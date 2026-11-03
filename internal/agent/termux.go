//go:build linux

package agent

import (
	"os"
	"path/filepath"
)

// applyTermuxTLS points Go's x509 at Termux's CA bundle. Without SSL_CERT_FILE,
// Go only looks at /etc/ssl/certs and similar paths that do not exist on
// Android, so every HTTPS request fails with "certificate signed by unknown
// authority".
func applyTermuxTLS() {
	if os.Getenv("SSL_CERT_FILE") != "" {
		return
	}
	candidates := []string{}
	if prefix := os.Getenv("PREFIX"); prefix != "" {
		candidates = append(candidates, filepath.Join(prefix, "etc", "tls", "cert.pem"))
	}
	candidates = append(candidates,
		"/data/data/com.termux/files/usr/etc/tls/cert.pem",
		"/system/etc/security/cacerts",
	)
	for _, c := range candidates {
		st, err := os.Stat(c)
		if err != nil || st.IsDir() {
			continue
		}
		_ = os.Setenv("SSL_CERT_FILE", c)
		return
	}
	// Android's system store is a directory of hashed certificates.
	if st, err := os.Stat("/system/etc/security/cacerts"); err == nil && st.IsDir() {
		_ = os.Setenv("SSL_CERT_DIR", "/system/etc/security/cacerts")
	}
}
