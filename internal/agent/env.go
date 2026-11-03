package agent

import "strings"

// secretEnv reports process environment names that must never be inherited by
// supervised applications, build steps, one-shot commands or terminal shells.
// The agent token is the fleet credential; a deployed app that can read it can
// impersonate the phone.
func secretEnv(name string) bool {
	switch name {
	case "PHLEET_TOKEN", "PHLEET_TOKEN_FILE", "PHLEET_SECRET", "PHLEET_API_TOKEN":
		return true
	}
	return false
}

func sanitizeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if secretEnv(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
