//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"phleet/internal/config"
)

type updateManifest struct {
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
	URL       string `json:"url"`
}

func (a *Agent) updateKeyPath() string {
	return filepath.Join(a.cfg.StateDir, "update.pub")
}

func (a *Agent) updateLoop(ctx context.Context) {
	// Give the first heartbeat and enrollment a moment, then check at most a
	// few times a day. A successful update replaces the process immediately.
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	if err := a.checkForUpdate(ctx); err != nil {
		a.log.Debug("agent update check", "err", err)
	}
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := a.checkForUpdate(ctx); err != nil {
				a.log.Debug("agent update check", "err", err)
			}
		}
	}
}

func (a *Agent) checkForUpdate(ctx context.Context) error {
	pubRaw, err := os.ReadFile(a.updateKeyPath())
	if err != nil {
		return nil // no pinned key: auto-update disabled
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pubRaw)))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid pinned update key")
	}
	arch := runtime.GOARCH
	target := a.absoluteURL("/api/agent/update?arch=" + url.QueryEscape(arch))
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
		return fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}
	var m updateManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m); err != nil {
		return err
	}
	if m.Version == "" || m.Version == config.Version {
		return nil
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("invalid update signature")
	}
	payload := m.Version + "\n" + m.SHA256 + "\n" + m.Arch
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(payload), sig) {
		return fmt.Errorf("update signature verification failed")
	}
	if m.Arch != arch {
		return fmt.Errorf("update arch %q does not match %q", m.Arch, arch)
	}
	return a.applyUpdate(ctx, m)
}

func (a *Agent) applyUpdate(ctx context.Context, m updateManifest) error {
	targetURL := a.absoluteURL(m.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
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
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 64<<20))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("download update: %v", copyErr)
	}
	if n == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("empty update")
	}
	if hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		_ = os.Remove(tmp)
		return fmt.Errorf("update checksum mismatch")
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	a.log.Info("agent updated; restarting", "version", m.Version)
	return syscall.Exec(exe, os.Args, os.Environ())
}

func (a *Agent) absoluteURL(path string) string {
	u, err := url.Parse(a.cfg.URL)
	if err != nil {
		return path
	}
	scheme := "https"
	if u.Scheme == "ws" || u.Scheme == "http" {
		scheme = "http"
	}
	return scheme + "://" + u.Host + path
}
