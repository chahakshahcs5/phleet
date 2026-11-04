package httpapi

import (
	"net/http"
	"strconv"
	"time"
)

// sampleAppMetric caps app-metric writes at one row per 15 seconds per project,
// regardless of the 5-second heartbeat. The chart stays smooth and the SQLite
// table stays an order of magnitude smaller on a phone-heavy fleet.
func (s *Server) sampleAppMetric(projectID string) bool {
	s.appMetricMu.Lock()
	defer s.appMetricMu.Unlock()
	now := time.Now()
	if last, ok := s.appMetricAt[projectID]; ok && now.Sub(last) < 15*time.Second {
		return false
	}
	s.appMetricAt[projectID] = now
	return true
}

func (s *Server) handleProjectMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetProject(id); err != nil {
		apiErr(w, 404, err)
		return
	}
	minutes := 60
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20160 {
			minutes = n
		}
	}
	var bucket int64 = 30
	switch {
	case minutes > 2880:
		bucket = 1800
	case minutes > 720:
		bucket = 300
	case minutes > 120:
		bucket = 60
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute).Unix()
	samples, err := s.st.QueryAppMetrics(id, since, bucket)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"samples": samples, "bucketSec": bucket})
}

func (s *Server) handleProjectRequests(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetProject(id); err != nil {
		apiErr(w, 404, err)
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	minutes := 1440
	if v := r.URL.Query().Get("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20160 {
			minutes = n
		}
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute).Unix()
	logs, err := s.st.ListRequestLogs(id, limit, since)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"requests": logs})
}
