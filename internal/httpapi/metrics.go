package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"phleet/internal/config"
)

// handlePrometheus exposes a small set of fleet gauges. It is behind the
// panel credential so a scrape uses the admin token (or a session cookie):
//
//	curl -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/metrics
func (s *Server) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	servers, samples, statsErr := s.st.Stats()
	projects, _ := s.st.ListProjects()
	addons, _ := s.st.ListAddons()
	alerts, _ := s.st.CountActiveAlerts()

	writeGauge := func(name, help string, value any) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", name, help, name, name, value)
	}
	writeGauge("phleet_build_info", "Build and version information.", 1)
	fmt.Fprintf(w, "# HELP phleet_build_version phleet version as a label.\n# TYPE phleet_build_version gauge\n")
	fmt.Fprintf(w, "phleet_build_version{version=%q} 1\n", config.Version)
	if statsErr == nil {
		writeGauge("phleet_servers_total", "Registered phones.", servers)
		writeGauge("phleet_metric_rows_total", "Stored metric samples.", samples)
	}
	writeGauge("phleet_agents_online", "Agents currently connected.", s.hub.Count())
	writeGauge("phleet_projects_total", "Projects (including stack services).", len(projects))
	writeGauge("phleet_addons_total", "Addons and catalogue services.", len(addons))
	writeGauge("phleet_alerts_active", "Alerts currently firing or acknowledged.", alerts)

	// Keep the endpoint useful without leaking project names or credentials.
	byKind := map[string]int{}
	for _, a := range addons {
		byKind[strings.ToLower(a.Kind)]++
	}
	fmt.Fprintf(w, "# HELP phleet_addons_by_kind Addons grouped by kind.\n# TYPE phleet_addons_by_kind gauge\n")
	keys := make([]string, 0, len(byKind))
	for k := range byKind {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, kind := range keys {
		fmt.Fprintf(w, "phleet_addons_by_kind{kind=%q} %d\n", kind, byKind[kind])
	}
}
