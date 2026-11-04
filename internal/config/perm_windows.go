//go:build windows

package config

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const aclMarker = ".acl-hardened-v2"

// hardenDataDir restricts the data directory to SYSTEM, Administrators and the
// account running phleetd. Go's 0600/0700 modes do not translate to Windows
// ACLs, so without this a token file under ProgramData can be readable by any
// local user. The call is best-effort: an unusual ACL setup must not prevent
// the control plane from starting.
func hardenDataDir(dir string) {
	marker := filepath.Join(dir, aclMarker)
	markerCurrent := false
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == "2" {
		markerCurrent = true
	}

	dirGrants := []string{
		"*S-1-5-18:(OI)(CI)F",     // LOCAL SYSTEM
		"*S-1-5-32-544:(OI)(CI)F", // BUILTIN\Administrators
	}
	fileGrants := []string{
		"*S-1-5-18:F",     // LOCAL SYSTEM
		"*S-1-5-32-544:F", // BUILTIN\Administrators
	}
	if u, err := user.Current(); err == nil {
		if sid := strings.TrimSpace(u.Uid); sid != "" {
			dirGrants = append(dirGrants, "*"+sid+":(OI)(CI)F")
			fileGrants = append(fileGrants, "*"+sid+":F")
		}
	}

	// /inheritance:r + /grant:r on the directory establishes the inheritable
	// ACL. Do not combine it with /T: icacls then strips the inherited ACEs
	// from existing files without granting the explicit file ACEs, leaving
	// tokens and the database with an empty DACL.
	dirArgs := append([]string{dir, "/inheritance:r", "/grant:r"}, dirGrants...)
	if exec.Command("icacls", dirArgs...).Run() != nil {
		return
	}
	if markerCurrent {
		return
	}
	// Repair existing files (including data created by an older broken
	// hardening pass). /grant:r adds explicit full-control ACEs and leaves the
	// directory inheritance intact.
	fileArgs := append([]string{dir, "/T", "/grant:r"}, fileGrants...)
	if exec.Command("icacls", fileArgs...).Run() != nil {
		return
	}
	_ = os.WriteFile(marker, []byte("2\n"), 0o600)
}
