// Package httpapi exposes the control plane HTTP and WebSocket API.
package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"phleet/internal/config"
	"phleet/internal/hub"
	"phleet/internal/store"
	"phleet/internal/webui"
)

type Server struct {
	cfg     *config.Config
	st      *store.Store
	hub     *hub.Hub
	log     *slog.Logger
	mux     *http.ServeMux
	started time.Time
	edge    edgeState

	rrMu sync.Mutex
	rr   map[string]int

	orphanMu sync.Mutex
	orphans  map[string]time.Time

	recMu sync.Mutex
	recAt map[string]time.Time

	offlineMu     sync.Mutex
	offlineTimers map[string]*time.Timer

	tunnel *tunnelManager

	bridges *tcpBridge

	reconcileCh chan struct{}
}

func New(cfg *config.Config, st *store.Store, log *slog.Logger) *Server {
	s := &Server{
		cfg: cfg, st: st, log: log, mux: http.NewServeMux(), started: time.Now(),
		rr:            map[string]int{},
		orphans:       map[string]time.Time{},
		recAt:         map[string]time.Time{},
		offlineTimers: map[string]*time.Timer{},
		reconcileCh:   make(chan struct{}, 1),
		tunnel:        newTunnelManager(cfg, log),
	}
	s.edge.publicURL = cfg.PublicURL
	s.bridges = newTCPBridge(s)
	s.hub = hub.New(hub.Callbacks{
		OnHello:       s.onHello,
		OnHeartbeat:   s.onHeartbeat,
		OnDisconnect:  s.onDisconnect,
		OnDeployEvent: s.onDeployEvent,
		OnAddonEvent:  s.onAddonEvent,
	})
	s.routes()
	return s
}

func (s *Server) Hub() *hub.Hub { return s.hub }

// Close releases listeners and live agent connections. Tests use it; the
// production process exits instead.
func (s *Server) Close() {
	if s.bridges != nil {
		s.bridges.stopAll()
	}
	s.hub.CloseAll()
}

func (s *Server) Handler() http.Handler { return securityHeaders(http.HandlerFunc(s.route)) }

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	api := http.NewServeMux()

	api.HandleFunc("GET /me", s.handleMe)
	api.HandleFunc("POST /logout", s.handleLogout)
	api.HandleFunc("GET /edge/tunnel", s.handleTunnelStatus)
	api.HandleFunc("POST /edge/tunnel/start", s.handleTunnelAction("start"))
	api.HandleFunc("POST /edge/tunnel/stop", s.handleTunnelAction("stop"))
	api.HandleFunc("POST /edge/tunnel/restart", s.handleTunnelAction("restart"))
	api.HandleFunc("GET /events", s.handleEvents)
	api.HandleFunc("GET /events/facets", s.handleEventFacets)
	api.HandleFunc("GET /setup", s.handleSetupInfo)
	api.HandleFunc("GET /servers", s.handleListServers)
	api.HandleFunc("POST /servers/provision", s.handleProvisionServer)
	api.HandleFunc("GET /servers/{id}", s.handleGetServer)
	api.HandleFunc("PATCH /servers/{id}", s.handlePatchServer)
	api.HandleFunc("DELETE /servers/{id}", s.handleDeleteServer)
	api.HandleFunc("GET /servers/{id}/metrics", s.handleMetrics)
	api.HandleFunc("POST /servers/{id}/exec", s.handleExec)
	api.HandleFunc("GET /servers/{id}/processes", s.handleProcesses)

	api.HandleFunc("GET /servers/{id}/files", s.handleFSList)
	api.HandleFunc("GET /servers/{id}/file", s.handleFSRead)
	api.HandleFunc("PUT /servers/{id}/file", s.handleFSWrite)
	api.HandleFunc("POST /servers/{id}/fs", s.handleFSOp)
	api.HandleFunc("POST /servers/{id}/upload", s.handleUpload)
	api.HandleFunc("POST /servers/{id}/upload-folder", s.handleUploadFolder)
	api.HandleFunc("GET /servers/{id}/download", s.handleDownload)
	api.HandleFunc("GET /servers/{id}/terminal", s.handleTerminal)
	api.HandleFunc("POST /rebalance", s.handleRebalance)

	api.HandleFunc("GET /projects", s.handleListProjects)
	api.HandleFunc("POST /projects", s.handleCreateProject)
	api.HandleFunc("GET /projects/{id}", s.handleGetProject)
	api.HandleFunc("PATCH /projects/{id}", s.handleUpdateProject)
	api.HandleFunc("DELETE /projects/{id}", s.handleDeleteProject)
	api.HandleFunc("POST /projects/{id}/deploy", s.handleDeploy)
	api.HandleFunc("POST /projects/{id}/rollback", s.handleProjectAction("rollback"))
	api.HandleFunc("POST /projects/{id}/restart", s.handleProjectAction("restart"))
	api.HandleFunc("POST /projects/{id}/stop", s.handleProjectAction("stop"))
	api.HandleFunc("POST /projects/{id}/start", s.handleProjectAction("start"))
	api.HandleFunc("GET /projects/{id}/deployments", s.handleDeployments)
	api.HandleFunc("GET /projects/{id}/logs", s.handleAppLogs)
	api.HandleFunc("POST /projects/{id}/source", s.handleUploadSource)
	api.HandleFunc("GET /projects/{id}/source", s.handleDownloadSource)
	api.HandleFunc("GET /deployments/{id}", s.handleDeployment)

	api.HandleFunc("GET /services/engines", s.handleServiceEngines)
	api.HandleFunc("GET /services", s.handleListServices)
	api.HandleFunc("POST /services", s.handleCreateService)
	api.HandleFunc("GET /services/{id}", s.handleGetService)
	api.HandleFunc("PATCH /services/{id}", s.handleRenameService)
	api.HandleFunc("DELETE /services/{id}", s.handleDeleteAddon)
	api.HandleFunc("POST /services/{id}/start", s.handleAddonAction("start"))
	api.HandleFunc("POST /services/{id}/stop", s.handleAddonAction("stop"))
	api.HandleFunc("POST /services/{id}/restart", s.handleAddonAction("restart"))
	api.HandleFunc("GET /services/{id}/logs", s.handleServiceLogs)
	api.HandleFunc("POST /services/{id}/backup", s.handleCreateBackup)
	api.HandleFunc("GET /services/{id}/backups", s.handleListBackups)
	api.HandleFunc("POST /services/{id}/restore", s.handleRestoreBackup)
	api.HandleFunc("GET /services/{id}/backups/{backupId}/download", s.handleAddonBackupDownload)

	api.HandleFunc("GET /addons/engines", s.handleListAddonEngines)
	api.HandleFunc("GET /addons", s.handleListAddons)
	api.HandleFunc("POST /addons", s.handleCreateAddon)
	api.HandleFunc("GET /addons/{id}", s.handleGetAddon)
	api.HandleFunc("PATCH /addons/{id}", s.handleUpdateAddon)
	api.HandleFunc("DELETE /addons/{id}", s.handleDeleteAddon)
	api.HandleFunc("POST /addons/{id}/start", s.handleAddonAction("start"))
	api.HandleFunc("POST /addons/{id}/stop", s.handleAddonAction("stop"))
	api.HandleFunc("POST /addons/{id}/restart", s.handleAddonAction("restart"))
	api.HandleFunc("POST /addons/{id}/bind", s.handleBindAddon)
	api.HandleFunc("DELETE /addons/{id}/bind/{projectId}", s.handleUnbindAddon)
	api.HandleFunc("POST /addons/{id}/standby", s.handleAddStandby)
	api.HandleFunc("POST /addons/{id}/promote", s.handlePromoteAddon)
	api.HandleFunc("DELETE /addons/{id}/placement/{serverId}", s.handleDeleteAddonPlacement)
	api.HandleFunc("GET /addons/{id}/connection", s.handleAddonConnection)
	api.HandleFunc("GET /addons/{id}/logs", s.handleAddonLogs)
	api.HandleFunc("POST /addons/{id}/query", s.handleAddonQuery)
	api.HandleFunc("POST /addons/{id}/backup", s.handleCreateBackup)
	api.HandleFunc("GET /addons/{id}/backups", s.handleListBackups)
	api.HandleFunc("POST /addons/{id}/restore", s.handleRestoreBackup)
	api.HandleFunc("PUT /addons/{id}/backups/{backupId}", s.handleAddonBackupUpload)
	api.HandleFunc("GET /addons/{id}/backups/{backupId}/download", s.handleAddonBackupDownload)

	api.HandleFunc("GET /alerts", s.handleListAlerts)
	api.HandleFunc("GET /alerts/count", s.handleAlertCount)
	api.HandleFunc("POST /alerts/{id}/ack", s.handleAckAlert)
	api.HandleFunc("GET /alert-rules", s.handleGetRules)
	api.HandleFunc("PUT /alert-rules", s.handlePutRules)
	api.HandleFunc("GET /notify", s.handleGetNotify)
	api.HandleFunc("PUT /notify", s.handlePutNotify)
	api.HandleFunc("POST /notify/test", s.handleTestNotify)

	s.mux.Handle("POST /api/login", http.HandlerFunc(s.handleLogin))
	s.mux.HandleFunc("GET /api/meta", s.handleMeta)
	s.mux.Handle("/api/", http.StripPrefix("/api", s.requireAuth(api)))
	s.mux.HandleFunc("/agent", s.handleAgent)
	s.mux.HandleFunc("/agent/data", s.handleAgentData)
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/download/agent", s.handleAgentDownload)
	s.mux.HandleFunc("/download/bootstrap.sh", s.handleBootstrap)
	s.mux.HandleFunc("/download/bootstrap-root.sh", s.handleBootstrapRoot)
	s.mux.HandleFunc("/download/bootstrap-debian.sh", s.handleBootstrapDebian)
	s.mux.Handle("/", s.hostGuard(webui.Handler()))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	servers, samples, err := s.st.Stats()
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":           true,
		"version":      config.Version,
		"uptimeSec":    int64(time.Since(s.started).Seconds()),
		"agentsOnline": s.hub.Count(),
		"servers":      servers,
		"metricRows":   samples,
	})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	publicURL, _ := s.Edge()
	writeJSON(w, 200, map[string]any{
		"version":   config.Version,
		"publicUrl": publicURL,
	})
}

// ---- auth ----

func (s *Server) sign(exp int64) string {
	mac := hmac.New(sha256.New, s.cfg.SessionKey)
	payload := "phleet|" + strconv.FormatInt(exp, 10)
	mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(v string) bool {
	parts := strings.Split(v, "|")
	if len(parts) != 3 || parts[0] != "phleet" {
		return false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	expect := s.sign(exp)
	return subtle.ConstantTimeCompare([]byte(expect), []byte(v)) == 1
}

func secureEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if v := r.Header.Get("X-Phleet-Token"); v != "" {
		return v
	}
	return r.URL.Query().Get("token")
}

func (s *Server) authed(r *http.Request) bool {
	if t := bearer(r); t != "" {
		if secureEq(t, s.cfg.AdminToken) || secureEq(t, s.cfg.AgentToken) {
			return true
		}
	}
	c, err := r.Cookie("phleet_session")
	if err != nil {
		return false
	}
	return s.validSession(c.Value)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body, 4096); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid body"})
		return
	}
	if !secureEq(strings.TrimSpace(body.Token), s.cfg.AdminToken) {
		time.Sleep(250 * time.Millisecond)
		writeJSON(w, 401, map[string]any{"error": "wrong token"})
		return
	}
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	c := &http.Cookie{
		Name:     "phleet_session",
		Value:    s.sign(exp),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(exp, 0),
		MaxAge:   30 * 24 * 3600,
	}
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		c.Secure = true
	}
	http.SetCookie(w, c)
	writeJSON(w, 200, map[string]any{"ok": true, "version": config.Version})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "phleet_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	publicURL, _ := s.Edge()
	writeJSON(w, 200, map[string]any{
		"authenticated": true,
		"version":       config.Version,
		"dataDir":       s.cfg.DataDir,
		"publicUrl":     publicURL,
		"agentsOnline":  s.hub.Count(),
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func readJSON(r *http.Request, dst any, max int64) error {
	if max <= 0 {
		max = 1 << 20
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func truthy(v string) bool {
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

// ---- events ----

func (s *Server) handleEventFacets(w http.ResponseWriter, r *http.Request) {
	actions, actors, err := s.st.EventFacets()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"actions": actions, "actors": actors})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	q := store.EventQuery{
		Action: strings.TrimSpace(query.Get("action")),
		Actor:  strings.TrimSpace(query.Get("actor")),
		Search: strings.TrimSpace(query.Get("q")),
	}
	if n, err := strconv.Atoi(query.Get("limit")); err == nil && n > 0 {
		q.Limit = n
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if n, err := strconv.ParseInt(query.Get("before"), 10, 64); err == nil && n > 0 {
		q.Before = n
	}

	evts, hasMore, err := s.st.ListEventsPage(q)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	// Older agent.offline rows were written without a detail. Resolve the
	// target server so the history is useful even before this fix.
	for i := range evts {
		if evts[i].Detail != "" || !strings.HasPrefix(evts[i].Action, "agent.") {
			continue
		}
		if srv, err := s.st.GetServer(evts[i].Target); err == nil && srv.Name != "" {
			evts[i].Detail = srv.Name
			if srv.Hostname != "" {
				evts[i].Detail += " (" + srv.Hostname + ")"
			}
		}
	}
	total, err := s.st.CountEvents(q)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	page := map[string]any{
		"limit":   q.Limit,
		"hasMore": hasMore,
		"total":   total,
	}
	if hasMore && len(evts) > 0 {
		page["nextBefore"] = evts[len(evts)-1].ID
	}
	resp := map[string]any{"events": evts, "page": page}
	writeJSON(w, 200, resp)
}
