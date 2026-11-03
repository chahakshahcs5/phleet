//go:build windows

package config

import (
	"os/exec"
	"os/user"
	"strings"
)

// hardenDataDir restricts the data directory to SYSTEM, Administrators and the
// account running phleetd. Go's 0600/0700 modes do not translate to Windows
// ACLs, so without this a token file under ProgramData can be readable by any
// local user. The call is best-effort: an unusual ACL setup must not prevent
// the control plane from starting.
func hardenDataDir(dir string) {
	args := []string{
		dir,
		"/inheritance:r",
		"/grant:r",
		"*S-1-5-18:(OI)(CI)F",     // LOCAL SYSTEM
		"*S-1-5-32-544:(OI)(CI)F", // BUILTIN\Administrators
	}
	if u, err := user.Current(); err == nil {
		if sid := strings.TrimSpace(u.Uid); sid != "" {
			args = append(args, "*"+sid+":(OI)(CI)F")
		}
	}
	args = append(args, "/T")
	_ = exec.Command("icacls", args...).Run()
}
