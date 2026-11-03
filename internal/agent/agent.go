//go:build linux

// Package agent implements the phleet agent that runs inside Termux.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/hashicorp/yamux"

	"phleet/internal/agentproto"
	"phleet/internal/config"
	"phleet/internal/engines"
)

type Config struct {
	URL       string
	Token     string
	TokenPath string
	Name      string
	ID        string
	StateDir  string
	Roots     []string
}

type Agent struct {
	cfg   Config
	home  string
	roots []string
	log   *slog.Logger

	writeMu sync.Mutex
	ws      *websocket.Conn

	sessMu   sync.Mutex
	sessions map[string]*ptySession

	cpuMu   sync.Mutex
	prevCPU cpuTimes
	netPrev netCounters

	capsMu   sync.Mutex
	caps     map[string]any
	capsGen  int
	capsSent int

	shPath      string
	gitPath     string
	pkgManager  string
	batteryTool string
	httpClient  *http.Client

	batteryMu       sync.Mutex
	batteryOffUntil time.Time
	batteryBusy     bool
	battery         batteryReading

	metricsMu   sync.Mutex
	metrics     agentproto.Metrics
	metricsAt   time.Time
	metricsBusy bool

	ctx        context.Context
	appsMu     sync.Mutex
	apps       map[string]*appProcess
	appsPath   string
	appsSaveMu sync.Mutex
	deployMu   sync.Mutex
	addonMu    sync.Mutex

	tunMu   sync.Mutex
	tunSess *yamux.Session

	relayMu sync.Mutex
	relays  map[string]*relayListener

	lastCPMu sync.Mutex
	lastCP   time.Time

	tokenMu   sync.RWMutex
	token     string
	tokenPath string
	helloAck  chan struct{}
}

func New(cfg Config, log *slog.Logger) (*Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if cfg.Roots == nil {
		cfg.Roots = []string{home}
	}
	roots := make([]string, 0, len(cfg.Roots))
	for _, r := range cfg.Roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		roots = append(roots, filepath.Clean(abs))
	}
	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(home, ".phleet")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	// Tools installed on demand (rclone's static fallback) live next to the
	// agent. Put them on PATH once so lookTool and every supervised child can
	// find them.
	binDir := filepath.Join(cfg.StateDir, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	_ = os.Setenv("PHLEET_STATE_DIR", cfg.StateDir)
	if path := os.Getenv("PATH"); !strings.Contains(path, binDir) {
		_ = os.Setenv("PATH", binDir+string(os.PathListSeparator)+path)
	}
	tokenPath := cfg.TokenPath
	if tokenPath == "" {
		tokenPath = filepath.Join(cfg.StateDir, "agent.token")
	}
	a := &Agent{
		cfg:       cfg,
		home:      home,
		roots:     roots,
		log:       log,
		sessions:  map[string]*ptySession{},
		apps:      map[string]*appProcess{},
		appsPath:  filepath.Join(cfg.StateDir, "apps.json"),
		relays:    map[string]*relayListener{},
		token:     cfg.Token,
		tokenPath: tokenPath,
		helloAck:  make(chan struct{}, 1),
	}
	if a.cfg.ID == "" {
		a.cfg.ID = loadID(cfg.StateDir)
	}
	// Debian/Fedora put engines outside PATH (postgresql keeps initdb and
	// pg_ctl in /usr/lib/postgresql/<version>/bin), so add those before
	// probing capabilities or rendering any command.
	applyDistroToolPaths()
	a.setCaps(probeCaps())
	a.pkgManager = packageManager()
	// Termux keeps its CA bundle outside Go's default Linux paths and Android
	// has no /etc/resolv.conf; without these Go TLS and DNS fail on device.
	applyTermuxTLS()
	a.httpClient = newDoHHTTPClient()
	if sh, ok := lookTool("sh"); ok {
		a.shPath = sh
	} else if _, err := os.Stat("/system/bin/sh"); err == nil {
		a.shPath = "/system/bin/sh"
	} else {
		a.shPath = "/data/data/com.termux/files/usr/bin/sh"
	}
	if git, ok := lookTool("git"); ok {
		a.gitPath = git
	}
	if batt, ok := lookTool("termux-battery-status"); ok {
		a.batteryTool = batt
	}
	return a, nil
}

// lookTool resolves an executable with os.Stat instead of exec.LookPath.
// exec.LookPath uses faccessat2 on arm64, which Android 10's seccomp policy
// kills with SIGSYS; plain stat is allowed.
func lookTool(name string) (string, bool) {
	if strings.ContainsRune(name, os.PathSeparator) || strings.Contains(name, "/") {
		if st, err := os.Stat(name); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return name, true
		}
		return "", false
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate, true
		}
	}
	return "", false
}

func loadID(dir string) string {
	p := filepath.Join(dir, "agent-id")
	if b, err := os.ReadFile(p); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	id := uuid.NewString()
	_ = os.WriteFile(p, []byte(id+"\n"), 0o600)
	return id
}

func (a *Agent) ID() string { return a.cfg.ID }

func (a *Agent) currentToken() string {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.token
}

func (a *Agent) setToken(tok string) {
	if strings.TrimSpace(tok) == "" {
		return
	}
	a.tokenMu.Lock()
	a.token = tok
	a.tokenMu.Unlock()
	tmp := a.tokenPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		a.log.Warn("save device token", "err", err)
		return
	}
	if err := os.Rename(tmp, a.tokenPath); err != nil {
		a.log.Warn("replace device token", "err", err)
	}
}

func (a *Agent) signalHelloAck() {
	select {
	case a.helloAck <- struct{}{}:
	default:
	}
}

// Run connects and reconnects until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	a.ctx = ctx
	if err := a.loadApps(); err != nil {
		a.log.Warn("load apps", "err", err)
	}
	a.restoreApps()
	go a.lagLoop(ctx)
	go a.leaseLoop(ctx)
	go a.capsLoop(ctx)
	backoff := time.Second
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt++
		a.log.Info("connecting", "attempt", attempt)
		start := time.Now()
		err := a.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > 60*time.Second {
			backoff = time.Second
		}
		jitter := time.Duration(rand.Int63n(int64(backoff)/2 + 1))
		wait := backoff + jitter
		a.log.Warn("connection lost, retrying", "err", err, "in", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (a *Agent) connect(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ws, resp, err := websocket.Dial(dctx, a.cfg.URL, &websocket.DialOptions{
		HTTPClient: a.httpClient,
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + a.currentToken()},
			"User-Agent":    []string{"phleet-agent/" + config.Version},
		},
	})
	cancel()
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial: %w (http %d)", err, resp.StatusCode)
		}
		return fmt.Errorf("dial: %w", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	defer a.closeAllPTYs()
	ws.SetReadLimit(4 << 20)
	a.markCPMessage()
	a.writeMu.Lock()
	a.ws = ws
	a.writeMu.Unlock()
	defer func() {
		a.writeMu.Lock()
		if a.ws == ws {
			a.ws = nil
		}
		a.writeMu.Unlock()
	}()

	hostname, _ := os.Hostname()
	if hostname == "" || strings.EqualFold(hostname, "localhost") {
		// Android always reports "localhost" as the kernel hostname. Without
		// this fallback every phone in the panel shows the same useless value;
		// the agent's configured name is at least device-specific.
		hostname = a.cfg.Name
	}
	hello := agentproto.Hello{
		ID:           a.cfg.ID,
		Name:         a.cfg.Name,
		Hostname:     hostname,
		OS:           osLabel(),
		Arch:         runtime.GOARCH,
		AgentVersion: config.Version,
		Home:         a.home,
		User:         username(),
		Caps:         a.capsSnapshot(),
	}
	if err := a.send(agentproto.TypeHello, "", hello); err != nil {
		return err
	}
	a.markCapsSent()
	a.log.Info("connected", "url", a.cfg.URL, "id", a.cfg.ID)

	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	go a.heartbeatLoop(connCtx)
	go a.tunnelLoop(connCtx)

	for {
		rctx, rcancel := context.WithTimeout(connCtx, 90*time.Second)
		typ, data, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			return err
		}
		a.markCPMessage()
		if typ != websocket.MessageText {
			continue
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		a.dispatch(connCtx, env)
	}
}

func (a *Agent) send(typ, replyTo string, data any) error {
	env := agentproto.Envelope{Type: typ, ReplyTo: replyTo}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		env.Data = raw
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.ws == nil {
		return errors.New("not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.ws.Write(ctx, websocket.MessageText, b)
}

func (a *Agent) reply(env agentproto.Envelope, data any, err error) {
	if err != nil {
		_ = a.send("reply", env.ID, nil)
		return
	}
	_ = a.send("reply", env.ID, data)
}

func (a *Agent) dispatch(ctx context.Context, env agentproto.Envelope) {
	switch env.Type {
	case agentproto.TypeHelloAck:
		var ack agentproto.HelloAck
		if err := json.Unmarshal(env.Data, &ack); err == nil && ack.OK {
			if ack.Token != "" {
				a.setToken(ack.Token)
			}
			a.signalHelloAck()
		}
	case agentproto.TypeExec:
		var req agentproto.ExecRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			res, err := a.exec(req)
			a.complete(env, res, err)
		}()
	case agentproto.TypeFSList:
		var req agentproto.FSListRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			res, err := a.list(req)
			a.complete(env, res, err)
		}()
	case agentproto.TypeFSRead:
		var req agentproto.FSReadRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			res, err := a.readFile(req)
			a.complete(env, res, err)
		}()
	case agentproto.TypeFSWrite:
		var req agentproto.FSWriteRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.writeFile(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeFSWriteChunk:
		var req agentproto.FSWriteChunkRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.writeChunk(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeFSExtract:
		var req agentproto.FSExtractRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			res, err := a.extractZip(req)
			a.complete(env, res, err)
		}()
	case agentproto.TypeFSMkdir:
		var req agentproto.FSMkdirRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.mkdir(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeFSRename:
		var req agentproto.FSRenameRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.rename(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeFSDelete:
		var req agentproto.FSDeleteRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.remove(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeFSStat:
		var req agentproto.FSListRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			res, err := a.stat(req.Path)
			a.complete(env, res, err)
		}()
	case agentproto.TypeProcList:
		go func() {
			res, err := a.processes()
			a.complete(env, res, err)
		}()
	case agentproto.TypeDeployRequest:
		var req agentproto.DeployRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go a.handleDeploy(a.ctx, req, func(ev agentproto.DeployEvent) {
			_ = a.send(agentproto.TypeDeployEvent, "", ev)
		})
	case agentproto.TypeAppLogs:
		var req agentproto.AppLogsRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			res, err := a.appLogs(req)
			a.complete(env, res, err)
		}()
	case agentproto.TypeAppControl:
		var req agentproto.AppControlRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.appControl(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeAddonProvision:
		var req agentproto.AddonSpec
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go a.handleProvisionAddon(req, a.addonEmitter())
	case agentproto.TypeAddonControl:
		var req agentproto.AddonControlRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.addonControl(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeAddonBackup:
		var req agentproto.AddonBackupRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go a.handleAddonBackup(req, a.addonEmitter())
	case agentproto.TypeAddonRestore:
		var req agentproto.AddonRestoreRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go a.handleAddonRestore(req, a.addonEmitter())
	case agentproto.TypeAddonVerify:
		var req agentproto.AddonVerifyRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.handleAddonVerify(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeAddonRelay:
		var req agentproto.AddonRelaySpec
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.relayControl(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeAddonPromote:
		var req agentproto.AddonPromoteRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.handleAddonPromote(req, a.addonEmitter())
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypeAddonLease:
		var req agentproto.AddonLeaseRequest
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		go func() {
			err := a.handleAddonLease(req)
			a.complete(env, agentproto.Empty{}, err)
		}()
	case agentproto.TypePTYOpen:
		var req agentproto.PTYOpen
		if err := json.Unmarshal(env.Data, &req); err != nil {
			a.fail(env, err)
			return
		}
		if err := a.openPTY(req); err != nil {
			a.fail(env, err)
		}
	case agentproto.TypePTYData:
		var req agentproto.PTYData
		if err := json.Unmarshal(env.Data, &req); err == nil {
			a.writePTY(req.Session, req.Data)
		}
	case agentproto.TypePTYResize:
		var req agentproto.PTYResize
		if err := json.Unmarshal(env.Data, &req); err == nil {
			a.resizePTY(req.Session, req.Cols, req.Rows)
		}
	case agentproto.TypePTYClose:
		var req agentproto.PTYSession
		if err := json.Unmarshal(env.Data, &req); err == nil {
			a.closePTY(req.Session)
		}
	}
	_ = ctx
}

func (a *Agent) fail(env agentproto.Envelope, err error) {
	a.log.Warn("request failed", "type", env.Type, "err", err)
	_ = a.sendEnvelope(agentproto.Envelope{Type: "reply", ReplyTo: env.ID, Error: err.Error()})
}

func (a *Agent) complete(env agentproto.Envelope, data any, err error) {
	if err != nil {
		a.log.Warn("request failed", "type", env.Type, "err", err)
		b, _ := json.Marshal(err.Error())
		e := agentproto.Envelope{Type: "reply", ReplyTo: env.ID, Error: err.Error(), Data: b}
		_ = a.sendEnvelope(e)
		return
	}
	_ = a.send("reply", env.ID, data)
}

func (a *Agent) sendEnvelope(env agentproto.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.ws == nil {
		return errors.New("not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.ws.Write(ctx, websocket.MessageText, b)
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	a.sendHeartbeat()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sendHeartbeat()
		}
	}
}

func (a *Agent) sendHeartbeat() {
	a.refreshMetrics()
	a.metricsMu.Lock()
	m := a.metrics
	if a.metricsAt.IsZero() {
		// First heartbeat: no sample yet. At=0 tells the control plane to keep
		// the heartbeat (apps and caps still matter) but not store a row of
		// zeroes, which used to put a fake 0% CPU / 0 °C dip in every chart.
		m = agentproto.Metrics{BatteryPct: -1}
	}
	a.metricsMu.Unlock()
	hb := agentproto.Heartbeat{
		Metrics: m,
		Apps:    a.appStatuses(),
		// Send capabilities only when they change. The control plane otherwise
		// rewrites the same JSON several times a minute per phone.
		Caps: a.capsIfChanged(),
	}
	if err := a.send(agentproto.TypeHeartbeat, "", hb); err != nil {
		a.log.Debug("heartbeat failed", "err", err)
	}
}

// refreshMetrics samples host metrics in the background. A single stuck sysfs
// or helper read must never stop heartbeats: without them the hub closes the
// control connection after 90 seconds and the phone looks offline.
func (a *Agent) refreshMetrics() {
	a.metricsMu.Lock()
	stale := time.Since(a.metricsAt) > 4*time.Second
	start := stale && !a.metricsBusy
	if start {
		a.metricsBusy = true
	}
	a.metricsMu.Unlock()
	if !start {
		return
	}
	go func() {
		m := a.collectMetrics()
		m.At = time.Now().Unix()
		a.metricsMu.Lock()
		a.metrics = m
		a.metricsAt = time.Now()
		a.metricsBusy = false
		a.metricsMu.Unlock()
	}()
}

func (a *Agent) appStatuses() []agentproto.AppStatus {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	out := make([]agentproto.AppStatus, 0, len(a.apps))
	for _, p := range a.apps {
		p.mu.Lock()
		out = append(out, agentproto.AppStatus{
			Name:         p.spec.Name,
			Slug:         p.spec.Slug,
			Kind:         p.spec.Kind,
			Role:         p.spec.Role,
			Epoch:        p.spec.Epoch,
			State:        p.status,
			Port:         p.spec.Port,
			PID:          p.pid,
			Restarts:     p.restarts,
			LagMS:        p.lagMS,
			LastError:    p.lastError,
			DeploymentID: p.spec.DeploymentID,
			DeployStatus: p.spec.DeployStatus,
		})
		p.mu.Unlock()
	}
	return out
}

func (a *Agent) exec(req agentproto.ExecRequest) (agentproto.ExecResponse, error) {
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = 20
	}
	if req.TimeoutSec > 300 {
		req.TimeoutSec = 300
	}
	cwd := a.home
	if req.Cwd != "" {
		if p, err := a.resolve(req.Cwd); err == nil {
			cwd = p
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, a.shPath, "-c", req.Command)
	prepareCommandGroup(cmd)
	cmd.Dir = cwd
	cmd.Env = append(sanitizeEnv(os.Environ()), "TERM=dumb", "CI=1")
	out, err := cmd.CombinedOutput()
	res := agentproto.ExecResponse{
		Output:   string(out),
		ExitCode: 0,
		Duration: time.Since(start).Milliseconds(),
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			res.ExitCode = 124
			res.Output += "\n[timed out]"
		} else {
			return res, err
		}
	}
	if len(res.Output) > 1<<20 {
		res.Output = res.Output[:1<<20] + "\n[truncated]"
	}
	return res, nil
}

// ---- capability probe ----

func probeCaps() map[string]any {
	tools := []string{
		"git", "node", "npm", "pnpm", "yarn", "bun", "deno",
		"python3", "pip3", "go", "rustc", "cargo", "java", "php", "ruby",
		"tmux", "adb", "proot-distro", "cloudflared", "ffmpeg", "sqlite3", "sshd",
		"docker", "podman", "mariadb", "psql", "redis-server", "nginx", "caddy",
		"curl", "wget", "tar", "zip", "unzip",
		"termux-battery-status", "termux-wake-lock",
	}
	found := map[string]bool{}
	for _, t := range tools {
		if _, ok := lookTool(t); ok {
			found[t] = true
		}
	}
	installed := map[string]bool{}
	for _, e := range engines.All() {
		ok := len(e.Binaries) > 0
		for _, bin := range e.Binaries {
			if _, exists := lookTool(bin); !exists {
				ok = false
				break
			}
		}
		installed[e.ID] = ok
	}
	thermal := 0
	if z, err := filepath.Glob("/sys/class/thermal/thermal_zone*"); err == nil {
		thermal = len(z)
	}
	_, batteryErr := os.Stat("/sys/class/power_supply/battery")
	home, _ := os.UserHomeDir()
	termux := os.Getenv("TERMUX_VERSION") != "" || strings.Contains(home, "com.termux")
	hasPkg := lookToolBool("pkg")
	manager := packageManager()
	// Termux's prefix is owned by the agent user, so pkg never needs root. apt
	// does: either the agent is root or the sudoers rule allows package
	// management without a password.
	pkgInstall := manager == "pkg" || os.Geteuid() == 0 || (manager != "" && canSudo())
	// A Debian chroot on Android still has /system bind-mounted, so build.prop
	// alone is not enough: a real distro has /etc/os-release, Android does not.
	android := fileExists("/system/build.prop") && !fileExists("/etc/os-release")
	// userland is what projects and addons need: a tool environment beyond the
	// Android shell. Termux, a package manager, a distro chroot/VM and a plain
	// Linux host all qualify. A rooted phone running the agent from
	// /data/local/tmp has none of these and is monitoring-only.
	userland := true
	if android {
		userland = termux || hasPkg || manager != "" ||
			(found["git"] && (found["node"] || found["python3"] || found["go"]))
	}
	return map[string]any{
		"root":          os.Geteuid() == 0,
		"termux":        termux,
		"userland":      userland,
		"android":       android,
		"pkgManager":    manager,
		"pkgInstall":    pkgInstall,
		"ip":            primaryIPv4(),
		"sshPort":       sshPort(),
		"termuxApi":     found["termux-battery-status"],
		"adb":           found["adb"],
		"pkg":           hasPkg,
		"tools":         found,
		"engines":       installed,
		"thermalZones":  thermal,
		"battery":       batteryErr == nil,
		"chargeControl": fileExists("/sys/class/power_supply/battery/input_current_limit"),
		"cpus":          runtime.NumCPU(),
		"home":          home,
		// The agent understands hello.ack and can persist a per-device token.
		"deviceTokens": true,
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func lookToolBool(name string) bool {
	_, ok := lookTool(name)
	return ok
}

// primaryIPv4 is the phone's LAN address. It lets the panel (and the operator)
// reach the device over SSH while the agent itself is offline, which is the
// fastest way to tell "phone/network down" from "agent down".
func primaryIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String()
		}
	}
	// Some Android builds hide interface addresses from getifaddrs. A connected
	// UDP socket reports the source address the routing table would use, without
	// sending a packet.
	if c, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		addr := c.LocalAddr()
		_ = c.Close()
		if ua, ok := addr.(*net.UDPAddr); ok && ua.IP != nil && !ua.IP.IsLoopback() && ua.IP.To4() != nil {
			return ua.IP.String()
		}
	}
	// Some ROMs expose neither getifaddrs addresses nor a routable UDP source to
	// the app. Fall back to the iproute2 output the operator sees in Termux.
	if out, err := exec.Command("sh", "-c",
		"ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	if out, err := exec.Command("sh", "-c",
		"ifconfig 2>/dev/null | awk '/inet /{print $2}' | grep -v '^127\\.' | head -1").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	return ""
}

// sshPort reports the local SSH port when sshd is listening on loopback.
func sshPort() string {
	for _, p := range []string{"8022", "22"} {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+p, 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return p
		}
	}
	return ""
}

func username() string {
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	if v := os.Getenv("LOGNAME"); v != "" {
		return v
	}
	return fmt.Sprintf("uid-%d", os.Geteuid())
}

func androidRelease() string {
	if b, err := os.ReadFile("/system/build.prop"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "ro.build.version.release=") {
				return "android-" + strings.TrimSpace(strings.TrimPrefix(line, "ro.build.version.release="))
			}
		}
	}
	return ""
}

func (a *Agent) processMap() map[int]string {
	out := map[int]string{}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid := 0
		if _, err := fmt.Sscanf(e.Name(), "%d", &pid); err != nil || pid <= 0 {
			continue
		}
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil && len(b) > 0 {
			s := strings.ReplaceAll(strings.TrimRight(string(b), "\x00"), "\x00", " ")
			if len(s) > 160 {
				s = s[:160]
			}
			out[pid] = s
		}
	}
	return out
}

// Kill a process tree; used by the deploy supervisor later.
func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// prepareCommandGroup makes command timeouts kill the whole tree, not just the
// shell. Without this a timed-out build leaves npm/node children behind, and a
// grandchild holding the stdout pipe can keep cmd.Wait blocked past the
// deadline.
func prepareCommandGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
