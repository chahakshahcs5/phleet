//go:build linux

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"phleet/internal/agentproto"
	"phleet/internal/engines"
)

const addonProvisionTimeout = 20 * time.Minute

func (a *Agent) addonEmitter() func(agentproto.AddonEvent) {
	return func(ev agentproto.AddonEvent) {
		_ = a.send(agentproto.TypeAddonEvent, "", ev)
	}
}

// capsSnapshot returns the current capability map.
func (a *Agent) capsSnapshot() map[string]any {
	a.capsMu.Lock()
	defer a.capsMu.Unlock()
	return a.caps
}

// setCaps replaces the capability map and bumps the generation so the next
// heartbeat tells the control plane.
func (a *Agent) setCaps(caps map[string]any) {
	if caps == nil {
		return
	}
	a.capsMu.Lock()
	if reflect.DeepEqual(a.caps, caps) {
		a.capsMu.Unlock()
		return
	}
	a.caps = caps
	a.capsGen++
	a.capsMu.Unlock()
}

func (a *Agent) markCapsSent() {
	a.capsMu.Lock()
	a.capsSent = a.capsGen
	a.capsMu.Unlock()
}

// capsIfChanged returns the map only when it changed since the last send.
func (a *Agent) capsIfChanged() map[string]any {
	a.capsMu.Lock()
	defer a.capsMu.Unlock()
	if a.capsGen == a.capsSent {
		return nil
	}
	a.capsSent = a.capsGen
	return a.caps
}

// capsLoop re-probes capabilities periodically. Engines can be installed by the
// operator (apt inside a chroot) while the agent runs; without this the panel
// would keep saying "not installed" until the agent restarted.
func (a *Agent) capsLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.setCaps(probeCaps())
		}
	}
}

func (a *Agent) markCPMessage() {
	a.lastCPMu.Lock()
	a.lastCP = time.Now()
	a.lastCPMu.Unlock()
}

// leaseLoop self-fences auto-failover primaries when the control plane has been
// unreachable for longer than the lease. This is the trade-off that makes
// automatic promotion safe: a panel outage becomes a database outage for
// addons that opt in, and only then may a standby be promoted.
func (a *Agent) leaseLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.lastCPMu.Lock()
			last := a.lastCP
			a.lastCPMu.Unlock()
			if last.IsZero() {
				continue
			}
			a.appsMu.Lock()
			list := make([]*appProcess, 0, len(a.apps))
			for _, p := range a.apps {
				list = append(list, p)
			}
			a.appsMu.Unlock()
			for _, p := range list {
				p.mu.Lock()
				lease := p.spec.LeaseSeconds
				slug := p.spec.Slug
				running := p.cmd != nil && p.status == "running" && !p.deploying
				p.mu.Unlock()
				if lease <= 0 || !running || time.Since(last) <= time.Duration(lease)*time.Second {
					continue
				}
				a.log.Warn("addon lease expired; self-fencing", "slug", slug, "leaseSec", lease)
				a.stopProcess(p)
				p.mu.Lock()
				if p.status == "stopped" {
					p.lastError = "control-plane lease expired; self-fenced"
				}
				p.mu.Unlock()
				a.saveApps()
			}
		}
	}
}

func (a *Agent) handleAddonLease(req agentproto.AddonLeaseRequest) error {
	if !slugRe.MatchString(req.Slug) {
		return errors.New("invalid slug")
	}
	a.appsMu.Lock()
	p := a.apps[req.Slug]
	a.appsMu.Unlock()
	if p == nil {
		return fmt.Errorf("addon %q is not installed", req.Slug)
	}
	p.mu.Lock()
	p.spec.LeaseSeconds = req.LeaseSeconds
	p.mu.Unlock()
	a.saveApps()
	return nil
}

func (a *Agent) addonRoot() string {
	return filepath.Join(a.home, "phleet", "data")
}

func (a *Agent) serviceRoot() string {
	return filepath.Join(a.home, "phleet", "services")
}

// addonDir validates that a control-plane-supplied path stays inside the
// workload directories. A compromised control plane must not be able to point
// an engine at an arbitrary directory.
func (a *Agent) addonDir(dir string) (string, error) {
	clean := filepath.Clean(dir)
	for _, root := range []string{a.addonRoot(), a.serviceRoot()} {
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("workload data directory %q is outside %s or %s", dir, a.addonRoot(), a.serviceRoot())
}

func engineInstalled(e *engines.Engine) bool {
	if e == nil || len(e.Binaries) == 0 {
		return false
	}
	for _, bin := range e.Binaries {
		if _, ok := lookTool(bin); !ok {
			return false
		}
	}
	return true
}

func paramsForSpec(spec agentproto.AddonSpec) engines.Params {
	return engines.Params{
		DataDir: spec.DataDir, File: spec.File, Port: spec.Port,
		User: spec.User, Password: spec.Password, Database: spec.Database,
		Superuser: spec.Superuser, SuperPassword: spec.SuperPassword,
		ReplicaUser: spec.ReplicaUser, ReplicaPass: spec.ReplicaPass,
	}
}

// handleProvisionAddon installs the engine if needed, initialises the data
// directory exactly once, starts the supervised process and waits for health.
// It is deliberately idempotent: a re-run after a reboot or a lost control
// plane connection never re-initialises an existing cluster.
func (a *Agent) handleProvisionAddon(spec agentproto.AddonSpec, emit func(agentproto.AddonEvent)) {
	eng, ok := engines.Get(spec.Engine)
	if !ok {
		emit(agentproto.AddonEvent{AddonID: spec.AddonID, Phase: "status", Status: "failed", Error: "unknown engine " + spec.Engine})
		return
	}
	logf := func(format string, args ...any) {
		emit(agentproto.AddonEvent{AddonID: spec.AddonID, Phase: "log", Line: fmt.Sprintf(format, args...)})
	}
	status := func(s, errMsg string) {
		emit(agentproto.AddonEvent{AddonID: spec.AddonID, Phase: "status", Status: s, Error: errMsg})
	}
	fail := func(msg string) {
		logf("error: %s", msg)
		status("failed", msg)
	}

	if !slugRe.MatchString(spec.Slug) {
		fail("invalid slug")
		return
	}
	if spec.Role == "standby" && eng.StandbyInit == nil {
		fail(fmt.Sprintf("%s does not support a standby", eng.Name))
		return
	}
	if spec.Role == "standby" && (spec.PrimaryHost == "" || spec.PrimaryPort <= 0) {
		fail("standby has no primary endpoint")
		return
	}
	dir, err := a.addonDir(spec.DataDir)
	if err != nil {
		fail(err.Error())
		return
	}

	a.addonMu.Lock()
	defer a.addonMu.Unlock()

	p := a.ensureApp(spec.Slug, spec.Name, spec.Port)
	p.mu.Lock()
	p.deploying = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.deploying = false
		p.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(a.ctx, addonProvisionTimeout)
	defer cancel()

	status("provisioning", "")

	if !engineInstalled(eng) {
		if eng.Install == nil && a.pkgManager == "" {
			fail(fmt.Sprintf("%s is not installed and no package manager is available on this device", eng.Name))
			return
		}
		elevate := ""
		staticFallback := false
		if a.pkgManager == "apt" && os.Geteuid() != 0 {
			if canSudo() {
				elevate = "sudo -n "
			} else if eng.Install != nil {
				// Android mounts /data nosuid, so sudo cannot work inside a
				// Debian chroot. Engines with a static installer do not need
				// root; use that instead of failing the install.
				staticFallback = true
			} else {
				fail(fmt.Sprintf("%s needs apt, and this phone does not allow passwordless sudo for package management", eng.Name))
				return
			}
		}
		mode := a.pkgManager
		if staticFallback {
			mode = "a static binary"
		}
		logf("installing %s with %s (this can take a few minutes)", eng.Name, mode)
		var cmdStr string
		if staticFallback {
			cmdStr = eng.Install(paramsForSpec(spec))
		} else {
			cmdStr = engines.InstallScriptAs(eng, paramsForSpec(spec), a.pkgManager, elevate)
		}
		if err := a.runCommand(ctx, a.home, a.shPath, []string{"-c", cmdStr}, os.Environ(), func(line string) {
			logf("install: %s", line)
		}); err != nil {
			fail("install failed: " + err.Error())
			return
		}
		// The engine (and possibly a package manager) just became available:
		// publish the new capabilities on the next heartbeat.
		a.setCaps(probeCaps())
	}

	if spec.Role == "standby" && spec.RelayPort > 0 {
		if err := a.startRelay(agentproto.AddonRelaySpec{
			AddonID: spec.AddonID, Action: "start", LocalPort: spec.RelayPort,
			TargetPort: spec.PrimaryPort,
		}); err != nil {
			fail("cannot start the standby relay: " + err.Error())
			return
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail("cannot create data directory: " + err.Error())
		return
	}
	sentinel := filepath.Join(dir, ".phleet-addon")
	if _, err := os.Stat(sentinel); err != nil {
		entries, _ := os.ReadDir(dir)
		if len(entries) > 0 {
			fail("data directory exists without a phleet marker; refusing to initialise over existing data")
			return
		}
		if spec.Role == "standby" {
			logf("cloning %s from the primary into %s", eng.Name, dir)
			script := eng.StandbyInit(paramsForSpec(spec), engines.Primary{
				Host: spec.PrimaryHost, Port: spec.PrimaryPort,
				User: spec.ReplicaUser, Password: spec.ReplicaPass,
			})
			if err := a.runCommand(ctx, dir, a.shPath, []string{"-c", script}, os.Environ(), func(line string) {
				logf("standby: %s", line)
			}); err != nil {
				_ = os.RemoveAll(dir)
				fail("standby initialisation failed: " + err.Error())
				return
			}
		} else {
			logf("initialising %s in %s", eng.Name, dir)
			if script := eng.Init(paramsForSpec(spec)); script != "" {
				if err := a.runCommand(ctx, dir, a.shPath, []string{"-c", script}, os.Environ(), func(line string) {
					logf("init: %s", line)
				}); err != nil {
					_ = os.RemoveAll(dir)
					fail("initialisation failed: " + err.Error())
					return
				}
			}
		}
		if err := os.WriteFile(sentinel, []byte(spec.AddonID+"\n"), 0o600); err != nil {
			fail("cannot write data marker: " + err.Error())
			return
		}
	} else {
		logf("reusing existing data directory %s", dir)
	}

	params := paramsForSpec(spec)
	lagCmd := ""
	if spec.Role != "standby" {
		lagCmd = engines.LagCommand(eng.ID, params)
	}
	appSpec := AppSpec{
		Name:         spec.Name,
		Slug:         spec.Slug,
		Kind:         enginesKind(eng),
		Addon:        true,
		Embedded:     eng.Embedded,
		Role:         spec.Role,
		Epoch:        spec.Epoch,
		Port:         spec.Port,
		StartCmd:     eng.Run(params),
		HealthCmd:    eng.Health(params),
		LagCmd:       lagCmd,
		LeaseSeconds: spec.LeaseSeconds,
		ReleaseDir:   dir,
		DataDir:      dir,
		StopSignal:   eng.StopSignal,
		StopTimeout:  eng.StopTimeout,
		Env:          spec.Env,
		DeployStatus: "starting",
		UpdatedAt:    time.Now().Unix(),
	}
	if err := a.startProcess(p, appSpec); err != nil {
		fail("could not start: " + err.Error())
		return
	}

	logf("waiting for %s to become healthy", eng.Name)
	if err := a.waitAddonHealthy(ctx, spec.Port, appSpec.HealthCmd, 90*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cmd != nil || eng.Embedded
	}); err != nil {
		a.stopProcess(p)
		fail("health check failed: " + err.Error())
		return
	}
	logf("health check passed")

	if spec.Role != "standby" {
		if script := eng.PostStart(params); script != "" {
			if err := a.runCommand(ctx, dir, a.shPath, []string{"-c", script}, os.Environ(), func(line string) {
				logf("setup: %s", line)
			}); err != nil {
				a.stopProcess(p)
				fail("post-start setup failed: " + err.Error())
				return
			}
		}
	}

	p.mu.Lock()
	p.spec.DeployStatus = "active"
	p.spec.UpdatedAt = time.Now().Unix()
	p.mu.Unlock()
	a.saveApps()
	status("running", "")
	logf("%s is ready on port %d", eng.Name, spec.Port)
}

func enginesKind(e *engines.Engine) string {
	if e.Embedded {
		return "file"
	}
	return e.Kind
}

func (a *Agent) addonControl(req agentproto.AddonControlRequest) error {
	if !slugRe.MatchString(req.Slug) {
		return errors.New("invalid slug")
	}
	a.appsMu.Lock()
	p := a.apps[req.Slug]
	a.appsMu.Unlock()
	if p == nil {
		return fmt.Errorf("addon %q is not installed", req.Slug)
	}
	p.mu.Lock()
	spec := p.spec
	p.mu.Unlock()

	switch req.Action {
	case "start", "restart":
		if err := a.startProcess(p, spec); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return a.waitAddonHealthy(ctx, spec.Port, spec.HealthCmd, 90*time.Second, func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.cmd != nil || spec.Kind == "file"
		})
	case "stop":
		a.stopProcess(p)
		a.saveApps()
		return nil
	case "delete":
		a.stopProcess(p)
		a.appsMu.Lock()
		delete(a.apps, req.Slug)
		a.appsMu.Unlock()
		a.saveApps()
		if req.PurgeData && spec.DataDir != "" {
			dir, err := a.addonDir(spec.DataDir)
			if err == nil {
				_ = os.RemoveAll(dir)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", req.Action)
	}
}

// waitAddonHealthy waits for the port (if any) and then for the engine's own
// health command. A TCP dial alone is not enough: an initialising Postgres
// accepts connections before it can answer queries.
func (a *Agent) waitAddonHealthy(ctx context.Context, port int, healthCmd string, timeout time.Duration, alive func() bool) error {
	deadline := time.Now().Add(timeout)
	var last error
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		if alive != nil && !alive() {
			return errors.New("process exited before it became healthy")
		}
		if port > 0 {
			conn, err := net.DialTimeout("tcp", address, 1500*time.Millisecond)
			if err != nil {
				last = err
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
					continue
				}
			}
			conn.Close()
		}
		if strings.TrimSpace(healthCmd) == "" {
			return nil
		}
		hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := exec.CommandContext(hctx, a.shPath, "-c", healthCmd).CombinedOutput()
		hcancel()
		if err == nil {
			return nil
		}
		last = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return last
}

func backupExt(e *engines.Engine) string {
	return engines.BackupExt(e.ID)
}

// waitCommand runs a probe until it exits 0. Used after promotion: pg_isready
// succeeds during recovery, but a promoted primary must accept writes.
func (a *Agent) waitCommand(ctx context.Context, command string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := exec.CommandContext(cctx, a.shPath, "-c", command).CombinedOutput()
		cancel()
		if err == nil {
			return nil
		}
		last = strings.TrimSpace(string(out))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if last == "" {
		last = "timed out"
	}
	return errors.New(last)
}

func (a *Agent) handleAddonBackup(req agentproto.AddonBackupRequest, emit func(agentproto.AddonEvent)) {
	eng, ok := engines.Get(req.Engine)
	if !ok {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_failed", Error: "unknown engine"})
		return
	}
	logf := func(format string, args ...any) {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "log", Line: fmt.Sprintf(format, args...)})
	}
	dir, err := a.addonDir(req.DataDir)
	if err != nil {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_failed", Error: err.Error()})
		return
	}
	if eng.Backup == nil {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_failed", Error: eng.Name + " has no persistent data to back up"})
		return
	}
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_failed", Error: err.Error()})
		return
	}
	dest := filepath.Join(backupDir, req.BackupID+backupExt(eng))
	params := engines.Params{
		DataDir: dir, File: req.File, Port: req.Port,
		User: req.User, Password: req.Password, Database: req.Database,
	}
	a.addonMu.Lock()
	defer a.addonMu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Minute)
	defer cancel()

	logf("writing %s backup", eng.Name)
	if err := a.runCommand(ctx, dir, a.shPath, []string{"-c", eng.Backup(params, dest)}, os.Environ(), func(line string) {
		logf("%s: %s", eng.ID, line)
	}); err != nil {
		_ = os.Remove(dest)
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_failed", Error: "backup failed: " + err.Error()})
		return
	}
	logf("uploading backup")
	if err := a.uploadFile(ctx, req.UploadPath, dest); err != nil {
		_ = os.Remove(dest)
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_failed", Error: "upload failed: " + err.Error()})
		return
	}
	_ = os.Remove(dest)
	emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "backup_ok"})
	logf("backup complete")
}

// handleAddonVerify downloads a backup to the state directory, checks its
// checksum and runs an engine-native integrity command. It never touches the
// live addon data directory.
func (a *Agent) handleAddonVerify(req agentproto.AddonVerifyRequest) error {
	eng, ok := engines.Get(req.Engine)
	if !ok {
		return errors.New("unknown engine")
	}
	dest := filepath.Join(a.cfg.StateDir, "tmp", "verify-"+req.BackupID+backupExt(eng))
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Minute)
	defer cancel()
	a.addonMu.Lock()
	defer a.addonMu.Unlock()
	defer os.Remove(dest)
	if err := a.downloadFile(ctx, req.DownloadPath, dest); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if req.Checksum != "" {
		sum, err := fileSHA256(dest)
		if err != nil || !strings.EqualFold(sum, req.Checksum) {
			return errors.New("backup checksum mismatch")
		}
	}
	params := engines.Params{
		Port: req.Port, User: req.User, Password: req.Password, Database: req.Database,
	}
	cmd := exec.CommandContext(ctx, a.shPath, "-c", engines.VerifyCommand(eng.ID, params, dest))
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("verify failed: %w: %s", err, msg)
		}
		return fmt.Errorf("verify failed: %w", err)
	}
	return nil
}

func (a *Agent) handleAddonRestore(req agentproto.AddonRestoreRequest, emit func(agentproto.AddonEvent)) {
	eng, ok := engines.Get(req.Engine)
	if !ok {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "failed", Error: "unknown engine"})
		return
	}
	logf := func(format string, args ...any) {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "log", Line: fmt.Sprintf(format, args...)})
	}
	fail := func(msg string) {
		logf("error: %s", msg)
		emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "failed", Error: msg})
	}
	if eng.Restore == nil {
		fail(eng.Name + " does not support restore")
		return
	}
	dir, err := a.addonDir(req.DataDir)
	if err != nil {
		fail(err.Error())
		return
	}
	if _, err := os.Stat(filepath.Join(dir, ".phleet-addon")); err != nil {
		fail("data directory has no phleet marker; provision the addon before restoring")
		return
	}
	a.appsMu.Lock()
	p := a.apps[req.Slug]
	a.appsMu.Unlock()
	if p == nil {
		fail("addon is not installed on this phone")
		return
	}

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Minute)
	defer cancel()
	// Keep the restore file out of the data directory: rclone's restore
	// cleans the bucket tree and would delete the file it is reading.
	tmpDir := filepath.Join(a.cfg.StateDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		fail("cannot create restore temp directory: " + err.Error())
		return
	}
	dest := filepath.Join(tmpDir, "restore-"+req.BackupID+backupExt(eng))
	logf("downloading backup")
	if err := a.downloadFile(ctx, req.DownloadPath, dest); err != nil {
		_ = os.Remove(dest)
		fail("download failed: " + err.Error())
		return
	}
	defer os.Remove(dest)
	if req.Checksum != "" {
		sum, err := fileSHA256(dest)
		if err != nil || !strings.EqualFold(sum, req.Checksum) {
			fail("backup checksum mismatch; refusing to restore")
			return
		}
	}

	a.addonMu.Lock()
	defer a.addonMu.Unlock()

	p.mu.Lock()
	spec := p.spec
	running := p.cmd != nil
	p.mu.Unlock()
	if spec.ReleaseDir == "" {
		fail("addon is not provisioned on this phone")
		return
	}
	params := engines.Params{
		DataDir: dir, File: req.File, Port: req.Port,
		User: req.User, Password: req.Password, Database: req.Database,
	}
	// Keep a local safety dump before overwriting live data. It lives under
	// the addon's backups directory, so the file browser can retrieve it even
	// though it is not part of the control-plane backup list.
	if eng.Backup != nil {
		backupDir := filepath.Join(dir, "backups")
		if err := os.MkdirAll(backupDir, 0o700); err == nil {
			safety := filepath.Join(backupDir, "pre-restore-"+time.Now().Format("20060102-150405")+backupExt(eng))
			logf("pre-restore snapshot: %s", safety)
			scctx, scancel := context.WithTimeout(ctx, 15*time.Minute)
			err := a.runCommand(scctx, dir, a.shPath, []string{"-c", eng.Backup(params, safety)}, os.Environ(), func(line string) {
				logf("snapshot: %s", line)
			})
			scancel()
			if err != nil {
				logf("warning: pre-restore snapshot failed: %s", err)
				_ = os.Remove(safety)
			} else {
				prunePreRestore(backupDir, 2)
			}
		}
	}

	// Postgres restores through a live connection; Redis, SQLite and rclone
	// replace files and must be stopped first.
	stopFirst := eng.RestoreStop
	if stopFirst {
		a.stopProcess(p)
	} else if !running {
		if err := a.startProcess(p, spec); err != nil {
			fail("could not start the engine for a live restore: " + err.Error())
			return
		}
		if err := a.waitAddonHealthy(ctx, spec.Port, spec.HealthCmd, 90*time.Second, nil); err != nil {
			fail("engine did not become healthy for the restore: " + err.Error())
			return
		}
	}
	logf("restoring %s", eng.Name)
	if err := a.runCommand(ctx, dir, a.shPath, []string{"-c", eng.Restore(params, dest)}, os.Environ(), func(line string) {
		logf("%s: %s", eng.ID, line)
	}); err != nil {
		fail("restore failed: " + err.Error())
		return
	}
	if stopFirst {
		if err := a.startProcess(p, spec); err != nil {
			fail("restart after restore failed: " + err.Error())
			return
		}
	} else {
		// Keep the supervisor state accurate; the process was already running.
		a.saveApps()
	}
	if err := a.waitAddonHealthy(ctx, spec.Port, spec.HealthCmd, 90*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cmd != nil || spec.Kind == "file"
	}); err != nil {
		fail("health check after restore failed: " + err.Error())
		return
	}
	emit(agentproto.AddonEvent{AddonID: req.AddonID, BackupID: req.BackupID, Phase: "status", Status: "running"})
	logf("restore complete")
}

// handleAddonPromote turns a standby into the primary after the control plane
// has fenced the old one. The engine does the promotion; the spec is updated so
// lag sampling now runs in the other direction.
func (a *Agent) handleAddonPromote(req agentproto.AddonPromoteRequest, emit func(agentproto.AddonEvent)) error {
	eng, ok := engines.Get(req.Engine)
	if !ok {
		return errors.New("unknown engine")
	}
	if eng.Promote == nil {
		return fmt.Errorf("%s does not support promotion", eng.Name)
	}
	a.addonMu.Lock()
	defer a.addonMu.Unlock()

	a.appsMu.Lock()
	p := a.apps[req.Slug]
	a.appsMu.Unlock()
	if p == nil {
		return fmt.Errorf("addon %q is not installed on this phone", req.Slug)
	}
	p.mu.Lock()
	spec := p.spec
	p.mu.Unlock()
	if spec.ReleaseDir == "" {
		return errors.New("addon is not provisioned")
	}
	params := engines.Params{
		DataDir: spec.DataDir, File: req.File, Port: spec.Port,
		User: req.User, Password: req.Password, Database: req.Database,
		Superuser: req.Superuser, SuperPassword: req.SuperPassword,
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	emit(agentproto.AddonEvent{AddonID: req.AddonID, Phase: "log", Line: "promoting standby to primary"})
	if err := a.runCommand(ctx, spec.DataDir, a.shPath, []string{"-c", eng.Promote(params)}, os.Environ(), func(line string) {
		emit(agentproto.AddonEvent{AddonID: req.AddonID, Phase: "log", Line: strings.TrimSpace(line)})
	}); err != nil {
		return fmt.Errorf("promotion failed: %w", err)
	}
	// pg_isready is still true in recovery, so wait for the engine to accept
	// writes before reporting the promotion as complete.
	if eng.PromoteReady != nil {
		if err := a.waitCommand(ctx, eng.PromoteReady(params), 90*time.Second); err != nil {
			return fmt.Errorf("promotion did not become writable: %w", err)
		}
	}
	if err := a.waitAddonHealthy(ctx, spec.Port, spec.HealthCmd, 90*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cmd != nil || spec.Kind == "file"
	}); err != nil {
		return fmt.Errorf("health check after promotion failed: %w", err)
	}
	p.mu.Lock()
	p.spec.Role = "primary"
	p.spec.Epoch = req.Epoch
	p.spec.LeaseSeconds = req.LeaseSeconds
	p.spec.LagCmd = engines.LagCommand(eng.ID, params)
	p.mu.Unlock()
	a.saveApps()
	emit(agentproto.AddonEvent{AddonID: req.AddonID, Phase: "status", Status: "running"})
	emit(agentproto.AddonEvent{AddonID: req.AddonID, Phase: "log", Line: "promotion complete"})
	return nil
}

// lagLoop samples replication lag for primaries on a slow cadence. Doing it on
// the heartbeat path would run a client query every 5 seconds.
func (a *Agent) lagLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sampleLags(ctx)
		}
	}
}

func (a *Agent) sampleLags(ctx context.Context) {
	a.appsMu.Lock()
	list := make([]*appProcess, 0, len(a.apps))
	for _, p := range a.apps {
		list = append(list, p)
	}
	a.appsMu.Unlock()
	for _, p := range list {
		p.mu.Lock()
		cmd := p.spec.LagCmd
		alive := p.cmd != nil || p.spec.Kind == "file"
		p.mu.Unlock()
		if cmd == "" {
			continue
		}
		lag := int64(-1)
		if alive {
			lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			out, err := exec.CommandContext(lctx, a.shPath, "-c", cmd).Output()
			cancel()
			if err == nil {
				s := strings.TrimSpace(string(out))
				if i := strings.IndexAny(s, "\r\n"); i >= 0 {
					s = strings.TrimSpace(s[:i])
				}
				if v, perr := strconv.ParseInt(s, 10, 64); perr == nil {
					lag = v
				}
			}
		}
		p.mu.Lock()
		p.lagMS = lag
		p.mu.Unlock()
	}
}

// uploadFile PUTs a local file to the control plane over the same outbound
// TLS/DoH client the agent already uses.
func (a *Agent) uploadFile(ctx context.Context, apiPath, src string) error {
	base, err := a.controlPlaneURL(apiPath)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		return errors.New("backup is empty")
	}
	// 32 MB chunks stay comfortably under Cloudflare's 100 MB request cap and
	// let a large dump survive a single failed chunk.
	const chunk = 32 << 20
	for offset := int64(0); offset < st.Size(); offset += chunk {
		n := int64(chunk)
		if remaining := st.Size() - offset; remaining < n {
			n = remaining
		}
		last := offset+n >= st.Size()
		target := fmt.Sprintf("%s?offset=%d&last=%t", base, offset, last)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, io.NewSectionReader(f, offset, n))
		if err != nil {
			return err
		}
		req.ContentLength = n
		req.Header.Set("Authorization", "Bearer "+a.currentToken())
		req.Header.Set("X-Phleet-Server", a.cfg.ID)
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("chunk at %d: HTTP %d: %s", offset, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	return nil
}

func prunePreRestore(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "pre-restore-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (a *Agent) downloadFile(ctx context.Context, apiPath, dest string) error {
	target, err := a.controlPlaneURL(apiPath)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.currentToken())
	req.Header.Set("X-Phleet-Server", a.cfg.ID)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	const limit = 4 << 30
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	if n > limit {
		_ = os.Remove(dest)
		return errors.New("backup exceeds the 4 GB restore limit")
	}
	if n == 0 {
		_ = os.Remove(dest)
		return errors.New("backup is empty")
	}
	return nil
}

func (a *Agent) controlPlaneURL(apiPath string) (string, error) {
	u, err := url.Parse(a.cfg.URL)
	if err != nil {
		return "", err
	}
	scheme := "https"
	switch u.Scheme {
	case "ws", "http":
		scheme = "http"
	case "wss", "https":
		scheme = "https"
	}
	if u.Host == "" {
		return "", errors.New("invalid control plane URL")
	}
	return scheme + "://" + u.Host + apiPath, nil
}
