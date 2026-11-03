// Package config resolves the data directory and the long-lived secrets used
// by the control plane.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Version   = "0.10.1"
	AdminFile = "admin_token"
	AgentFile = "agent_token"
	KeyFile   = "session.key"
)

func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("ProgramData"); p != "" {
			return filepath.Join(p, "phleet")
		}
	}
	if p := os.Getenv("PHLEET_HOME"); p != "" {
		return p
	}
	if p, err := os.UserConfigDir(); err == nil {
		return filepath.Join(p, "phleet")
	}
	return filepath.Join(os.TempDir(), "phleet")
}

type Config struct {
	DataDir        string
	Addr           string
	PublicURL      string
	BridgeLAN      bool
	StorageMaxBody int64
	AdminToken     string
	AgentToken     string
	SessionKey     []byte
	ServerSecret   string
}

// Load ensures the data directory and secrets exist. Existing secrets are
// never rotated automatically: agents and browsers would break.
func Load(dataDir string) (*Config, error) {
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	hardenDataDir(dataDir)
	admin, err := readOrCreate(filepath.Join(dataDir, AdminFile), 32)
	if err != nil {
		return nil, err
	}
	agent, err := readOrCreate(filepath.Join(dataDir, AgentFile), 32)
	if err != nil {
		return nil, err
	}
	key, err := readOrCreate(filepath.Join(dataDir, KeyFile), 64)
	if err != nil {
		return nil, err
	}
	return &Config{
		DataDir:    dataDir,
		AdminToken: admin,
		AgentToken: agent,
		SessionKey: []byte(key),
	}, nil
}

func readOrCreate(path string, bytes int) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		v := strings.TrimSpace(string(b))
		if v != "" {
			return v, nil
		}
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	v := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(v+"\n"), 0o600); err != nil {
		return "", err
	}
	return v, nil
}

func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "phleet.db") }
func (c *Config) BinDir() string { return filepath.Join(c.DataDir, "bin") }
func (c *Config) CloudflaredConfig() string {
	return filepath.Join(c.DataDir, "cloudflare.json")
}
