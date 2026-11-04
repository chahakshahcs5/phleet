// Package config resolves the data directory and the long-lived secrets used
// by the control plane.
package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Version   = "0.16.0"
	AdminFile = "admin_token"
	AgentFile = "agent_token"
	// KeyFile is the data-encryption key used for env, secret files, TOTP and
	// backups. The name is historical; SigningKeyFile is separate so rotating
	// one does not make the other's data unreadable.
	KeyFile        = "session.key"
	SigningKeyFile = "signing.key"
	UpdateKeyFile  = "update.key"
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
	SigningKey     []byte
	ServerSecret   string
	UpdateKey      ed25519.PrivateKey
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
	signingKey, err := readOrCreate(filepath.Join(dataDir, SigningKeyFile), 64)
	if err != nil {
		return nil, err
	}
	updateKey, err := loadOrCreateUpdateKey(filepath.Join(dataDir, UpdateKeyFile))
	if err != nil {
		return nil, err
	}
	return &Config{
		DataDir:    dataDir,
		AdminToken: admin,
		AgentToken: agent,
		SessionKey: []byte(key),
		SigningKey: []byte(signingKey),
		UpdateKey:  updateKey,
	}, nil
}

func loadOrCreateUpdateKey(path string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("invalid %s", path)
		}
		return ed25519.PrivateKey(raw), nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

func (c *Config) UpdatePublicKey() string {
	if len(c.UpdateKey) != ed25519.PrivateKeySize {
		return ""
	}
	pub := c.UpdateKey.Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(pub)
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
