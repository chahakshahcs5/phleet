package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"phleet/internal/cloudflare"
	"phleet/internal/config"
)

const tunnelServiceName = "cloudflared"

type tunnelStatus struct {
	Configured       bool   `json:"configured"`
	PublicURL        string `json:"publicUrl"`
	TunnelID         string `json:"tunnelId"`
	TunnelName       string `json:"tunnelName"`
	ServiceInstalled bool   `json:"serviceInstalled"`
	ServiceStatus    string `json:"serviceStatus"`
	StartType        string `json:"startType"`
	Mode             string `json:"mode"`
	Running          bool   `json:"running"`
	PID              int    `json:"pid,omitempty"`
	Binary           string `json:"binary,omitempty"`
	LogPath          string `json:"logPath,omitempty"`
	Error            string `json:"error,omitempty"`
}

type tunnelManager struct {
	cfg *config.Config
	log *slog.Logger

	mu           sync.Mutex
	proc         *exec.Cmd
	pid          int
	running      bool
	intentional  bool
	binary       string
	lastErr      string
	startedAt    time.Time
	restartDelay time.Duration
	restartTimer *time.Timer
}

func newTunnelManager(cfg *config.Config, log *slog.Logger) *tunnelManager {
	return &tunnelManager{cfg: cfg, log: log, restartDelay: 2 * time.Second}
}

// Run is called once by the control plane. If the Windows service exists, the
// Service Control Manager owns the connector. Otherwise a saved tunnel token
// starts a supervised child process so a fresh install still comes online.
func (m *tunnelManager) Run(ctx context.Context) {
	if runtime.GOOS == "windows" {
		if _, err := queryWindowsService(tunnelServiceName); err == nil {
			return
		} else if !isServiceNotInstalled(err) {
			m.log.Warn("tunnel: cannot query service", "err", err)
			return
		}
	}
	conf, err := cloudflare.LoadConfig(m.cfg.CloudflaredConfig())
	if err != nil || strings.TrimSpace(conf.TunnelToken) == "" {
		return
	}
	if err := m.startProcess(); err != nil {
		m.log.Warn("tunnel auto-start failed", "err", err)
	}
	<-ctx.Done()
	_ = m.stopProcess()
}

func (m *tunnelManager) Status() tunnelStatus {
	st := tunnelStatus{
		PublicURL: m.cfg.PublicURL,
		LogPath:   filepath.Join(m.cfg.DataDir, "cloudflared.log"),
	}
	if conf, err := cloudflare.LoadConfig(m.cfg.CloudflaredConfig()); err != nil {
		st.Error = "cloudflare config not found; run phleet setup-cloudflare"
	} else {
		st.Configured = strings.TrimSpace(conf.TunnelToken) != ""
		st.TunnelID = conf.TunnelID
		st.TunnelName = conf.TunnelName
		if st.PublicURL == "" && conf.Domain != "" {
			st.PublicURL = "https://fleet." + conf.Domain
		}
	}

	if runtime.GOOS == "windows" {
		svc, err := queryWindowsService(tunnelServiceName)
		if err == nil {
			st.ServiceInstalled = true
			st.ServiceStatus = serviceStateLabel(svc.State)
			st.StartType = serviceStartTypeLabel(svc.StartType)
			st.Mode = "service"
			st.Running = svc.State == 4
			if st.Error == "" {
				st.Error = ""
			}
			return st
		}
		if !isServiceNotInstalled(err) {
			st.Error = err.Error()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proc != nil {
		st.Mode = "process"
		st.Running = m.running
		st.PID = m.pid
		st.Binary = m.binary
	} else if st.Mode == "" {
		st.Mode = "none"
	}
	if m.lastErr != "" {
		st.Error = m.lastErr
	}
	return st
}

func (m *tunnelManager) Action(action string) (tunnelStatus, error) {
	switch action {
	case "start", "stop", "restart":
	default:
		return m.Status(), fmt.Errorf("unknown tunnel action %q", action)
	}

	if runtime.GOOS == "windows" {
		svc, err := queryWindowsService(tunnelServiceName)
		if err == nil {
			switch action {
			case "start":
				err = m.startWindowsService(svc.State)
			case "stop":
				err = m.stopWindowsService(svc.State)
			case "restart":
				err = m.restartWindowsService(svc.State)
			}
			return m.Status(), err
		}
		if !isServiceNotInstalled(err) {
			return m.Status(), fmt.Errorf("cannot query cloudflared service: %w", err)
		}
	}

	var err error
	switch action {
	case "start":
		err = m.startProcess()
	case "stop":
		err = m.stopProcess()
	case "restart":
		_ = m.stopProcess()
		err = m.startProcess()
	}
	return m.Status(), err
}

func (m *tunnelManager) startProcess() error {
	m.mu.Lock()
	if m.proc != nil && m.running {
		m.mu.Unlock()
		return nil
	}
	if m.restartTimer != nil {
		m.restartTimer.Stop()
		m.restartTimer = nil
	}
	m.mu.Unlock()

	conf, err := cloudflare.LoadConfig(m.cfg.CloudflaredConfig())
	if err != nil {
		return fmt.Errorf("cloudflare config: %w", err)
	}
	if strings.TrimSpace(conf.TunnelToken) == "" {
		return errors.New("tunnel token is missing from cloudflare.json")
	}
	binary, err := m.findBinary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.cfg.DataDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(m.cfg.DataDir, "cloudflared.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open cloudflared log: %w", err)
	}
	cmd := exec.Command(binary, "tunnel", "--no-autoupdate", "run", "--token", conf.TunnelToken)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	hideProcessWindow(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start cloudflared: %w", err)
	}

	m.mu.Lock()
	m.proc = cmd
	m.pid = cmd.Process.Pid
	m.running = true
	m.intentional = false
	m.binary = binary
	m.lastErr = ""
	m.startedAt = time.Now()
	m.restartDelay = 2 * time.Second
	m.mu.Unlock()
	m.log.Info("tunnel process started", "pid", cmd.Process.Pid, "binary", binary)
	go m.waitProcess(cmd, logFile)
	return nil
}

func (m *tunnelManager) waitProcess(cmd *exec.Cmd, logFile *os.File) {
	err := cmd.Wait()
	_ = logFile.Close()

	m.mu.Lock()
	if m.proc != cmd {
		m.mu.Unlock()
		return
	}
	m.proc = nil
	m.pid = 0
	m.running = false
	intentional := m.intentional
	m.intentional = false
	if !intentional {
		if err != nil {
			m.lastErr = err.Error()
		} else {
			m.lastErr = "cloudflared exited"
		}
	}
	if time.Since(m.startedAt) > 60*time.Second {
		m.restartDelay = 2 * time.Second
	}
	delay := m.restartDelay
	if delay <= 0 {
		delay = 2 * time.Second
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	m.restartDelay = delay * 2
	m.mu.Unlock()

	if intentional {
		m.log.Info("tunnel process stopped")
		return
	}
	m.log.Warn("tunnel process exited; restarting", "err", err, "in", delay)
	m.scheduleRestart(delay)
}

func (m *tunnelManager) scheduleRestart(delay time.Duration) {
	m.mu.Lock()
	if m.restartTimer != nil {
		m.mu.Unlock()
		return
	}
	m.restartTimer = time.AfterFunc(delay, func() {
		m.mu.Lock()
		m.restartTimer = nil
		m.mu.Unlock()
		if runtime.GOOS == "windows" {
			if _, err := queryWindowsService(tunnelServiceName); err == nil {
				return
			}
		}
		if err := m.startProcess(); err != nil {
			m.mu.Lock()
			m.lastErr = err.Error()
			m.mu.Unlock()
			m.scheduleRestart(30 * time.Second)
		}
	})
	m.mu.Unlock()
}

func (m *tunnelManager) stopProcess() error {
	m.mu.Lock()
	if m.restartTimer != nil {
		m.restartTimer.Stop()
		m.restartTimer = nil
	}
	cmd := m.proc
	if cmd == nil || cmd.Process == nil {
		m.running = false
		m.pid = 0
		m.mu.Unlock()
		return nil
	}
	m.intentional = true
	m.mu.Unlock()

	_ = cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		running := m.running
		m.mu.Unlock()
		if !running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (m *tunnelManager) findBinary() (string, error) {
	dirs := []string{}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	dirs = append(dirs, m.cfg.BinDir(), m.cfg.DataDir)
	names := []string{"cloudflared.exe", "cloudflared"}
	for _, dir := range dirs {
		for _, name := range names {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("cloudflared binary not found next to phleetd or in PATH")
}

// ---- Windows Service Control Manager ----

type winServiceState struct {
	State     int
	StartType int
}

var (
	serviceStatePattern     = regexp.MustCompile(`(?im)^\s*STATE\s*:\s*(\d+)`)
	serviceStartTypePattern = regexp.MustCompile(`(?im)^\s*START_TYPE\s*:\s*(\d+)`)
)

func queryWindowsService(name string) (winServiceState, error) {
	out, err := exec.Command("sc.exe", "query", name).CombinedOutput()
	if err != nil {
		return winServiceState{}, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	state := parseServiceState(out)
	if state == 0 {
		return winServiceState{}, fmt.Errorf("cannot parse service state: %s", strings.TrimSpace(string(out)))
	}
	startType := 0
	if qc, err := exec.Command("sc.exe", "qc", name).CombinedOutput(); err == nil {
		startType = parseServiceStartType(qc)
	}
	return winServiceState{State: state, StartType: startType}, nil
}

func parseServiceState(out []byte) int {
	m := serviceStatePattern.FindSubmatch(out)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

func parseServiceStartType(out []byte) int {
	m := serviceStartTypePattern.FindSubmatch(out)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

func serviceStateLabel(v int) string {
	switch v {
	case 1:
		return "Stopped"
	case 2:
		return "Starting"
	case 3:
		return "Stopping"
	case 4:
		return "Running"
	case 5:
		return "Continuing"
	case 6:
		return "Pausing"
	case 7:
		return "Paused"
	default:
		return "Unknown"
	}
}

func serviceStartTypeLabel(v int) string {
	switch v {
	case 0:
		return "Boot"
	case 1:
		return "System"
	case 2:
		return "Automatic"
	case 3:
		return "Manual"
	case 4:
		return "Disabled"
	default:
		return "Unknown"
	}
}

func isServiceNotInstalled(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "1060") || strings.Contains(s, "does not exist") || strings.Contains(s, "not exist")
}

func (m *tunnelManager) startWindowsService(current int) error {
	if current == 4 {
		return nil
	}
	out, err := exec.Command("sc.exe", "start", tunnelServiceName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "1056") && !strings.Contains(strings.ToLower(string(out)), "already") {
		return fmt.Errorf("start cloudflared service: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return waitServiceState(4, 30*time.Second)
}

func (m *tunnelManager) stopWindowsService(current int) error {
	if current == 1 {
		return nil
	}
	out, err := exec.Command("sc.exe", "stop", tunnelServiceName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "1062") && !strings.Contains(strings.ToLower(string(out)), "not active") {
		return fmt.Errorf("stop cloudflared service: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return waitServiceState(1, 60*time.Second)
}

func (m *tunnelManager) restartWindowsService(current int) error {
	if current != 1 {
		if err := m.stopWindowsService(current); err != nil {
			return err
		}
	}
	return m.startWindowsService(1)
}

func waitServiceState(want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last int
	for {
		svc, err := queryWindowsService(tunnelServiceName)
		if err == nil {
			last = svc.State
			if last == want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("cloudflared service is %s, expected %s", serviceStateLabel(last), serviceStateLabel(want))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Server) handleTunnelStatus(w http.ResponseWriter, _ *http.Request) {
	s.writeTunnelStatus(w, s.tunnel.Status())
}

func (s *Server) handleTunnelAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		st, err := s.tunnel.Action(action)
		if err != nil {
			apiErr(w, 409, err)
			return
		}
		s.writeTunnelStatus(w, st)
	}
}

func (s *Server) writeTunnelStatus(w http.ResponseWriter, st tunnelStatus) {
	if publicURL, _ := s.Edge(); publicURL != "" {
		st.PublicURL = publicURL
	}
	writeJSON(w, 200, st)
}
