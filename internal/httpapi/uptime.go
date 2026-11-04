package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"phleet/internal/model"
)

func (s *Server) handleProjectUptime(w http.ResponseWriter, r *http.Request) {
	u, err := s.st.GetUptimeStatus(r.PathValue("id"))
	if err != nil {
		writeJSON(w, 200, map[string]any{"uptime": nil})
		return
	}
	writeJSON(w, 200, map[string]any{"uptime": u})
}

func (s *Server) uptimeLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkUptime()
		}
	}
}

func (s *Server) checkUptime() {
	publicURL, _ := s.Edge()
	if publicURL == "" {
		return
	}
	projects, err := s.st.ListProjects()
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, p := range projects {
		if p.Kind == "stack" || p.Hostname == "" {
			continue
		}
		host := p.Hostname
		if p.CustomHostname != "" {
			host = p.CustomHostname
		}
		path := p.HealthPath
		if path == "" {
			path = "/"
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		scheme := "https"
		if strings.HasPrefix(publicURL, "http://") {
			scheme = "http"
		}
		url := scheme + "://" + host + path
		resp, err := client.Get(url)
		u := model.UptimeStatus{ProjectID: p.ID, CheckedAt: time.Now().Unix()}
		if err != nil {
			u.Error = err.Error()
		} else {
			u.StatusCode = resp.StatusCode
			u.OK = resp.StatusCode < 500
			_ = resp.Body.Close()
		}
		_ = s.st.SetUptimeStatus(u)
		key := "uptime:" + p.ID
		if u.OK {
			s.resolveAlert(key)
		} else {
			msg := p.Name + " is unreachable from the public edge"
			if u.Error != "" {
				msg += ": " + u.Error
			} else {
				msg += ": HTTP " + http.StatusText(u.StatusCode)
			}
			s.fireAlert(key, "uptime", "warning", p.ID, msg)
		}
	}
}
