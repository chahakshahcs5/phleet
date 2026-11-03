package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"phleet/internal/agentproto"
	"phleet/internal/model"
)

type serverView struct {
	model.Server
	Online bool          `json:"online"`
	Latest *model.Sample `json:"latest,omitempty"`
}

func (s *Server) view(srv model.Server) serverView {
	v := serverView{Server: srv, Online: s.hub.Online(srv.ID)}
	if latest, err := s.st.LatestMetrics(srv.ID); err == nil {
		v.Latest = &latest
	}
	return v
}

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.st.ListServers()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	out := make([]serverView, 0, len(servers))
	for _, srv := range servers {
		out = append(out, s.view(srv))
	}
	writeJSON(w, 200, map[string]any{
		"servers":      out,
		"now":          time.Now().Unix(),
		"agentsOnline": s.hub.Count(),
	})
}

func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	srv, err := s.st.GetServer(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{"server": s.view(srv)})
}

func (s *Server) handlePatchServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	srv, err := s.st.GetServer(id)
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
		Drain  *bool             `json:"drain"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	if body.Name != "" {
		srv.Name = body.Name
	}
	if body.Labels != nil {
		srv.Labels = body.Labels
	}
	if err := s.st.UpdateServerMeta(id, srv.Name, srv.Labels); err != nil {
		apiErr(w, 500, err)
		return
	}
	if body.Drain != nil {
		if err := s.st.SetServerDrain(id, *body.Drain); err != nil {
			apiErr(w, 500, err)
			return
		}
		srv.Drain = *body.Drain
	}
	_ = s.st.AddEvent("ui", "server.update", id, srv.Name)
	s.triggerReconcile()
	s.handleGetServer(w, r)
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetServer(id); err != nil {
		apiErr(w, 404, err)
		return
	}
	s.hub.Kick(id)
	if err := s.st.DeleteServer(id); err != nil {
		apiErr(w, 500, err)
		return
	}
	for _, suffix := range []string{":offline", ":temp", ":battery", ":disk"} {
		_, _ = s.st.ResolveAlert("server:" + id + suffix)
	}
	_ = s.st.AddEvent("ui", "server.delete", id, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	minutes := 360
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
	samples, err := s.st.QueryMetrics(id, since, bucket)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"samples": samples, "bucketSec": bucket})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeoutSec"`
		Cwd        string `json:"cwd"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	if body.Command == "" {
		apiErr(w, 400, errors.New("command is required"))
		return
	}
	timeout := body.TimeoutSec
	if timeout <= 0 {
		timeout = 20
	}
	if timeout > 300 {
		timeout = 300
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout+10)*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, id, agentproto.TypeExec, agentproto.ExecRequest{
		Command: body.Command, TimeoutSec: timeout, Cwd: body.Cwd,
	})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.ExecResponse
	_ = jsonUnmarshal(env.Data, &res)
	_ = s.st.AddEvent("ui", "exec", id, body.Command)
	writeJSON(w, 200, res)
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, r.PathValue("id"), agentproto.TypeProcList, agentproto.Empty{})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.ProcListResponse
	_ = jsonUnmarshal(env.Data, &res)
	writeJSON(w, 200, res)
}

func errText(s string) error { return errors.New(s) }

func jsonUnmarshal(raw []byte, dst any) error { return json.Unmarshal(raw, dst) }
