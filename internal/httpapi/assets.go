package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
)

// serveScript publishes an onboarding script from the data dir. The scripts
// contain no secrets: the agent token is passed as an argument by the operator.
func (s *Server) serveScript(w http.ResponseWriter, name string) {
	p := filepath.Join(s.cfg.BinDir(), name)
	b, err := os.ReadFile(p)
	if err != nil {
		http.Error(w, name+" is not published; run scripts/build.ps1", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}

// handleBootstrap serves the Termux onboarding script.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	s.serveScript(w, "bootstrap.sh")
}

// handleBootstrapRoot serves the rooted-Android (no Termux) onboarding script.
func (s *Server) handleBootstrapRoot(w http.ResponseWriter, r *http.Request) {
	s.serveScript(w, "bootstrap-root.sh")
}

// handleBootstrapDebian serves the Debian-chroot (no Termux, full userland)
// onboarding script. It runs on the Android host as root, not inside the chroot.
func (s *Server) handleBootstrapDebian(w http.ResponseWriter, r *http.Request) {
	s.serveScript(w, "bootstrap-debian.sh")
}
