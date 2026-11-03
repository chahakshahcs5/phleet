package httpapi

import (
	"fmt"

	"phleet/internal/engines"
	"phleet/internal/model"
)

// capBool reads a boolean capability. The second result is false when the
// agent did not report it (older builds), so callers can stay permissive
// instead of blocking a phone that simply predates the field.
func capBool(caps map[string]any, key string) (bool, bool) {
	if caps == nil {
		return false, false
	}
	v, ok := caps[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func capRecord(caps map[string]any, key string) map[string]any {
	if caps == nil {
		return nil
	}
	m, _ := caps[key].(map[string]any)
	return m
}

// serverHasUserland reports whether a phone can build and run projects: Termux,
// a package manager, or a chroot/VM with git and a runtime. A rooted phone
// running the agent straight from /data/local/tmp has none of these.
func serverHasUserland(srv model.Server) bool {
	if v, ok := capBool(srv.Caps, "userland"); ok {
		return v
	}
	if v, ok := capBool(srv.Caps, "termux"); ok && v {
		return true
	}
	if v, ok := capBool(srv.Caps, "pkg"); ok && v {
		return true
	}
	return true
}

// engineInstalled reports whether the engine's binaries are already on the
// phone. The agent probes them on every reconnect.
func engineInstalled(srv model.Server, engineID string) bool {
	v, _ := capRecord(srv.Caps, "engines")[engineID].(bool)
	return v
}

// serverCanInstallEngine is true unless the agent explicitly reports no
// userland. Engine installs are best-effort (`pkg install` on Termux, a static
// download for rclone), so a host that already has the binaries is fine and a
// Linux host is never blocked. A monitoring-only Android phone has no way to
// install anything, so the API rejects the create instead of failing later.
func serverCanInstallEngine(srv model.Server, engineID string) bool {
	if engineInstalled(srv, engineID) {
		return true
	}
	if e, ok := engines.Get(engineID); ok {
		// Some engines are only packaged for Debian (beanstalkd, for example).
		// Do not offer them on Termux, where the install would fail.
		if len(e.Platforms) > 0 {
			manager, _ := srv.Caps["pkgManager"].(string)
			allowed := false
			for _, p := range e.Platforms {
				if p == manager || (p == "static" && e.Install != nil) {
					allowed = true
					break
				}
			}
			if !allowed {
				return false
			}
		}
		// Engines with their own installer (a static arm64 download into
		// $PHLEET_STATE_DIR/bin) only need curl/tar, not a package manager.
		if e.Install != nil {
			return serverHasUserland(srv)
		}
	}
	// pkgInstall is explicit: apt on an unprivileged chroot user needs sudo.
	if v, ok := capBool(srv.Caps, "pkgInstall"); ok {
		return v
	}
	if v, ok := capBool(srv.Caps, "userland"); ok && !v {
		return false
	}
	return true
}

func serverLabel(srv model.Server) string {
	if srv.Name != "" {
		return srv.Name
	}
	return srv.ID
}

func noUserlandError(srv model.Server, consequence string) error {
	return fmt.Errorf("%s is monitoring-only (no Termux or userland): %s", serverLabel(srv), consequence)
}
