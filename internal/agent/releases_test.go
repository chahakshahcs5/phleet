//go:build linux

package agent

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestAppReleasesListsKeptReleases(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "apps", "demo")
	for _, name := range []string{"20260101-000000", "20260102-000000", "20260103-000000"} {
		if err := os.MkdirAll(filepath.Join(base, "releases", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "releases", "20260102-000000"), filepath.Join(base, "current")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	a := &Agent{home: home}
	res, err := a.appReleases("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %+v", res.Releases)
	}
	if res.Releases[0].Name != "20260103-000000" || res.Releases[1].Name != "20260102-000000" {
		t.Fatalf("releases are not newest first: %+v", res.Releases)
	}
	if !res.Releases[1].Current || res.Releases[0].Current {
		t.Fatalf("current marker: %+v", res.Releases)
	}
	if _, err := a.appReleases("../bad"); err == nil {
		t.Fatal("invalid slug must be rejected")
	}
}

func TestRollbackToSpecificRelease(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".phleet")
	base := filepath.Join(home, "apps", "worker")
	for _, name := range []string{"20260101-000000", "20260102-000000"} {
		if err := os.MkdirAll(filepath.Join(base, "releases", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := &Agent{
		home:     home,
		shPath:   "/bin/sh",
		cfg:      Config{StateDir: state},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		apps:     map[string]*appProcess{},
		appsPath: filepath.Join(state, "apps.json"),
		appCPU:   map[string]appCPUSample{},
	}
	p := a.ensureApp("worker", "Worker", 0)
	spec := AppSpec{
		Slug: "worker", Name: "Worker", Kind: "worker", Port: 0,
		StartCmd: "sleep 30", ReleaseDir: filepath.Join(base, "releases", "20260102-000000"),
	}
	if err := a.spawnProcess(p, spec); err != nil {
		t.Fatal(err)
	}
	defer a.stopProcess(p)

	if err := a.rollbackApp("worker", "20260101-000000"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	p.mu.Lock()
	got := p.spec.ReleaseDir
	p.mu.Unlock()
	if filepath.Base(got) != "20260101-000000" {
		t.Fatalf("release not switched: %q", got)
	}
	if current, err := filepath.EvalSymlinks(filepath.Join(base, "current")); err != nil || filepath.Base(current) != "20260101-000000" {
		t.Fatalf("current symlink: %v %q", err, current)
	}
	if err := a.rollbackApp("worker", "../escape"); err == nil {
		t.Fatal("invalid release name must be rejected")
	}
}

func TestIsHexRef(t *testing.T) {
	if !isHexRef("0123abc") || !isHexRef("0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("valid commit SHAs were rejected")
	}
	for _, v := range []string{"main", "v1.2.3", "feature/foo", "0123xyz", "abc"} {
		if isHexRef(v) {
			t.Fatalf("%q must not be treated as a commit", v)
		}
	}
}
