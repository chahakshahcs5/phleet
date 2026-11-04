//go:build linux

package agent

import (
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// staticServer serves a static site from a release directory. It lives inside
// the agent so a static project needs no runtime on the phone: the agent owns
// the loopback port and the switchboard behind it, exactly like a supervised
// process. Requests are confined to the release root, and dotfiles (including
// .git and .env) are never served.
type staticServer struct {
	root string
	port int
	ln   net.Listener
	srv  *http.Server
	spa  bool
	done chan struct{}
}

// resolveStaticRoot maps the configured publish directory to a real path that
// is guaranteed to stay inside the release. Both the joined path and its
// symlink target are checked, because a git repository can contain symlinks.
func resolveStaticRoot(releaseDir, staticDir string) (string, error) {
	root := releaseDir
	if strings.TrimSpace(staticDir) != "" {
		root = filepath.Join(releaseDir, filepath.FromSlash(staticDir))
	}
	if !withinDir(releaseDir, root) {
		return "", fmt.Errorf("static directory escapes the release directory")
	}
	releaseResolved, err := filepath.EvalSymlinks(releaseDir)
	if err != nil {
		return "", fmt.Errorf("release directory: %w", err)
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("static directory: %w", err)
	}
	if !withinDir(releaseResolved, rootResolved) {
		return "", fmt.Errorf("static directory resolves outside the release directory")
	}
	return rootResolved, nil
}

func startStaticServer(root string, port int) (*staticServer, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid static port %d", port)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("static root: %w", err)
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("static root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("static root %s is not a directory", resolved)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, fmt.Errorf("listen on static port %d: %w", port, err)
	}
	s := &staticServer{root: resolved, port: port, ln: ln, done: make(chan struct{})}
	if st, err := os.Stat(filepath.Join(resolved, "index.html")); err == nil && !st.IsDir() {
		s.spa = true
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = s.srv.Serve(ln)
		close(s.done)
	}()
	return s, nil
}

func (s *staticServer) close() {
	if s == nil {
		return
	}
	_ = s.srv.Close()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
}

func (s *staticServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")

	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if rel == "" {
		rel = "."
	}
	if !safeStaticPath(rel) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if !withinDir(s.root, full) {
		http.NotFound(w, r)
		return
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err == nil && !withinDir(s.root, resolved) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.notFoundOrIndex(w, r, rel)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil {
		s.notFoundOrIndex(w, r, rel)
		return
	}
	if info.IsDir() {
		index := filepath.Join(resolved, "index.html")
		if st, err := os.Stat(index); err == nil && !st.IsDir() {
			s.serveFile(w, r, index, st)
			return
		}
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, resolved, info)
}

// notFoundOrIndex implements SPA fallback: an extensionless route that does not
// exist is served index.html, so client-side routers keep working. Missing
// assets (paths with an extension) still return 404.
func (s *staticServer) notFoundOrIndex(w http.ResponseWriter, r *http.Request, rel string) {
	if !s.spa || path.Ext(rel) != "" {
		http.NotFound(w, r)
		return
	}
	index := filepath.Join(s.root, "index.html")
	st, err := os.Stat(index)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, index, st)
}

func (s *staticServer) serveFile(w http.ResponseWriter, r *http.Request, name string, info os.FileInfo) {
	f, err := os.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if filepath.Base(name) == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// safeStaticPath rejects path segments that must never be published: dotfiles
// (.git, .env, .ssh) are blocked, with the standard .well-known exception.
func safeStaticPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if !strings.HasPrefix(part, ".") || part == "." || part == ".." {
			continue
		}
		if part != ".well-known" {
			return false
		}
	}
	return true
}

func withinDir(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}
