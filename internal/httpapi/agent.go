package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/hub"
	"phleet/internal/model"
)

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	// The header may contain the shared enrollment token or a per-device
	// token. We cannot know which until the hello supplies the server ID, so
	// accept the socket and verify immediately after reading hello.
	tok := bearer(r)
	if tok == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		s.log.Warn("agent accept failed", "err", err)
		return
	}
	ws.SetReadLimit(256 << 10)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	typ, data, err := ws.Read(ctx)
	cancel()
	if err != nil || typ != websocket.MessageText {
		_ = ws.Close(websocket.StatusPolicyViolation, "hello expected")
		return
	}
	var env agentproto.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type != agentproto.TypeHello {
		_ = ws.Close(websocket.StatusPolicyViolation, "hello expected")
		return
	}
	var hb agentproto.Hello
	if err := json.Unmarshal(env.Data, &hb); err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, "bad hello")
		return
	}
	if hb.ID == "" {
		hb.ID = uuid.NewString()
	}
	if hb.Name == "" {
		hb.Name = hb.Hostname
	}
	if hb.Name == "" {
		hb.Name = "phone-" + hb.ID[:6]
	}
	if hb.Caps == nil {
		hb.Caps = map[string]any{}
	}
	canEnroll, _ := hb.Caps["deviceTokens"].(bool)
	ok, newToken := s.agentHelloAuth(tok, hb.ID, canEnroll)
	if !ok {
		s.log.Warn("agent hello rejected", "id", hb.ID, "host", hb.Hostname)
		_ = ws.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}
	ackRaw, _ := json.Marshal(agentproto.HelloAck{OK: true, Token: newToken})
	ack, _ := json.Marshal(agentproto.Envelope{Type: agentproto.TypeHelloAck, Data: ackRaw})
	wctx, wcancel := context.WithTimeout(r.Context(), 10*time.Second)
	err = ws.Write(wctx, websocket.MessageText, ack)
	wcancel()
	if err != nil {
		s.log.Warn("agent hello ack failed", "id", hb.ID, "err", err)
		return
	}
	if newToken != "" {
		// Commit the hash when the connection registers, after the token has
		// safely left the control plane.
		s.setPendingDeviceToken(hb.ID, newToken)
	}
	s.log.Info("agent connected", "id", hb.ID, "name", hb.Name, "host", hb.Hostname, "arch", hb.Arch)

	conn := hub.NewConn(hb.ID, ws)
	s.hub.Serve(conn, hb)
	s.log.Info("agent disconnected", "id", hb.ID)
}

func (s *Server) onHello(hb agentproto.Hello) error {
	s.cancelOffline(hb.ID)
	existing, err := s.st.GetServer(hb.ID)
	labels := map[string]string{}
	created := time.Now().Unix()
	name := hb.Name
	if err == nil {
		labels = existing.Labels
		created = existing.CreatedAt
		if existing.Name != "" {
			name = existing.Name
		}
	}
	srv := model.Server{
		ID:           hb.ID,
		Name:         name,
		Hostname:     hb.Hostname,
		OS:           hb.OS,
		Arch:         hb.Arch,
		AgentVersion: hb.AgentVersion,
		Caps:         hb.Caps,
		Labels:       labels,
		Status:       "online",
		LastSeen:     time.Now().Unix(),
		CreatedAt:    created,
	}
	if err := s.st.UpsertServer(srv); err != nil {
		return err
	}
	s.adoptPendingDeviceToken(hb.ID)
	_ = s.st.AddEvent("agent", "agent.online", hb.ID, name+" ("+hb.Hostname+")")
	s.triggerReconcile()
	go s.syncRelaysForServer(hb.ID)
	go s.syncAddonLeases(hb.ID)
	return nil
}

func (s *Server) onHeartbeat(id string, hb agentproto.Heartbeat) {
	m := hb.Metrics
	// At == 0 means the agent has not collected a sample yet (the first
	// heartbeat after it starts). Store nothing rather than a row of zeroes,
	// but still touch the server and process apps/caps below.
	if m.At != 0 {
		// Store control-plane time, not the phone's clock. Android clocks can
		// be wrong after boot, and a malicious agent must not be able to write
		// far-future rows that break charts and retention.
		storedAt := time.Now().Unix()
		if skew := storedAt - m.At; skew > 300 || skew < -300 {
			s.log.Debug("agent clock skew", "server", id, "skewSec", skew)
		}
		sm := model.Metrics{
			CPUPercent: m.CPUPercent, MemUsed: m.MemUsed, MemTotal: m.MemTotal,
			DiskUsed: m.DiskUsed, DiskTotal: m.DiskTotal, TempC: m.TempC,
			BatteryPct: m.BatteryPct, BatteryTemp: m.BatteryTemp, Charging: m.Charging,
			Load1: m.Load1, NetRx: m.NetRx, NetTx: m.NetTx, Uptime: m.Uptime, At: storedAt,
		}
		if err := s.st.InsertMetrics(id, sm); err != nil {
			s.log.Warn("insert metrics", "err", err, "server", id)
		}
	}
	if err := s.st.TouchServer(id, "online"); err != nil {
		s.log.Warn("touch server", "err", err, "server", id)
	}
	// Capabilities can change while the agent runs (an engine installed by the
	// operator, Termux added). The agent sends the map only when it changes.
	if len(hb.Caps) > 0 {
		if err := s.st.SetServerCaps(id, hb.Caps); err != nil {
			s.log.Warn("update caps", "err", err, "server", id)
		}
	}
	for _, app := range hb.Apps {
		s.syncAppStatus(id, app)
	}
}

func (s *Server) onDisconnect(id string) {
	s.scheduleOffline(id)
}

// offlineGrace keeps short reconnects from flashing a phone offline. The hub
// only calls onDisconnect for the current connection, so this is a real drop;
// if a replacement connection registers before the timer fires, it is skipped.
const offlineGrace = 6 * time.Second

func (s *Server) scheduleOffline(id string) {
	s.offlineMu.Lock()
	if t, ok := s.offlineTimers[id]; ok {
		t.Stop()
	}
	s.offlineTimers[id] = time.AfterFunc(offlineGrace, func() {
		s.offlineMu.Lock()
		delete(s.offlineTimers, id)
		s.offlineMu.Unlock()
		if s.hub.Online(id) {
			return
		}
		s.markOffline(id)
	})
	s.offlineMu.Unlock()
}

func (s *Server) cancelOffline(id string) {
	s.offlineMu.Lock()
	if t, ok := s.offlineTimers[id]; ok {
		t.Stop()
		delete(s.offlineTimers, id)
	}
	s.offlineMu.Unlock()
}

func (s *Server) markOffline(id string) {
	if s.hub.Online(id) {
		return
	}
	srv, _ := s.st.GetServer(id)
	if err := s.st.SetServerOffline(id); err != nil {
		s.log.Warn("mark offline", "err", err, "server", id)
	}
	detail := id
	if srv.Name != "" {
		detail = srv.Name
		if srv.Hostname != "" {
			detail += " (" + srv.Hostname + ")"
		}
	}
	detail += " - connection closed"
	_ = s.st.AddEvent("agent", "agent.offline", id, detail)
	s.triggerReconcile()
}

func (s *Server) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	if !s.agentTokenAuthed(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := "phleet-agent-linux-arm64"
	if q := r.URL.Query().Get("arch"); q != "" {
		switch q {
		case "arm64", "amd64", "arm":
			name = "phleet-agent-linux-" + q
		}
	}
	p := filepath.Join(s.cfg.BinDir(), name)
	f, err := os.Open(p)
	if err != nil {
		writeJSON(w, 404, map[string]any{
			"error": "agent binary not published on this control plane",
			"hint":  "run scripts/build.ps1, then copy " + name + " into " + s.cfg.BinDir(),
		})
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

var errAgentOffline = errors.New("agent offline")
