package httpapi

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"phleet/internal/store"
)

func (s *Server) handleExportEvents(w http.ResponseWriter, r *http.Request) {
	q := store.EventQuery{
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Actor:  strings.TrimSpace(r.URL.Query().Get("actor")),
		Search: strings.TrimSpace(r.URL.Query().Get("q")),
	}
	limit := 10000
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 50000 {
		limit = n
	}
	events, err := s.st.ExportEvents(q, limit)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	if strings.EqualFold(r.URL.Query().Get("format"), "json") {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=phleet-events-"+stamp+".json")
		writeJSON(w, 200, map[string]any{"events": events})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=phleet-events-"+stamp+".csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "timestamp", "iso", "actor", "action", "target", "detail"})
	for _, e := range events {
		_ = cw.Write([]string{
			fmt.Sprint(e.ID),
			fmt.Sprint(e.TS),
			time.Unix(e.TS, 0).UTC().Format(time.RFC3339),
			e.Actor, e.Action, e.Target, e.Detail,
		})
	}
	cw.Flush()
}
