//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phleet/internal/agentproto"
)

func envLookup(env []string, name string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return strings.TrimPrefix(kv, name+"=")
		}
	}
	return ""
}

func TestPrepareGitAuthHTTPS(t *testing.T) {
	a := &Agent{cfg: Config{StateDir: t.TempDir()}, shPath: "/bin/sh"}
	env, cleanup, err := a.prepareGitAuth(agentproto.GitAuth{
		Type: "https", Username: "deploy", Secret: "token-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := envLookup(env, "PHLEET_GIT_USERNAME"); got != "deploy" {
		t.Fatalf("username env = %q", got)
	}
	if got := envLookup(env, "PHLEET_GIT_PASSWORD"); got != "token-value" {
		t.Fatalf("password env = %q", got)
	}
	script := envLookup(env, "GIT_ASKPASS")
	if script == "" {
		t.Fatal("GIT_ASKPASS is missing")
	}
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "PHLEET_GIT_PASSWORD") {
		t.Fatalf("askpass script does not read the password env: %s", b)
	}
	cleanup()
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatalf("askpass script survived cleanup: %v", err)
	}
}

func TestPrepareGitAuthSSH(t *testing.T) {
	a := &Agent{cfg: Config{StateDir: t.TempDir()}, shPath: "/bin/sh"}
	const key = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n"
	env, cleanup, err := a.prepareGitAuth(agentproto.GitAuth{Type: "ssh", Secret: key})
	if err != nil {
		t.Fatal(err)
	}
	command := envLookup(env, "GIT_SSH_COMMAND")
	if !strings.Contains(command, "IdentitiesOnly=yes") || !strings.Contains(command, "StrictHostKeyChecking=accept-new") {
		t.Fatalf("GIT_SSH_COMMAND = %q", command)
	}
	start := strings.Index(command, "-i '")
	if start < 0 {
		t.Fatalf("GIT_SSH_COMMAND has no key path: %q", command)
	}
	start += len("-i '")
	end := strings.Index(command[start:], "'")
	if end < 0 {
		t.Fatalf("GIT_SSH_COMMAND key path is unterminated: %q", command)
	}
	keyPath := command[start : start+end]
	b, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != key {
		t.Fatalf("key content = %q", b)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(keyPath)); !os.IsNotExist(err) {
		t.Fatalf("key directory survived cleanup: %v", err)
	}
}

func TestPrepareGitAuthRejectsEmptySecret(t *testing.T) {
	a := &Agent{cfg: Config{StateDir: t.TempDir()}, shPath: "/bin/sh"}
	if _, _, err := a.prepareGitAuth(agentproto.GitAuth{Type: "https"}); err == nil {
		t.Fatal("empty secret must be rejected")
	}
}
