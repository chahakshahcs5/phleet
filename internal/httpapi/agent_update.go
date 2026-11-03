package httpapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"phleet/internal/config"
)

// handleAgentUpdateManifest signs the currently published agent binary. The
// agent pins the matching public key at bootstrap and verifies both the SHA-256
// and the Ed25519 signature before replacing itself.
func (s *Server) handleAgentUpdateManifest(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.UpdateKey) != ed25519.PrivateKeySize {
		apiErr(w, http.StatusServiceUnavailable, errors.New("agent update signing is not configured"))
		return
	}
	arch := strings.TrimSpace(r.URL.Query().Get("arch"))
	switch arch {
	case "arm64", "amd64", "arm":
	default:
		apiErr(w, http.StatusBadRequest, errors.New("unsupported arch"))
		return
	}
	name := "phleet-agent-linux-" + arch
	path := filepath.Join(s.cfg.BinDir(), name)
	b, err := os.ReadFile(path)
	if err != nil {
		apiErr(w, http.StatusNotFound, errors.New("agent binary is not published"))
		return
	}
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	payload := config.Version + "\n" + digest + "\n" + arch
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(s.cfg.UpdateKey, []byte(payload)))
	writeJSON(w, 200, map[string]any{
		"version":   config.Version,
		"arch":      arch,
		"sha256":    digest,
		"signature": sig,
		"url":       "/download/agent?arch=" + arch,
	})
}

func (s *Server) handleUpdatePublicKey(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.UpdatePublicKey() == "" {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(s.cfg.UpdatePublicKey() + "\n"))
}
