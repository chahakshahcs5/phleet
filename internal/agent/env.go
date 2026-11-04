package agent

import (
	"sort"
	"strings"
)

// secretEnv reports process environment names that must never be inherited by
// supervised applications, build steps, one-shot commands or terminal shells.
// The agent token is the fleet credential; a deployed app that can read it can
// impersonate the phone.
func secretEnv(name string) bool {
	switch name {
	case "PHLEET_TOKEN", "PHLEET_TOKEN_FILE", "PHLEET_SECRET", "PHLEET_API_TOKEN",
		// Not a secret itself, but it points at the directory containing the
		// device token; apps have no reason to receive it.
		"PHLEET_STATE_DIR":
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

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		m[kv[:i]] = kv[i+1:]
	}
	return m
}

func envFromMap(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

func applyEnv(m map[string]string, extra map[string]string) {
	for k, v := range extra {
		m[k] = v
	}
}

func deleteEnv(m map[string]string, names ...string) {
	for _, name := range names {
		delete(m, name)
	}
}
