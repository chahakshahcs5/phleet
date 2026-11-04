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
	"regexp"
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
	if req.Kind != "worker" && (req.Port <= 0 || req.Port > 65535) {
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
	releaseID := time.Now().Format("20060102-150405")
	commitSHA := ""
	status := func(s, errMsg string) {
		emit(agentproto.DeployEvent{
			DeploymentID: req.DeploymentID, Phase: "status", Status: s, Error: errMsg,
			Release: releaseID, Commit: commitSHA,
		})
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
	if strings.TrimSpace(req.StartCmd) == "" && req.Kind != "static" {
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
	dataDir := filepath.Join(base, "shared", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fail("cannot create app data directory: " + err.Error())
		return
	}
	secretsDir := filepath.Join(base, "shared", "secrets")
	if err := writeSecrets(secretsDir, req.Secrets); err != nil {
		fail("cannot write secret files: " + err.Error())
		return
	}
	logf("persistent data: %s", dataDir)
	cacheDir := filepath.Join(base, "shared", "cache")
	for _, d := range []string{"npm", "pnpm", "yarn", "go", "pip", "cargo"} {
		_ = os.MkdirAll(filepath.Join(cacheDir, d), 0o755)
	}

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
		ref := strings.TrimSpace(req.Ref)
		branch := req.Branch
		if ref != "" && !isHexRef(ref) {
			// A branch or tag: clone it directly.
			branch = ref
		}
		logf("deploying %s from %s (ref %s)", slug, req.Repo, branchOrDefault(branch))
		logf("cloning repository…")
		cloneCtx, cloneCancel := context.WithTimeout(ctx, 10*time.Minute)
		cloneArgs := []string{"clone", "--depth", "1"}
		if branch != "" {
			cloneArgs = append(cloneArgs, "--branch", branch)
		}
		cloneArgs = append(cloneArgs, req.Repo, releaseDir)
		if a.gitPath == "" {
			cloneCancel()
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
		if ref != "" && isHexRef(ref) {
			// Webhooks send the pushed commit. A shallow clone of the branch
			// already contains it in the common case; otherwise fetch just that
			// commit, which GitHub and GitLab allow for reachable objects.
			head := ""
			if out, err := exec.CommandContext(ctx, a.gitPath, "-C", releaseDir, "rev-parse", "HEAD").Output(); err == nil {
				head = strings.TrimSpace(string(out))
			}
			if !strings.EqualFold(head, ref) && !strings.HasPrefix(strings.ToLower(head), strings.ToLower(ref)) {
				logf("fetching commit %s", ref)
				fetchCtx, fetchCancel := context.WithTimeout(ctx, 10*time.Minute)
				err := a.runCommand(fetchCtx, releaseDir, a.gitPath,
					[]string{"fetch", "--depth", "1", "origin", ref}, appEnvForDeploy(req), func(line string) {
						logf("git: %s", line)
					})
				fetchCancel()
				if err == nil {
					checkoutCtx, checkoutCancel := context.WithTimeout(ctx, 2*time.Minute)
					err = a.runCommand(checkoutCtx, releaseDir, a.gitPath,
						[]string{"checkout", "--detach", ref}, appEnvForDeploy(req), func(line string) {
							logf("git: %s", line)
						})
					checkoutCancel()
				}
				if err != nil {
					_ = os.RemoveAll(releaseDir)
					fail("git checkout " + ref + " failed: " + err.Error())
					return
				}
			}
		}
		if a.gitPath != "" {
			if out, err := exec.CommandContext(ctx, a.gitPath, "-C", releaseDir, "rev-parse", "HEAD").Output(); err == nil {
				commitSHA = strings.TrimSpace(string(out))
				if commitSHA != "" {
					logf("commit %s", commitSHA)
				}
			}
		}
	}

	// Persist the environment for local debugging; the supervisor also injects
	// it directly, so the file is informational.
	writeEnvFile(filepath.Join(base, "shared", ".env"), req.Env)

	if strings.TrimSpace(req.BuildCmd) != "" {
		logf("running build: %s", req.BuildCmd)
		buildCtx, buildCancel := context.WithTimeout(ctx, 10*time.Minute)
		buildEnv := append(appEnvForDeploy(req), cacheEnv(cacheDir)...)
		err := a.runCommand(buildCtx, releaseDir, a.shPath, []string{"-c", req.BuildCmd}, buildEnv, func(line string) {
			logf("build: %s", line)
		})
		buildCancel()
		if err != nil {
			fail("build failed: " + err.Error())
			return
		}
	}

	status("starting", "")
	logf("starting process on public port %d", req.Port)

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
		Kind:         req.Kind,
		Port:         req.Port,
		StartCmd:     req.StartCmd,
		HealthPath:   req.HealthPath,
		StaticDir:    req.StaticDir,
		ReleaseDir:   releaseDir,
		DataDir:      dataDir,
		PrevRelease:  prev.ReleaseDir,
		Env:          req.Env,
		DeploymentID: req.DeploymentID,
		DeployStatus: "starting",
		UpdatedAt:    time.Now().Unix(),
	}
	if !isAddonSpec(spec) && !isWorkerSpec(spec) {
		if prev.InternalPort > 0 && p.proxy != nil && p.proxy.currentTarget() > 0 {
			alternate := prev.InternalPort == internalPort(req.Port, false)
			spec.InternalPort = internalPort(req.Port, alternate)
		} else {
			spec.InternalPort = internalPort(req.Port, false)
		}
	}

	// Blue-green: keep the old release serving on the public port, start the
	// new release on an alternate internal port, health-check it, then switch.
	if !isAddonSpec(spec) && prev.InternalPort > 0 && p.proxy != nil && (p.cmd != nil || p.static != nil) {
		pNew := &appProcess{proxy: p.proxy}
		pNew.mu.Lock()
		pNew.deploying = true
		pNew.mu.Unlock()
		failNew := func(msg string) {
			a.stopProcess(pNew)
			pNew.mu.Lock()
			pNew.deploying = false
			pNew.mu.Unlock()
			a.setDeployState(slug, req.DeploymentID, "rolled_back")
			status("rolled_back", msg)
			logf("new release failed; the previous release is still serving: %s", msg)
		}
		if err := a.spawnProcess(pNew, spec); err != nil {
			failNew("could not start process: " + err.Error())
			return
		}
		status("health_check", "")
		if err := waitHealthy(ctx, appPort(spec), req.HealthPath, healthTimeout, func() bool {
			pNew.mu.Lock()
			defer pNew.mu.Unlock()
			return pNew.cmd != nil || pNew.static != nil
		}); err != nil {
			failNew("health check failed: " + err.Error())
			return
		}
		logf("health check passed on internal port %d; switching", spec.InternalPort)
		p.proxy.setTarget(spec.InternalPort)
		a.swapCurrent(slug, releaseDir)
		a.pruneReleases(slug, req.KeepReleases)
		pNew.mu.Lock()
		pNew.deploying = false
		pNew.spec.DeployStatus = "active"
		pNew.spec.DeploymentID = req.DeploymentID
		pNew.spec.UpdatedAt = time.Now().Unix()
		pNew.mu.Unlock()
		a.appsMu.Lock()
		a.apps[slug] = pNew
		a.appsMu.Unlock()
		a.saveApps()
		status("active", "")
		logf("deployed %s (%s) without dropping new requests", slug, releaseID)
		// Drain existing connections for a short window, then stop the old
		// release. New connections already go to the new backend.
		go func() {
			select {
			case <-time.After(30 * time.Second):
			case <-a.ctx.Done():
			}
			a.stopProcess(p)
		}()
		return
	}

	a.stopProcess(p)
	if err := a.startProcess(p, spec); err != nil {
		a.failDeploy(p, prev, req, "could not start process: "+err.Error(), logf, status)
		return
	}

	status("health_check", "")
	if isWorkerSpec(spec) {
		// A worker has no listener, so the only meaningful check is that it
		// survived startup. A bad command fails here and triggers a rollback.
		time.Sleep(1500 * time.Millisecond)
		p.mu.Lock()
		running := p.cmd != nil
		lastErr := p.lastError
		p.mu.Unlock()
		if !running {
			a.stopProcess(p)
			if lastErr == "" {
				lastErr = "worker exited immediately"
			}
			a.failDeploy(p, prev, req, "worker failed: "+lastErr, logf, status)
			return
		}
		logf("worker is running")
	} else {
		healthPath := req.HealthPath
		alive := func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.cmd != nil || p.static != nil
		}
		if err := waitHealthy(ctx, appPort(spec), healthPath, healthTimeout, alive); err != nil {
			logf("health check failed: %s", err)
			a.stopProcess(p)
			a.failDeploy(p, prev, req, "health check failed: "+err.Error(), logf, status)
			return
		}
		logf("health check passed")
	}

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
	if prev.ReleaseDir == "" || (prev.StartCmd == "" && prev.Kind != "static") {
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
	if !isWorkerSpec(prev) {
		if err := waitHealthy(context.Background(), appPort(prev), prev.HealthPath, 30*time.Second, func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.cmd != nil || p.static != nil
		}); err != nil {
			logf("rollback health check failed: %s", err)
			return false
		}
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

var secretFileRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// writeSecrets synchronises the app's secret-file directory with the deploy
// plan. Files are only accessible to the app user (0600) and a deleted secret is
// removed on the next deploy.
func writeSecrets(dir string, files map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := files[e.Name()]; !ok {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	for name, content := range files {
		if !secretFileRe.MatchString(name) {
			return fmt.Errorf("invalid secret name %q", name)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
		_ = os.Chmod(path, 0o600)
	}
	return nil
}

func isHexRef(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func appEnvForDeploy(req agentproto.DeployRequest) []string {
	spec := AppSpec{Port: req.Port, Slug: req.Slug, Env: req.Env}
	return appEnv(spec)
}

// cacheEnv points common package managers at a per-project cache in the shared
// directory. Builds are still isolated in release directories, but dependencies
// survive from one deploy to the next instead of being downloaded again onto
// eMMC.
func cacheEnv(dir string) []string {
	return []string{
		"XDG_CACHE_HOME=" + dir,
		"npm_config_cache=" + filepath.Join(dir, "npm"),
		"PNPM_STORE_DIR=" + filepath.Join(dir, "pnpm"),
		"YARN_CACHE_FOLDER=" + filepath.Join(dir, "yarn"),
		"GOCACHE=" + filepath.Join(dir, "go"),
		"PIP_CACHE_DIR=" + filepath.Join(dir, "pip"),
		"CARGO_HOME=" + filepath.Join(dir, "cargo"),
	}
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
	prepareCommandGroup(cmd)
	cmd.Dir = dir
	cmd.Env = sanitizeEnv(env)
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
