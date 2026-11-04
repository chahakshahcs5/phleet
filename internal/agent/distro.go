//go:build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// packageManager returns the package manager the agent can drive. Termux's pkg
// comes first: Termux also ships apt, but pkg is the supported entry point.
func packageManager() string {
	for _, c := range []struct{ bin, name string }{
		{"pkg", "pkg"},
		{"apt-get", "apt"},
		{"dnf", "dnf"},
		{"apk", "apk"},
		{"pacman", "pacman"},
	} {
		if lookToolBool(c.bin) {
			return c.name
		}
	}
	return ""
}

// canSudo reports whether the agent user can run package management as root
// without a password. The Debian bootstrap intentionally does not grant that
// (the agent stays unprivileged so PostgreSQL can run), but an operator may
// configure a narrow sudo rule themselves. Testing `sudo -n apt-get --version`
// avoids relying on `sudo -n true`, which a command-scoped rule would reject.
func canSudo() bool {
	if os.Geteuid() == 0 {
		return true
	}
	sudo, ok := lookTool("sudo")
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, sudo, "-n", "apt-get", "--version").Run() == nil
}

// osLabel describes the operating system as the panel shows it. Android
// exposes its release in /system/build.prop; a Debian chroot on the same phone
// has /etc/os-release instead. Reporting "linux/linux" for the latter was
// useless, so a chroot now reports the distro it actually runs.
func osLabel() string {
	if v := androidRelease(); v != "" {
		return "linux/" + v
	}
	if v := distroRelease(); v != "" {
		return "linux/" + v
	}
	return "linux"
}

// distroRelease returns "<id>-<version>" from /etc/os-release, e.g. debian-13.
func distroRelease() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	id := strings.ToLower(vals["ID"])
	if id == "" {
		if f := strings.Fields(vals["NAME"]); len(f) > 0 {
			id = strings.ToLower(f[0])
		}
	}
	if id == "" {
		return ""
	}
	if ver := vals["VERSION_ID"]; ver != "" {
		return id + "-" + ver
	}
	return id
}

// applyDistroToolPaths adds Debian/Ubuntu PostgreSQL's versioned bin directory
// to PATH. The distro package installs initdb, pg_ctl, postgres and
// pg_basebackup there, so an apt-installed addon would otherwise look missing.
func applyDistroToolPaths() {
	dirs, err := filepath.Glob("/usr/lib/postgresql/*/bin")
	if err != nil || len(dirs) == 0 {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	parts := filepath.SplitList(os.Getenv("PATH"))
	for _, d := range dirs {
		seen := false
		for _, p := range parts {
			if p == d {
				seen = true
				break
			}
		}
		if !seen {
			parts = append([]string{d}, parts...)
		}
	}
	_ = os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}
