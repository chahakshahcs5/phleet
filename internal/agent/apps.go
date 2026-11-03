//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"phleet/internal/agentproto"
)

const (
	appLogMaxBytes = 5 << 20
	healthTimeout  = 90 * time.Second
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type AppSpec struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Kind     string `json:"kind,omitempty"`
	Addon    bool   `json:"addon,omitempty"`
	Embedded bool   `json:"embedded,omitempty"`
	Role     string `json:"role,omitempty"`
	Epoch    int    `json:"epoch,omitempty"`
	Port     int    `json:"port"`
	// InternalPort is the loopback port the app binds to. The public Port is
	// owned by the agent's switchboard proxy, which lets a new release become
	// healthy before the old one is stopped.
	InternalPort int               `json:"internalPort,omitempty"`
	StartCmd     string            `json:"startCmd"`
	HealthPath   string            `json:"healthPath"`
	HealthCmd    string            `json:"healthCmd,omitempty"`
	LagCmd       string            `json:"lagCmd,omitempty"`
	LeaseSeconds int               `json:"leaseSeconds,omitempty"`
	ReleaseDir   string            `json:"releaseDir"`
	DataDir      string            `json:"dataDir,omitempty"`
	PrevRelease  string            `json:"prevRelease,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	DeploymentID string            `json:"deploymentId,omitempty"`
	DeployStatus string            `json:"deployStatus,omitempty"`
	StopSignal   string            `json:"stopSignal,omitempty"`
	StopTimeout  int               `json:"stopTimeout,omitempty"`
	UpdatedAt    int64             `json:"updatedAt"`
}

type appProcess struct {
	mu        sync.Mutex
	spec      AppSpec
	cmd       *exec.Cmd
	done      chan struct{}
	gen       int
	status    string
	pid       int
	restarts  int
	lagMS     int64
	lastError string
	stopping  bool
	deploying bool
	logPath   string
	startedAt time.Time
	proxy     *appProxy
}

func (a *Agent) appBase(slug string) string {
	return filepath.Join(a.home, "apps", slug)
}

func isAddonSpec(spec AppSpec) bool {
	if spec.Addon {
		return true
	}
	switch spec.Kind {
	case "database", "cache", "objectstore", "file":
		return true
	}
	return false
}

func appPort(spec AppSpec) int {
	if spec.InternalPort > 0 {
		return spec.InternalPort
	}
	return spec.Port
}

// internalPorts live above the public app/addon ranges and alternate so a new
// release can run beside the old one during a blue-green deploy.
func internalPort(public int, alternate bool) int {
	p := public + 16000
	if alternate {
		p += 1000
	}
	return p
}

func (a *Agent) appPIDPath(slug string) string {
	return filepath.Join(a.appBase(slug), "app.pid")
}

func writePIDFile(path string, pid int) {
	if pid <= 0 {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func readPIDFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processMatchesDir reports whether pid belongs to this user and was started
// from dir (its working directory) or references dir in its command line.
// Android denies /proc/net/tcp to apps, so cwd and cmdline are the portable
// way to attribute a process to a supervised app.
func processMatchesDir(pid int, dir string) bool {
	if pid <= 0 || pid == os.Getpid() || dir == "" || !sameUser(pid) {
		return false
	}
	if link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		if link == dir || strings.HasPrefix(link, dir+string(os.PathSeparator)) {
			return true
		}
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		if strings.Contains(string(b), dir) {
			return true
		}
	}
	return false
}

func processesInDir(dir string) []int {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if processMatchesDir(pid, dir) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (a *Agent) loadApps() error {
	b, err := os.ReadFile(a.appsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var specs []AppSpec
	if err := json.Unmarshal(b, &specs); err != nil {
		return err
	}
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	for _, s := range specs {
		if !slugRe.MatchString(s.Slug) {
			continue
		}
		a.apps[s.Slug] = &appProcess{spec: s, status: "stopped"}
	}
	return nil
}

func (a *Agent) saveApps() {
	a.appsSaveMu.Lock()
	defer a.appsSaveMu.Unlock()

	a.appsMu.Lock()
	specs := make([]AppSpec, 0, len(a.apps))
	for _, p := range a.apps {
		p.mu.Lock()
		specs = append(specs, p.spec)
		p.mu.Unlock()
	}
	a.appsMu.Unlock()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Slug < specs[j].Slug })
	b, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.appsPath), 0o700); err != nil {
		a.log.Warn("save apps", "err", err)
		return
	}
	tmp := a.appsPath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		a.log.Warn("save apps", "err", err)
		return
	}
	if err := os.Rename(tmp, a.appsPath); err != nil {
		a.log.Warn("replace apps state", "err", err)
	}
}

func (a *Agent) ensureApp(slug, name string, port int) *appProcess {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	p, ok := a.apps[slug]
	if !ok {
		p = &appProcess{
			spec:   AppSpec{Slug: slug, Name: name, Port: port},
			status: "stopped",
		}
		a.apps[slug] = p
	}
	return p
}

// restoreApps brings supervised processes back after an agent/phone restart.
func (a *Agent) restoreApps() {
	a.appsMu.Lock()
	specs := make([]AppSpec, 0, len(a.apps))
	for _, p := range a.apps {
		p.mu.Lock()
		specs = append(specs, p.spec)
		p.mu.Unlock()
	}
	a.appsMu.Unlock()
	go func() {
		for i, spec := range specs {
			if spec.StartCmd == "" || spec.ReleaseDir == "" {
				continue
			}
			if i > 0 {
				select {
				case <-time.After(750 * time.Millisecond):
				case <-a.ctx.Done():
					return
				}
			}
			if _, err := os.Stat(spec.ReleaseDir); err != nil {
				a.log.Warn("release directory missing, not restoring", "slug", spec.Slug, "dir", spec.ReleaseDir)
				continue
			}
			p := a.ensureApp(spec.Slug, spec.Name, spec.Port)
			if a.adoptRunning(p, spec) {
				continue
			}
			a.log.Info("restoring app", "slug", spec.Slug, "port", spec.Port)
			if err := a.startProcess(p, spec); err != nil {
				a.log.Warn("restore app failed", "slug", spec.Slug, "err", err)
			}
		}
	}()
}

// adoptRunning re-attaches to a process that is already serving the app's port
// after an agent restart. Android can keep the old process group alive when the
// agent is killed, and /proc/net/tcp is not readable there, so the pidfile plus
// the process working directory are the reliable evidence.
func (a *Agent) adoptRunning(p *appProcess, spec AppSpec) bool {
	if spec.Embedded || spec.Port <= 0 || !portListening(appPort(spec)) {
		return false
	}
	if !isAddonSpec(spec) {
		if spec.InternalPort == 0 {
			spec.InternalPort = internalPort(spec.Port, false)
		}
		if p.proxy == nil {
			proxy, err := newAppProxy(spec.Port)
			if err != nil {
				return false
			}
			p.proxy = proxy
		}
		p.proxy.setTarget(spec.InternalPort)
	}
	pid := readPIDFile(a.appPIDPath(spec.Slug))
	if pid > 0 && !processMatchesDir(pid, spec.ReleaseDir) {
		pid = 0
	}
	if pid == 0 {
		if pids := processesInDir(spec.ReleaseDir); len(pids) > 0 {
			pid = pids[0]
		}
	}
	if pid == 0 {
		return false
	}
	p.mu.Lock()
	p.spec = spec
	p.cmd = nil
	p.done = nil
	p.gen++
	p.pid = pid
	p.status = "running"
	p.stopping = false
	p.deploying = false
	p.lastError = ""
	p.logPath = filepath.Join(a.appBase(spec.Slug), "logs", "app.log")
	p.startedAt = time.Now()
	p.mu.Unlock()
	writePIDFile(a.appPIDPath(spec.Slug), pid)
	a.log.Info("adopted running app", "slug", spec.Slug, "pid", pid, "port", spec.Port)
	return true
}

func (a *Agent) startApp(spec AppSpec) error {
	if !slugRe.MatchString(spec.Slug) {
		return fmt.Errorf("invalid slug %q", spec.Slug)
	}
	p := a.ensureApp(spec.Slug, spec.Name, spec.Port)
	return a.startProcess(p, spec)
}

func (a *Agent) startProcess(p *appProcess, spec AppSpec) error {
	a.stopProcess(p)
	if err := a.spawnProcess(p, spec); err != nil {
		return err
	}
	if p.proxy != nil && !isAddonSpec(spec) {
		p.proxy.setTarget(appPort(spec))
	}
	return nil
}

// spawnProcess starts a process without stopping the current one. It is used
// by blue-green deploy to bring the new release up beside the old release.
func (a *Agent) spawnProcess(p *appProcess, spec AppSpec) error {
	if strings.TrimSpace(spec.StartCmd) == "" {
		// Embedded engines (SQLite) have no process. They are "running" once
		// their file exists; the provisioning step owns the health check.
		if spec.Embedded || strings.EqualFold(spec.Kind, "file") {
			if _, err := os.Stat(spec.ReleaseDir); err != nil {
				return fmt.Errorf("data directory missing: %w", err)
			}
			p.mu.Lock()
			p.gen++
			p.spec = spec
			p.cmd = nil
			p.done = nil
			p.status = "running"
			p.pid = 0
			p.lastError = ""
			p.stopping = false
			p.startedAt = time.Now()
			p.mu.Unlock()
			a.saveApps()
			return nil
		}
		return errors.New("start command is empty")
	}
	if _, err := os.Stat(spec.ReleaseDir); err != nil {
		return fmt.Errorf("release directory missing: %w", err)
	}
	listenPort := spec.Port
	if !isAddonSpec(spec) {
		if spec.InternalPort == 0 {
			spec.InternalPort = internalPort(spec.Port, false)
		}
		if p.proxy == nil {
			proxy, err := newAppProxy(spec.Port)
			if err != nil {
				return err
			}
			p.proxy = proxy
		}
		// Blue-green starts the new backend before the switch. Only point the
		// proxy at this port when it is the first backend or the same one.
		if cur := p.proxy.currentTarget(); cur == 0 || cur == spec.InternalPort {
			p.proxy.setTarget(spec.InternalPort)
		}
		listenPort = spec.InternalPort
	}
	if err := a.ensurePortFree(context.Background(), listenPort, spec.ReleaseDir, 3*time.Second); err != nil {
		return err
	}
	base := a.appBase(spec.Slug)
	logDir := filepath.Join(base, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "app.log")
	rotateLog(logPath, appLogMaxBytes)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}

	cmd := exec.Command(a.shPath, "-c", spec.StartCmd)
	cmd.Dir = spec.ReleaseDir
	cmd.Env = appEnv(spec)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}

	done := make(chan struct{})
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.spec = spec
	p.cmd = cmd
	p.done = done
	p.status = "running"
	p.pid = cmd.Process.Pid
	p.lastError = ""
	p.stopping = false
	p.logPath = logPath
	p.startedAt = time.Now()
	p.mu.Unlock()
	writePIDFile(a.appPIDPath(spec.Slug), cmd.Process.Pid)
	a.saveApps()
	a.log.Info("app started", "slug", spec.Slug, "pid", cmd.Process.Pid, "port", spec.Port)
	go a.waitProcess(p, cmd, logFile, done, gen)
	return nil
}

func (a *Agent) waitProcess(p *appProcess, cmd *exec.Cmd, logFile *os.File, done chan struct{}, gen int) {
	err := cmd.Wait()
	logFile.Close()
	p.mu.Lock()
	if p.gen != gen || p.cmd != cmd {
		p.mu.Unlock()
		close(done)
		return
	}
	p.cmd = nil
	p.pid = 0
	_ = os.Remove(a.appPIDPath(p.spec.Slug))
	if p.stopping {
		p.stopping = false
		p.status = "stopped"
		p.mu.Unlock()
		close(done)
		return
	}
	if p.deploying {
		p.status = "deploying"
		p.mu.Unlock()
		close(done)
		return
	}
	p.status = "crashed"
	// A process that stayed up for a minute is not in a crash loop; reset the
	// backoff so a later single failure does not wait 30 seconds.
	if time.Since(p.startedAt) > time.Minute {
		p.restarts = 0
	}
	p.restarts++
	if err != nil {
		p.lastError = err.Error()
	} else {
		p.lastError = "exited with code 0"
	}
	spec := p.spec
	delay := time.Duration(1<<minInt(p.restarts, 5)) * time.Second
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	p.mu.Unlock()
	close(done)
	a.saveApps()
	a.log.Warn("app exited, restarting", "slug", spec.Slug, "err", err, "in", delay)

	go func() {
		select {
		case <-time.After(delay):
		case <-a.ctx.Done():
			return
		}
		p.mu.Lock()
		should := p.cmd == nil && !p.stopping
		p.mu.Unlock()
		if should {
			if err := a.startProcess(p, spec); err != nil {
				a.log.Warn("restart failed", "slug", spec.Slug, "err", err)
			}
		}
	}()
}

func (a *Agent) stopProcess(p *appProcess) {
	p.mu.Lock()
	cmd := p.cmd
	done := p.done
	pid := p.pid
	spec := p.spec
	if cmd == nil && pid <= 0 {
		p.status = "stopped"
		p.stopping = false
		p.mu.Unlock()
		return
	}
	sig := stopSignal(spec.StopSignal)
	timeout := time.Duration(spec.StopTimeout) * time.Second
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	p.stopping = true
	p.mu.Unlock()
	a.log.Info("stopping app", "slug", spec.Slug, "pid", pid, "cmd", cmd != nil)

	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, sig)
	} else if pid > 0 {
		// Adopted process from a previous agent: no exec.Cmd, but the
		// process group is still ours.
		_ = syscall.Kill(-pid, sig)
		_ = syscall.Kill(pid, sig)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(timeout):
			if cmd != nil && cmd.Process != nil {
				killProcessGroup(cmd.Process.Pid)
			}
			if pid > 0 {
				killProcessGroup(pid)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	} else if pid > 0 {
		deadline := time.Now().Add(timeout)
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if processAlive(pid) {
			killProcessGroup(pid)
		}
	}
	// A child may have ignored the group signal or been re-parented. Anything
	// still running from the app directory belongs to this app.
	for _, leftover := range processesInDir(spec.ReleaseDir) {
		a.log.Warn("killing leftover app process", "slug", spec.Slug, "pid", leftover)
		killProcessGroup(leftover)
	}
	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
		p.pid = 0
		p.status = "stopped"
	}
	p.mu.Unlock()
	_ = os.Remove(a.appPIDPath(spec.Slug))
}

func (a *Agent) stopApp(slug string) error {
	a.appsMu.Lock()
	p := a.apps[slug]
	a.appsMu.Unlock()
	if p == nil {
		return fmt.Errorf("app %q is not installed", slug)
	}
	a.stopProcess(p)
	a.saveApps()
	return nil
}

func (a *Agent) restartApp(slug string) error {
	a.appsMu.Lock()
	p := a.apps[slug]
	a.appsMu.Unlock()
	if p == nil {
		return fmt.Errorf("app %q is not installed", slug)
	}
	p.mu.Lock()
	spec := p.spec
	p.mu.Unlock()
	return a.startProcess(p, spec)
}

func (a *Agent) rollbackApp(slug string) error {
	a.appsMu.Lock()
	p := a.apps[slug]
	a.appsMu.Unlock()
	if p == nil {
		return fmt.Errorf("app %q is not installed", slug)
	}
	p.mu.Lock()
	spec := p.spec
	p.mu.Unlock()
	if spec.PrevRelease == "" {
		return errors.New("no previous release to roll back to")
	}
	if _, err := os.Stat(spec.PrevRelease); err != nil {
		return fmt.Errorf("previous release is gone: %w", err)
	}
	next := spec
	next.ReleaseDir, next.PrevRelease = spec.PrevRelease, spec.ReleaseDir
	next.DeployStatus = "rolled_back"
	if err := a.startProcess(p, next); err != nil {
		return err
	}
	a.swapCurrent(spec.Slug, next.ReleaseDir)
	a.saveApps()
	if err := waitHealthy(context.Background(), appPort(next), next.HealthPath, 30*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cmd != nil
	}); err != nil {
		return fmt.Errorf("rolled back but the app is not healthy: %w", err)
	}
	return nil
}

func (a *Agent) deleteApp(slug string) error {
	a.appsMu.Lock()
	p := a.apps[slug]
	a.appsMu.Unlock()
	if p == nil {
		return nil
	}
	a.stopProcess(p)
	if p.proxy != nil {
		p.proxy.close()
		p.proxy = nil
	}
	a.appsMu.Lock()
	delete(a.apps, slug)
	a.appsMu.Unlock()
	a.saveApps()
	return nil
}

func (a *Agent) appControl(req agentproto.AppControlRequest) error {
	switch req.Action {
	case "start":
		a.appsMu.Lock()
		p := a.apps[req.Slug]
		a.appsMu.Unlock()
		if p == nil {
			return fmt.Errorf("app %q is not installed", req.Slug)
		}
		p.mu.Lock()
		spec := p.spec
		p.mu.Unlock()
		return a.startProcess(p, spec)
	case "stop":
		return a.stopApp(req.Slug)
	case "restart":
		return a.restartApp(req.Slug)
	case "rollback":
		return a.rollbackApp(req.Slug)
	case "delete":
		return a.deleteApp(req.Slug)
	default:
		return fmt.Errorf("unknown action %q", req.Action)
	}
}

func (a *Agent) appLogs(req agentproto.AppLogsRequest) (agentproto.AppLogsResponse, error) {
	if !slugRe.MatchString(req.Slug) {
		return agentproto.AppLogsResponse{}, fmt.Errorf("invalid slug")
	}
	path := filepath.Join(a.appBase(req.Slug), "logs", "app.log")
	f, err := os.Open(path)
	if err != nil {
		return agentproto.AppLogsResponse{Slug: req.Slug, Path: path}, nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return agentproto.AppLogsResponse{}, err
	}
	max := int64(req.Bytes)
	if max <= 0 || max > 512<<10 {
		max = 64 << 10
	}
	start := int64(0)
	if st.Size() > max {
		start = st.Size() - max
	}
	if _, err := f.Seek(start, 0); err != nil {
		return agentproto.AppLogsResponse{}, err
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.Read(buf); err != nil {
		return agentproto.AppLogsResponse{}, err
	}
	return agentproto.AppLogsResponse{
		Slug: req.Slug, Path: path, Data: buf, Size: st.Size(), Truncated: start > 0,
	}, nil
}

func (a *Agent) setDeployState(slug, deploymentID, status string) {
	a.appsMu.Lock()
	p := a.apps[slug]
	a.appsMu.Unlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	p.spec.DeploymentID = deploymentID
	p.spec.DeployStatus = status
	p.mu.Unlock()
	a.saveApps()
}

func (a *Agent) swapCurrent(slug, releaseDir string) {
	base := a.appBase(slug)
	link := filepath.Join(base, "current")
	_ = os.Remove(link)
	if err := os.Symlink(releaseDir, link); err != nil {
		a.log.Warn("symlink current", "slug", slug, "err", err)
	}
}

func (a *Agent) pruneReleases(slug string, keep int) {
	if keep <= 0 {
		keep = 5
	}
	dir := filepath.Join(a.appBase(slug), "releases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-keep; i++ {
		_ = os.RemoveAll(filepath.Join(dir, names[i]))
	}
}

func waitHealthy(ctx context.Context, port int, healthPath string, timeout time.Duration, alive func() bool) error {
	deadline := time.Now().Add(timeout)
	address := fmt.Sprintf("127.0.0.1:%d", port)
	client := &http.Client{Timeout: 3 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		if alive != nil && !alive() {
			return errors.New("process exited before it became healthy")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		conn, err := net.DialTimeout("tcp", address, 1500*time.Millisecond)
		if err != nil {
			last = err
			continue
		}
		conn.Close()
		if healthPath == "" {
			if alive != nil && !alive() {
				return errors.New("process exited before it became healthy")
			}
			return nil
		}
		url := fmt.Sprintf("http://%s%s", address, healthPath)
		resp, err := client.Get(url)
		if err != nil {
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode < 500 {
			if alive != nil && !alive() {
				return errors.New("process exited before it became healthy")
			}
			return nil
		}
		last = fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return last
}

// ---- port ownership ----

func portListening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ensurePortFree waits for our own process to release the port, then kills any
// orphan holdover (a previous agent process that outlived its supervisor). This
// prevents a new release from silently failing to bind while an old process
// keeps answering health checks and traffic.
func (a *Agent) ensurePortFree(ctx context.Context, port int, dir string, timeout time.Duration) error {
	if port <= 0 {
		return errors.New("invalid port")
	}
	deadline := time.Now().Add(timeout)
	for portListening(port) {
		if time.Now().After(deadline) {
			pids, _ := portHolders(port)
			if len(pids) == 0 && dir != "" {
				// Android denies /proc/net/tcp to apps; fall back to the
				// process working directory, which the kernel still exposes
				// to the same UID.
				pids = processesInDir(dir)
			}
			killed := 0
			for _, pid := range pids {
				if pid == os.Getpid() || !sameUser(pid) {
					continue
				}
				a.log.Warn("killing orphan process holding app port", "port", port, "pid", pid)
				killProcessGroup(pid)
				_ = syscall.Kill(pid, syscall.SIGKILL)
				killed++
			}
			if killed == 0 {
				return fmt.Errorf("port %d is in use by a process phleet does not own", port)
			}
			if !portListening(port) {
				return nil
			}
			return fmt.Errorf("port %d is still in use", port)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

// portHolders maps a listening TCP port to PIDs using /proc only, which works
// in Termux without lsof, ss or root.
func portHolders(port int) ([]int, error) {
	inodes := map[string]bool{}
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if i == 0 || strings.TrimSpace(line) == "" {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" {
				continue
			}
			idx := strings.LastIndex(f[1], ":")
			if idx < 0 {
				continue
			}
			p, err := strconv.ParseInt(f[1][idx+1:], 16, 32)
			if err != nil || int(p) != port {
				continue
			}
			inodes["socket:["+f[9]+"]"] = true
		}
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	var pids []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fds, err := os.ReadDir("/proc/" + e.Name() + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink("/proc/" + e.Name() + "/fd/" + fd.Name())
			if err != nil {
				continue
			}
			if inodes[link] {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}

func sameUser(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			f := strings.Fields(line)
			if len(f) < 2 {
				return false
			}
			uid, err := strconv.Atoi(f[1])
			return err == nil && uid == os.Geteuid()
		}
	}
	return false
}

func appEnv(spec AppSpec) []string {
	env := sanitizeEnv(os.Environ())
	// Addons must not inherit the app contract: HOST=0.0.0.0 in particular
	// would contradict the loopback-only bind in their own args. This applies
	// to every addon kind, not just databases and caches.
	if spec.Addon || spec.Kind == "database" || spec.Kind == "cache" || spec.Kind == "objectstore" || spec.Kind == "file" {
		keys := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+spec.Env[k])
		}
		return env
	}
	// The agent reaches apps over loopback. Binding 0.0.0.0 would expose the
	// port on Wi-Fi too, which contradicts "phones never accept inbound".
	// Users who explicitly want LAN access can override HOST in project env.
	env = append(env,
		"PORT="+fmt.Sprint(appPort(spec)),
		"HOST=127.0.0.1",
		"NODE_ENV=production",
		"PHLEET_APP="+spec.Slug,
		"PHLEET_PORT="+fmt.Sprint(spec.Port),
	)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+spec.Env[k])
	}
	return env
}

func rotateLog(path string, max int64) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < max {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// stopSignal maps the spec name to a POSIX signal. Postgres wants SIGINT (fast
// shutdown); MariaDB and Redis want SIGTERM. A SIGKILL here would force WAL/AOF
// recovery on the next start.
func stopSignal(name string) syscall.Signal {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "INT":
		return syscall.SIGINT
	case "QUIT":
		return syscall.SIGQUIT
	case "KILL":
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}
