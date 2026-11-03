//go:build linux

package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"phleet/internal/agentproto"
)

const deployTimeout = 15 * time.Minute

// handleDeploy executes a self-contained plan. It deliberately does not depend
// on the control plane connection: if the panel disappears, the phone finishes
// the release (or rolls back) on its own.
func (a *Agent) handleDeploy(ctx context.Context, req agentproto.DeployRequest, emit func(agentproto.DeployEvent)) {
	a.deployMu.Lock()
	defer a.deployMu.Unlock()

	slug := req.Slug
	if !slugRe.MatchString(slug) {
		emit(agentproto.DeployEvent{DeploymentID: req.DeploymentID, Phase: "status", Status: "failed", Error: "invalid slug"})
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		emit(agentproto.DeployEvent{DeploymentID: req.DeploymentID, Phase: "status", Status: "failed", Error: "invalid port"})
		return
	}
	p := a.ensureApp(slug, req.Name, req.Port)
	p.mu.Lock()
	p.deploying = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.deploying = false
		p.mu.Unlock()
	}()

	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		emit(agentproto.DeployEvent{DeploymentID: req.DeploymentID, Phase: "log", Line: line})
	}
	status := func(s, errMsg string) {
		emit(agentproto.DeployEvent{DeploymentID: req.DeploymentID, Phase: "status", Status: s, Error: errMsg})
	}
	fail := func(msg string) {
		logf("error: %s", msg)
		status("failed", msg)
		a.setDeployState(slug, req.DeploymentID, "failed")
	}

	if req.SourcePath == "" && strings.TrimSpace(req.Repo) == "" {
		fail("repository URL or source bundle is required")
		return
	}
	if strings.TrimSpace(req.StartCmd) == "" {
		fail("start command is required")
		return
	}

	status("building", "")
	a.setDeployState(slug, req.DeploymentID, "building")
	base := a.appBase(slug)
	releasesDir := filepath.Join(base, "releases")
	if err := os.MkdirAll(releasesDir, 0o755); err != nil {
		fail("cannot create app directory: " + err.Error())
		return
	}
	_ = os.MkdirAll(filepath.Join(base, "shared"), 0o755)
	_ = os.MkdirAll(filepath.Join(base, "logs"), 0o755)

	releaseID := time.Now().Format("20060102-150405")
	releaseDir := filepath.Join(releasesDir, releaseID)
	ctx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()
	logf("release %s", releaseID)

	if req.SourcePath != "" {
		logf("deploying %s from an uploaded source bundle", slug)
		zipPath := releaseDir + ".zip"
		logf("downloading source bundle…")
		if err := a.downloadSource(ctx, req.SourcePath, zipPath); err != nil {
			_ = os.Remove(zipPath)
			fail("source download failed: " + err.Error())
			return
		}
		logf("extracting source…")
		res, err := a.extractZip(agentproto.FSExtractRequest{Path: zipPath, Target: releaseDir, MaxBytes: 512 << 20})
		_ = os.Remove(zipPath)
		if err != nil {
			_ = os.RemoveAll(releaseDir)
			fail("source extraction failed: " + err.Error())
			return
		}
		logf("extracted %d files (%s)", res.Files, formatBytes(res.Bytes))
	} else {
		logf("deploying %s from %s (branch %s)", slug, req.Repo, branchOrDefault(req.Branch))
		logf("cloning repository…")
		cloneCtx, cloneCancel := context.WithTimeout(ctx, 10*time.Minute)
		cloneArgs := []string{"clone", "--depth", "1"}
		if req.Branch != "" {
			cloneArgs = append(cloneArgs, "--branch", req.Branch)
		}
		cloneArgs = append(cloneArgs, req.Repo, releaseDir)
		if a.gitPath == "" {
			fail("git is not installed on this phone")
			return
		}
		err := a.runCommand(cloneCtx, base, a.gitPath, cloneArgs, appEnvForDeploy(req), func(line string) {
			logf("git: %s", line)
		})
		cloneCancel()
		if err != nil {
			_ = os.RemoveAll(releaseDir)
			fail("git clone failed: " + err.Error())
			return
		}
	}

	// Persist the environment for local debugging; the supervisor also injects
	// it directly, so the file is informational.
	writeEnvFile(filepath.Join(base, "shared", ".env"), req.Env)

	if strings.TrimSpace(req.BuildCmd) != "" {
		logf("running build: %s", req.BuildCmd)
		buildCtx, buildCancel := context.WithTimeout(ctx, 10*time.Minute)
		err := a.runCommand(buildCtx, releaseDir, a.shPath, []string{"-c", req.BuildCmd}, appEnvForDeploy(req), func(line string) {
			logf("build: %s", line)
		})
		buildCancel()
		if err != nil {
			fail("build failed: " + err.Error())
			return
		}
	}

	status("starting", "")
	logf("starting process on port %d", req.Port)

	// Capture the previous spec before startProcess replaces it.
	p.mu.Lock()
	prev := p.spec
	p.mu.Unlock()
	if prev.ReleaseDir == releaseDir {
		prev = AppSpec{}
	}

	spec := AppSpec{
		Name:         req.Name,
		Slug:         slug,
		Port:         req.Port,
		StartCmd:     req.StartCmd,
		HealthPath:   req.HealthPath,
		ReleaseDir:   releaseDir,
		PrevRelease:  prev.ReleaseDir,
		Env:          req.Env,
		DeploymentID: req.DeploymentID,
		DeployStatus: "starting",
		UpdatedAt:    time.Now().Unix(),
	}
	a.stopProcess(p)
	if err := a.startProcess(p, spec); err != nil {
		a.failDeploy(p, prev, req, "could not start process: "+err.Error(), logf, status)
		return
	}

	status("health_check", "")
	healthPath := req.HealthPath
	alive := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cmd != nil
	}
	if err := waitHealthy(ctx, req.Port, healthPath, healthTimeout, alive); err != nil {
		logf("health check failed: %s", err)
		a.stopProcess(p)
		a.failDeploy(p, prev, req, "health check failed: "+err.Error(), logf, status)
		return
	}
	logf("health check passed")

	a.swapCurrent(slug, releaseDir)
	a.pruneReleases(slug, req.KeepReleases)

	p.mu.Lock()
	if p.gen > 0 {
		p.spec.DeployStatus = "active"
		p.spec.DeploymentID = req.DeploymentID
		p.spec.UpdatedAt = time.Now().Unix()
	}
	p.mu.Unlock()
	a.saveApps()
	status("active", "")
	logf("deployed %s (%s)", slug, releaseID)
}

// restorePrevious brings the last good release back after a failed deploy.
func (a *Agent) restorePrevious(p *appProcess, prev AppSpec, deploymentID string, logf func(string, ...any)) bool {
	if prev.ReleaseDir == "" || prev.StartCmd == "" {
		return false
	}
	if _, err := os.Stat(prev.ReleaseDir); err != nil {
		logf("previous release is unavailable: %s", err)
		return false
	}
	logf("rolling back to %s", filepath.Base(prev.ReleaseDir))
	prev.DeploymentID = deploymentID
	prev.DeployStatus = "rolled_back"
	prev.PrevRelease = ""
	if err := a.startProcess(p, prev); err != nil {
		logf("rollback failed: %s", err)
		return false
	}
	if err := waitHealthy(context.Background(), prev.Port, prev.HealthPath, 30*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.cmd != nil
	}); err != nil {
		logf("rollback health check failed: %s", err)
		return false
	}
	a.swapCurrent(prev.Slug, prev.ReleaseDir)
	logf("rollback complete; %s is serving %s again", prev.Slug, filepath.Base(prev.ReleaseDir))
	return true
}

func (a *Agent) failDeploy(p *appProcess, prev AppSpec, req agentproto.DeployRequest, msg string, logf func(string, ...any), status func(string, string)) {
	logf("error: %s", msg)
	if a.restorePrevious(p, prev, req.DeploymentID, logf) {
		a.setDeployState(req.Slug, req.DeploymentID, "rolled_back")
		status("rolled_back", msg)
		return
	}
	a.setDeployState(req.Slug, req.DeploymentID, "failed")
	status("failed", msg)
}

func branchOrDefault(b string) string {
	if b == "" {
		return "default"
	}
	return b
}

func appEnvForDeploy(req agentproto.DeployRequest) []string {
	spec := AppSpec{Port: req.Port, Slug: req.Slug, Env: req.Env}
	return appEnv(spec)
}

func writeEnvFile(path string, env map[string]string) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(env[k])
		b.WriteString("\n")
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o600)
}

func (a *Agent) runCommand(ctx context.Context, dir, name string, args []string, env []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 64<<10), 512<<10)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 2000 {
				line = line[:2000] + "…"
			}
			onLine(line)
		}
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out")
		}
		return err
	}
	return nil
}

// downloadSource pulls the uploaded project bundle from the control plane
// using the same outbound connection and TLS/DoH settings as the agent tunnel.
func (a *Agent) downloadSource(ctx context.Context, apiPath, dest string) error {
	u, err := url.Parse(a.cfg.URL)
	if err != nil {
		return err
	}
	scheme := "https"
	switch u.Scheme {
	case "ws", "http":
		scheme = "http"
	case "wss", "https":
		scheme = "https"
	}
	if u.Host == "" {
		return fmt.Errorf("invalid control plane URL")
	}
	src := scheme + "://" + u.Host + apiPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
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
	const limit = 128 << 20
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
		return fmt.Errorf("source bundle exceeds %d MB", limit>>20)
	}
	if n == 0 {
		_ = os.Remove(dest)
		return fmt.Errorf("source bundle is empty")
	}
	return nil
}
