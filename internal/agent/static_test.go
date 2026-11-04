//go:build linux

package agent

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func testStaticServer(t *testing.T, files map[string]string, spa bool) *staticServer {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &staticServer{root: root, spa: spa}
}

func staticRequest(s *staticServer, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestStaticServerServesFiles(t *testing.T) {
	s := testStaticServer(t, map[string]string{
		"index.html":    "<html>home</html>",
		"assets/app.js": "console.log('hi')",
		".env":          "SECRET=1",
		".git/config":   "[remote]",
	}, true)

	if rec := staticRequest(s, http.MethodGet, "/"); rec.Code != 200 || rec.Body.String() != "<html>home</html>" {
		t.Fatalf("index: %d %q", rec.Code, rec.Body.String())
	}
	if ct := staticRequest(s, http.MethodGet, "/assets/app.js").Header().Get("Content-Type"); ct == "" {
		t.Fatal("javascript content type missing")
	}

	// Dotfiles and .git must never be published, even though they exist in
	// the release directory.
	if rec := staticRequest(s, http.MethodGet, "/.env"); rec.Code != http.StatusNotFound {
		t.Fatalf(".env status %d", rec.Code)
	}
	if rec := staticRequest(s, http.MethodGet, "/.git/config"); rec.Code != http.StatusNotFound {
		t.Fatalf(".git status %d", rec.Code)
	}
	if rec := staticRequest(s, http.MethodPost, "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", rec.Code)
	}
}

func TestStaticServerSPAFallback(t *testing.T) {
	s := testStaticServer(t, map[string]string{"index.html": "spa", "app.js": "js"}, true)
	if rec := staticRequest(s, http.MethodGet, "/dashboard"); rec.Code != 200 || rec.Body.String() != "spa" {
		t.Fatalf("SPA fallback: %d %q", rec.Code, rec.Body.String())
	}
	if rec := staticRequest(s, http.MethodGet, "/missing.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status %d", rec.Code)
	}

	noSPA := testStaticServer(t, map[string]string{"index.html": "spa"}, false)
	if rec := staticRequest(noSPA, http.MethodGet, "/dashboard"); rec.Code != http.StatusNotFound {
		t.Fatalf("non-SPA fallback status %d", rec.Code)
	}
}

func TestStaticServerRejectsSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testStaticServer(t, map[string]string{"index.html": "ok"}, false)
	if err := os.Symlink(secret, filepath.Join(s.root, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rec := staticRequest(s, http.MethodGet, "/leak.txt"); rec.Code != http.StatusNotFound {
		t.Fatalf("symlink escape status %d", rec.Code)
	}
}

func TestResolveStaticRoot(t *testing.T) {
	release := t.TempDir()
	dist := filepath.Join(release, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveStaticRoot(release, "dist")
	if err != nil || got != dist {
		t.Fatalf("resolve dist: %v %q", err, got)
	}
	if _, err := resolveStaticRoot(release, "../outside"); err == nil {
		t.Fatal("traversal must be rejected")
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(release, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolveStaticRoot(release, "escape"); err == nil {
		t.Fatal("a symlink that leaves the release must be rejected")
	}
}

func TestSpawnStaticProcess(t *testing.T) {
	home := t.TempDir()
	release := filepath.Join(home, "apps", "site", "releases", "1")
	if err := os.MkdirAll(release, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "index.html"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	a := &Agent{
		home:     home,
		appsPath: filepath.Join(home, "apps.json"),
		apps:     map[string]*appProcess{},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p := a.ensureApp("site", "Site", port)
	spec := AppSpec{
		Slug: "site", Name: "Site", Kind: "static", Port: port,
		ReleaseDir: release, HealthPath: "/",
	}
	if err := a.spawnProcess(p, spec); err != nil {
		t.Fatalf("spawn static: %v", err)
	}
	defer func() {
		a.stopProcess(p)
		if p.proxy != nil {
			p.proxy.close()
		}
	}()
	p.mu.Lock()
	running := p.status == "running" && p.static != nil
	p.mu.Unlock()
	if !running {
		t.Fatal("static process is not running")
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
	if err != nil {
		t.Fatalf("GET static site: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "live" {
		t.Fatalf("static response: %d %q", resp.StatusCode, body)
	}
}

func TestSpawnWorkerProcess(t *testing.T) {
	home := t.TempDir()
	release := filepath.Join(home, "apps", "worker", "releases", "1")
	if err := os.MkdirAll(release, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		home:     home,
		shPath:   "/bin/sh",
		appsPath: filepath.Join(home, "apps.json"),
		apps:     map[string]*appProcess{},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	p := a.ensureApp("worker", "Worker", 0)
	spec := AppSpec{
		Slug: "worker", Name: "Worker", Kind: "worker", Port: 0,
		StartCmd: "sleep 30", ReleaseDir: release,
	}
	if err := a.spawnProcess(p, spec); err != nil {
		t.Fatalf("spawn worker: %v", err)
	}
	defer a.stopProcess(p)
	p.mu.Lock()
	running := p.status == "running" && p.cmd != nil && p.proxy == nil
	p.mu.Unlock()
	if !running {
		t.Fatal("worker is not running without a proxy")
	}
}

func TestWithinDir(t *testing.T) {
	root := filepath.Join("/srv", "app")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "dist"), true},
		{filepath.Join(root, "..", "other"), false},
		{"/srv/application", false},
	} {
		if got := withinDir(root, tc.path); got != tc.want {
			t.Fatalf("withinDir(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
