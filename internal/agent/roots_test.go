package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRootsAlwaysIncludesHome(t *testing.T) {
	home := t.TempDir()
	extra := t.TempDir()

	roots := buildRoots(home, []string{extra})
	homeResolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	extraResolved, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("roots = %#v, want home + extra", roots)
	}
	if roots[0] != homeResolved {
		t.Fatalf("roots[0] = %q, want home %q", roots[0], homeResolved)
	}
	if roots[1] != extraResolved {
		t.Fatalf("roots[1] = %q, want extra %q", roots[1], extraResolved)
	}
}

func TestBuildRootsDeduplicatesHome(t *testing.T) {
	home := t.TempDir()
	roots := buildRoots(home, []string{home, "", home})
	if len(roots) != 1 {
		t.Fatalf("roots = %#v, want one entry", roots)
	}
}

func TestBuildRootsSurvivesMissingExtra(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(t.TempDir(), "not-created")
	roots := buildRoots(home, []string{missing})
	if len(roots) != 2 {
		t.Fatalf("roots = %#v, want home + missing path", roots)
	}
	if _, err := os.Stat(roots[1]); !os.IsNotExist(err) {
		t.Fatalf("expected missing extra to remain configured, stat err = %v", err)
	}
}
