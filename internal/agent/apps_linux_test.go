//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appEnvValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return strings.TrimPrefix(kv, name+"="), true
		}
	}
	return "", false
}

func TestAppEnvExposesPersistentDataDir(t *testing.T) {
	const dir = "/home/phleet/apps/demo/shared/data"
	env := appEnv(AppSpec{Slug: "demo", Port: 4100, DataDir: dir})

	if got, ok := appEnvValue(env, "PHLEET_DATA_DIR"); !ok || got != dir {
		t.Fatalf("PHLEET_DATA_DIR = %q (present=%v), want %q", got, ok, dir)
	}
	if got, _ := appEnvValue(env, "HOST"); got != "127.0.0.1" {
		t.Fatalf("HOST = %q, want loopback", got)
	}
	if got, _ := appEnvValue(env, "PORT"); got != "4100" {
		t.Fatalf("PORT = %q, want 4100", got)
	}
}

func TestAppEnvOmitsEmptyDataDir(t *testing.T) {
	env := appEnv(AppSpec{Slug: "demo", Port: 4100})
	if _, ok := appEnvValue(env, "PHLEET_DATA_DIR"); ok {
		t.Fatal("PHLEET_DATA_DIR must be omitted when no data dir is set")
	}
}

func TestWriteSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := writeSecrets(dir, map[string]string{"a.json": "one", "b.pem": "two"}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a.json")); err != nil || string(b) != "one" {
		t.Fatalf("secret content: %v %q", err, b)
	}
	if st, err := os.Stat(filepath.Join(dir, "a.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode: %v %v", err, st)
	}
	if err := writeSecrets(dir, map[string]string{"a.json": "updated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.pem")); !os.IsNotExist(err) {
		t.Fatal("deleted secret should be removed")
	}
	if err := writeSecrets(dir, map[string]string{"../escape": "x"}); err == nil {
		t.Fatal("invalid secret name must be rejected")
	}
}

func TestAppEnvWorkerHasNoPort(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("HOST", "0.0.0.0")
	env := appEnv(AppSpec{Slug: "jobs", Kind: "worker", DataDir: "/home/phleet/apps/jobs/shared/data"})
	if _, ok := appEnvValue(env, "PORT"); ok {
		t.Fatal("a worker must not receive PORT")
	}
	if _, ok := appEnvValue(env, "HOST"); ok {
		t.Fatal("a worker must not inherit HOST")
	}
	if got, _ := appEnvValue(env, "PHLEET_WORKER"); got != "1" {
		t.Fatalf("PHLEET_WORKER = %q, want 1", got)
	}
	if got, _ := appEnvValue(env, "PHLEET_DATA_DIR"); got == "" {
		t.Fatal("a worker still gets a persistent data dir")
	}
}

func TestAppEnvProjectOverridesBuiltins(t *testing.T) {
	env := appEnv(AppSpec{
		Slug: "demo", Port: 4100,
		Env: map[string]string{"HOST": "0.0.0.0", "PORT": "9999", "NODE_ENV": "test"},
	})
	if got, _ := appEnvValue(env, "HOST"); got != "0.0.0.0" {
		t.Fatalf("HOST = %q, want project override", got)
	}
	if got, _ := appEnvValue(env, "PORT"); got != "9999" {
		t.Fatalf("PORT = %q, want project override", got)
	}
	if got, _ := appEnvValue(env, "NODE_ENV"); got != "test" {
		t.Fatalf("NODE_ENV = %q, want project override", got)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOST=") && kv != "HOST=0.0.0.0" {
			t.Fatalf("duplicate HOST entry: %q", kv)
		}
	}
}

func TestAddonEnvDropsAppContract(t *testing.T) {
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("PORT", "9999")
	env := appEnv(AppSpec{Slug: "db", Addon: true, Kind: "database"})
	for _, name := range []string{"HOST", "PORT", "NODE_ENV", "PHLEET_APP", "PHLEET_PORT"} {
		if _, ok := appEnvValue(env, name); ok {
			t.Fatalf("addon inherited %s", name)
		}
	}
}
