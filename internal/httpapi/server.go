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
	"net/url"
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
	panel   http.Handler
	started time.Time
	edge    edgeState

	rrMu   sync.Mutex
	rr     map[string]int
	portMu sync.Mutex

	orphanMu sync.Mutex
	orphans  map[string]time.Time

	recMu sync.Mutex
	recAt map[string]time.Time

	offlineMu     sync.Mutex
	offlineTimers map[string]*time.Timer

	deviceMu      sync.Mutex
	pendingDevice map[string]string

	apiUseMu sync.Mutex
	apiUse   map[string]time.Time

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
		pendingDevice: map[string]string{},
		apiUse:        map[string]time.Time{},
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
	s.panel = s.guard(securityHeaders(s.mux))
	s.warnSlugCollisions()
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

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; connect-src 'self' ws: wss:; "+
				"frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		// API and agent responses carry tokens, credentials and tenant data.
		// Never let a browser or intermediary cache them.
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") ||
			r.URL.Path == "/agent" || strings.HasPrefix(r.URL.Path, "/agent/") ||
			r.URL.Path == "/download/agent" {
			h.Set("Cache-Control", "no-store")
		}
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// guard rejects cross-site state-changing requests. SameSite=Lax does not help
// here: every project and service lives on a sibling subdomain of the panel,
// which the browser treats as same-site. The Origin check is therefore the
// boundary that keeps a compromised app from driving the panel.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site == "cross-site" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-site request rejected"})
			return
		}
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !s.originAllowed(origin, r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "origin is not allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed accepts the panel's own host (direct or through the edge) and
// loopback/LAN development origins. It deliberately does not accept other
// subdomains of the panel's domain: those are user workloads.
func (s *Server) originAllowed(origin, requestHost string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	oh := normalizeHost(u.Host)
	rh := normalizeHost(requestHost)
	if oh == "" || rh == "" {
		return false
	}
	if oh == rh {
		return true
	}
	publicURL, fleetHost := s.Edge()
	if fleetHost != "" {
		if oh == normalizeHost(fleetHost) {
			return true
		}
		if rh == normalizeHost(fleetHost) && publicURL != "" {
			if pu, err := url.Parse(publicURL); err == nil && normalizeHost(pu.Host) == oh {
				return true
			}
		}
	}
	// Local UI development and LAN access. A public sibling subdomain is not
	// loopback or private, so this does not weaken the production boundary.
	if (oh == "localhost" || isPrivateHost(oh)) && (rh == "localhost" || isPrivateHost(rh)) {
		return true
	}
	return false
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
	api.HandleFunc("GET /jobs/{id}", s.handleGetJob)
	api.HandleFunc("GET /api-tokens", s.handleListAPITokens)
	api.HandleFunc("POST /api-tokens", s.handleCreateAPIToken)
	api.HandleFunc("DELETE /api-tokens/{id}", s.handleDeleteAPIToken)
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

	// These three endpoints are also called by the agent itself (source
	// download, backup upload, restore download), so they must accept the
	// agent token. Everything else under /api is panel-only.
	s.mux.Handle("GET /api/projects/{id}/source",
		s.requirePanelOrAgent(http.HandlerFunc(s.handleDownloadSource)))
	s.mux.Handle("PUT /api/addons/{id}/backups/{backupId}",
		s.requirePanelOrAgent(http.HandlerFunc(s.handleAddonBackupUpload)))
	s.mux.Handle("GET /api/addons/{id}/backups/{backupId}/download",
		s.requirePanelOrAgent(http.HandlerFunc(s.handleAddonBackupDownload)))

	s.mux.Handle("/api/", http.StripPrefix("/api", s.requirePanelAuth(api)))
	s.mux.HandleFunc("/agent", s.handleAgent)
	s.mux.HandleFunc("/agent/data", s.handleAgentData)
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.Handle("GET /metrics", s.requirePanelAuth(http.HandlerFunc(s.handlePrometheus)))
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
	return ""
}

func (s *Server) adminTokenAuthed(r *http.Request) bool {
	t := bearer(r)
	return t != "" && secureEq(t, s.cfg.AdminToken)
}

func (s *Server) agentTokenAuthed(r *http.Request) bool {
	t := bearer(r)
	return t != "" && (secureEq(t, s.cfg.AgentToken) || secureEq(t, s.cfg.AdminToken))
}

func tokenHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func (s *Server) touchAPIToken(id string) {
	s.apiUseMu.Lock()
	last := s.apiUse[id]
	if time.Since(last) < time.Minute {
		s.apiUseMu.Unlock()
		return
	}
	s.apiUse[id] = time.Now()
	s.apiUseMu.Unlock()
	go func() { _ = s.st.TouchAPIToken(id) }()
}

// agentServerAuthed verifies the per-device token for one server. An empty
// hash means the phone has not enrolled yet and still uses the shared token.
func (s *Server) agentServerAuthed(tok, serverID string) bool {
	if tok == "" || serverID == "" {
		return false
	}
	if secureEq(tok, s.cfg.AdminToken) {
		return true
	}
	hash, err := s.st.GetServerDeviceHash(serverID)
	if err == nil && hash != "" {
		return secureEq(tokenHash(tok), hash)
	}
	return secureEq(tok, s.cfg.AgentToken)
}

// agentHelloAuth authorizes a hello and, for a new or legacy phone, returns a
// freshly generated device token that the caller must send in hello.ack.
func (s *Server) agentHelloAuth(tok, serverID string, canEnroll bool) (bool, string) {
	if secureEq(tok, s.cfg.AdminToken) {
		return true, ""
	}
	hash, err := s.st.GetServerDeviceHash(serverID)
	if err == nil && hash != "" {
		return secureEq(tokenHash(tok), hash), ""
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, ""
	}
	// If the hash write failed after we handed out a token, let that token
	// finish enrolling instead of locking the phone out.
	s.deviceMu.Lock()
	pending := s.pendingDevice[serverID]
	s.deviceMu.Unlock()
	if pending != "" {
		if secureEq(tok, pending) {
			return true, ""
		}
		// A reconnect raced the first enrollment. Hand out the same pending
		// token rather than replacing it under the other connection.
		if canEnroll && secureEq(tok, s.cfg.AgentToken) {
			return true, pending
		}
	}
	// No enrollment yet. Only clients that understand hello.ack may enroll;
	// an older agent keeps using the shared token so an upgrade cannot lock it
	// out of its own phone.
	if secureEq(tok, s.cfg.AgentToken) {
		if canEnroll {
			return true, randomHex(32)
		}
		return true, ""
	}
	return false, ""
}

func (s *Server) setPendingDeviceToken(serverID, tok string) {
	s.deviceMu.Lock()
	s.pendingDevice[serverID] = tok
	s.deviceMu.Unlock()
}

func (s *Server) adoptPendingDeviceToken(serverID string) {
	s.deviceMu.Lock()
	tok, ok := s.pendingDevice[serverID]
	if ok {
		delete(s.pendingDevice, serverID)
	}
	s.deviceMu.Unlock()
	if ok {
		if err := s.st.SetServerDeviceHash(serverID, tokenHash(tok)); err != nil {
			s.log.Warn("store device token", "server", serverID, "err", err)
			// Keep the pending token so the next hello can finish enrollment
			// instead of locking the phone out.
			s.setPendingDeviceToken(serverID, tok)
		}
	}
}

// agentHTTPAuthed covers the three HTTP endpoints an agent reaches after
// enrollment. Device tokens must name their server; the legacy shared token is
// still accepted when no server header is present (or the phone has not
// enrolled yet).
func (s *Server) agentHTTPAuthed(r *http.Request) bool {
	if s.panelRole(r) == "admin" {
		return true
	}
	tok := bearer(r)
	if tok == "" {
		return false
	}
	serverID := strings.TrimSpace(r.Header.Get("X-Phleet-Server"))
	if serverID == "" {
		return secureEq(tok, s.cfg.AgentToken)
	}
	return s.agentServerAuthed(tok, serverID)
}

func (s *Server) panelRole(r *http.Request) string {
	if s.adminTokenAuthed(r) {
		return "admin"
	}
	if tok := bearer(r); tok != "" {
		if t, err := s.st.GetAPITokenByHash(tokenHash(tok)); err == nil {
			s.touchAPIToken(t.ID)
			if t.Role == "admin" {
				return "admin"
			}
			return "viewer"
		}
	}
	c, err := r.Cookie("phleet_session")
	if err != nil {
		return ""
	}
	if s.validSession(c.Value) {
		return "admin"
	}
	return ""
}

func (s *Server) panelAuthed(r *http.Request) bool {
	return s.panelRole(r) != ""
}

func (s *Server) requirePanelAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := s.panelRole(r)
		if role == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if role != "admin" {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "read-only API token"})
				return
			}
			if !viewerPathAllowed(r.URL.Path) {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "endpoint requires an admin token"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requirePanelOrAgent is only used by the handful of endpoints that an agent
// must reach over HTTP. It still accepts the panel session so browser
// downloads keep working.
func (s *Server) requirePanelOrAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.agentHTTPAuthed(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
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
	// Requiring JSON forces a CORS preflight for cross-origin callers and
	// closes the text/plain CSRF hole. Upload endpoints use multipart or octet
	// streams and do not call this helper.
	ct := strings.TrimSpace(strings.ToLower(r.Header.Get("Content-Type")))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct != "application/json" {
		return errors.New("content type must be application/json")
	}
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
