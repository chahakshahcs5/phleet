// Package placement contains the pure scheduling rules used by the control
// plane: which phones should run a project, given labels, capacity and health.
package placement

import (
	"sort"
	"strconv"
	"strings"
)

// Server is the scheduler's view of a phone.
type Server struct {
	ID         string
	Online     bool
	Drained    bool
	NoUserland bool // rooted phone without Termux/chroot: metrics and shell only
	Labels     map[string]string
	Score      float64 // lower is better (temp + load)
	Placements int
}

// Need describes what a project wants.
type Need struct {
	Selector map[string]string
	Pinned   string
	Replicas int
	Existing map[string]bool // server IDs already hosting this project
}

// Match reports whether labels satisfy every selector key.
func Match(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// ParseSelector turns "tier=web,region=home" into a map.
func ParseSelector(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, &SelectorError{Part: part}
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if k == "" {
			return nil, &SelectorError{Part: part}
		}
		out[k] = v
	}
	return out, nil
}

type SelectorError struct{ Part string }

func (e *SelectorError) Error() string {
	return "invalid selector " + strconv.Quote(e.Part) + ", expected key=value"
}

func FormatSelector(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

// Plan returns the server IDs that should receive a new replica. It never
// returns more than the remaining capacity, never picks an ineligible phone,
// and distributes replicas by (placement count, health score, id).
func Plan(servers []Server, need Need) []string {
	if need.Replicas <= 0 {
		need.Replicas = 1
	}
	have := 0
	for id := range need.Existing {
		for _, s := range servers {
			if s.ID == id {
				have++
			}
		}
	}
	remaining := need.Replicas - have
	if remaining <= 0 {
		return nil
	}

	candidates := make([]Server, 0, len(servers))
	for _, s := range servers {
		if !s.Online || s.Drained || s.NoUserland {
			continue
		}
		if need.Pinned != "" && s.ID != need.Pinned {
			continue
		}
		if need.Existing[s.ID] {
			continue
		}
		if !Match(need.Selector, s.Labels) {
			continue
		}
		candidates = append(candidates, s)
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Placements != b.Placements {
			return a.Placements < b.Placements
		}
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		return a.ID < b.ID
	})
	if remaining > len(candidates) {
		remaining = len(candidates)
	}
	out := make([]string, 0, remaining)
	for _, c := range candidates[:remaining] {
		out = append(out, c.ID)
	}
	return out
}

// Eligible reports whether a phone may host new work for this project.
func Eligible(s Server, sel map[string]string, pinned string) bool {
	if !s.Online || s.Drained || s.NoUserland {
		return false
	}
	if pinned != "" && s.ID != pinned {
		return false
	}
	return Match(sel, s.Labels)
}
