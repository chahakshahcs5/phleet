package agent

import (
	"path/filepath"
)

// buildRoots returns the file-browser/shell sandbox roots. The user's home is
// always present; --root adds directories instead of replacing it.
func buildRoots(home string, extra []string) []string {
	roots := make([]string, 0, len(extra)+1)
	seen := make(map[string]bool, len(extra)+1)
	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			return
		}
		seen[abs] = true
		roots = append(roots, abs)
	}
	add(home)
	for _, r := range extra {
		add(r)
	}
	return roots
}
