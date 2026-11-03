package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"phleet/internal/model"
	"phleet/internal/store"
	"phleet/internal/tunnel"
)

// handleAgentData accepts the yamux-over-WebSocket data connection. The agent
// dials it after the control connection is up.
func (s *Server) handleAgentData(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	if tok == "" || (!secureEq(tok, s.cfg.AgentToken) && !secureEq(tok, s.cfg.AdminToken)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	serverID := strings.TrimSpace(r.Header.Get("X-Phleet-Server"))
	if serverID == "" {
		http.Error(w, "missing X-Phleet-Server", http.StatusBadRequest)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	ws.SetReadLimit(4 << 20)
	conn := tunnel.NewWSConn(ws)
	sess, err := yamux.Server(conn, tunnel.Config())
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "yamux")
		return
	}
	s.hub.SetTunnel(serverID, sess)
	s.log.Info("data tunnel connected", "server", serverID)
	go s.serveRelayStreams(serverID, sess)
	<-sess.CloseChan()
	s.hub.ClearTunnel(serverID, sess)
	s.log.Info("data tunnel closed", "server", serverID)
}

// route intercepts requests addressed to a project hostname before they reach
// the panel mux. Everything else (panel, API, agent endpoints) is unchanged.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if s.proxyStorage(w, r) {
		return
	}
	if s.proxyProject(w, r) {
		return
	}
	s.mux.ServeHTTP(w, r)
}

// storageMaxBody keeps an S3 request under Cloudflare's 100 MB per-request
// cap while still streaming it straight to the phone. -storage-max-mb raises
// it for direct/LAN access.
const defaultStorageMaxBody = 96 << 20

func (s *Server) storageLimit() int64 {
	if s.cfg.StorageMaxBody > 0 {
		return s.cfg.StorageMaxBody
	}
	return defaultStorageMaxBody
}

func writeS3Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message><RequestId>phleet</RequestId></Error>`, code, message)
}

// proxyStorage routes object-storage hostnames to an S3 placement. S3 is
// HTTP, so it uses the same tunnel and wildcard DNS as projects.
func (s *Server) proxyStorage(w http.ResponseWriter, r *http.Request) bool {
	host := normalizeHost(r.Host)
	if host == "" || host == "localhost" || isPrivateHost(host) {
		return false
	}
	addon, err := s.st.GetAddonByHostname(host)
	if err != nil || addon.Hostname == "" {
		return false
	}
	if addon.Service {
		return s.proxyServiceAddon(w, r, addon)
	}
	limit := s.storageLimit()
	if r.ContentLength > limit {
		writeS3Error(w, http.StatusRequestEntityTooLarge, "EntityTooLarge",
			fmt.Sprintf("a single request is limited to %d MB; use a multipart upload", limit>>20))
		return true
	}
	placements, err := s.st.ListAddonPlacements(addon.ID)
	if err != nil {
		writeS3Error(w, http.StatusBadGateway, "ServiceUnavailable", "no placement for this bucket")
		return true
	}
	target := ""
	for _, pl := range placements {
		if pl.Desired == "stopped" {
			continue
		}
		if (pl.Role == "primary" || pl.Role == "") && s.hub.Online(pl.ServerID) {
			if srv, err := s.st.GetServer(pl.ServerID); err == nil && srv.Drain {
				continue
			}
			target = pl.ServerID
			break
		}
	}
	if target == "" {
		writeS3Error(w, http.StatusBadGateway, "ServiceUnavailable", "object storage is not reachable")
		return true
	}
	sess, ok := s.hub.Tunnel(target)
	if !ok {
		writeS3Error(w, http.StatusBadGateway, "ServiceUnavailable", "storage tunnel is not connected")
		return true
	}
	if tok := bearer(r); tok != "" && (secureEq(tok, s.cfg.AdminToken) || secureEq(tok, s.cfg.AgentToken)) {
		r.Header.Del("Authorization")
		r.Header.Del("X-Phleet-Token")
	}
	if err := tunnel.ProxyHTTPOpts(w, r, sess, addon.Port, "phleet_session", tunnel.ProxyOptions{
		MaxBody: limit, Stream: true,
	}); err != nil {
		s.log.Warn("storage proxy failed", "addon", addon.Slug, "server", target, "err", err)
	}
	return true
}

// proxyServiceAddon routes a self-hosted service hostname to its pinned phone.
// Services are single-process apps, so unlike projects there is no replica
// selection: use the primary placement and proxy through its data tunnel.
func (s *Server) proxyServiceAddon(w http.ResponseWriter, r *http.Request, addon model.Addon) bool {
	placements, err := s.st.ListAddonPlacements(addon.ID)
	if err != nil || len(placements) == 0 {
		http.Error(w, "service is not provisioned", http.StatusBadGateway)
		return true
	}
	target := ""
	for _, pl := range placements {
		if pl.Desired == "stopped" {
			continue
		}
		if pl.Role != "primary" && pl.Role != "" {
			continue
		}
		if !s.hub.Online(pl.ServerID) {
			continue
		}
		if srv, err := s.st.GetServer(pl.ServerID); err == nil && srv.Drain {
			continue
		}
		target = pl.ServerID
		break
	}
	if target == "" {
		http.Error(w, "service phone is offline", http.StatusBadGateway)
		return true
	}
	sess, ok := s.hub.Tunnel(target)
	if !ok {
		http.Error(w, "service tunnel is not connected", http.StatusBadGateway)
		return true
	}
	if tok := bearer(r); tok != "" && (secureEq(tok, s.cfg.AdminToken) || secureEq(tok, s.cfg.AgentToken)) {
		r.Header.Del("Authorization")
		r.Header.Del("X-Phleet-Token")
	}
	if err := tunnel.ProxyHTTP(w, r, sess, addon.Port, "phleet_session"); err != nil {
		s.log.Warn("service proxy failed", "service", addon.Slug, "server", target, "err", err)
	}
	return true
}

func normalizeHost(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

func (s *Server) proxyProject(w http.ResponseWriter, r *http.Request) bool {
	host := normalizeHost(r.Host)
	if host == "" || host == "localhost" || isPrivateHost(host) {
		return false
	}
	_, fleetHost := s.Edge()
	if fleetHost != "" && strings.EqualFold(host, fleetHost) {
		return false
	}
	project, err := s.st.GetProjectByHostname(host)
	if err != nil {
		if err != store.ErrNotFound {
			s.log.Warn("project lookup", "host", host, "err", err)
		}
		return false
	}
	placements, err := s.st.ListProjectPlacements(project.ID)
	if err != nil || len(placements) == 0 {
		http.Error(w, "no placement for this app", http.StatusBadGateway)
		return true
	}

	type candidate struct {
		port int
		srv  string
	}
	healthy := []candidate{}
	fallback := []candidate{}
	for _, pl := range placements {
		if pl.Desired == "stopped" {
			continue
		}
		if srv, err := s.st.GetServer(pl.ServerID); err == nil && srv.Drain {
			continue
		}
		c := candidate{port: pl.Port, srv: pl.ServerID}
		if pl.Status == "running" {
			healthy = append(healthy, c)
		} else {
			fallback = append(fallback, c)
		}
	}
	candidates := append(healthy, fallback...)
	if len(candidates) == 0 {
		http.Error(w, "no healthy replica is reachable", http.StatusBadGateway)
		return true
	}

	if tok := bearer(r); tok != "" && (secureEq(tok, s.cfg.AdminToken) || secureEq(tok, s.cfg.AgentToken)) {
		r.Header.Del("Authorization")
		r.Header.Del("X-Phleet-Token")
	}
	start := s.nextReplica(project.ID, len(candidates))
	for i := 0; i < len(candidates); i++ {
		c := candidates[(start+i)%len(candidates)]
		sess, ok := s.hub.Tunnel(c.srv)
		if !ok {
			continue
		}
		if err := tunnel.ProxyHTTP(w, r, sess, c.port, "phleet_session"); err != nil {
			s.log.Warn("proxy failed", "project", project.Slug, "server", c.srv, "err", err)
		}
		return true
	}
	http.Error(w, "replica tunnels are not connected", http.StatusBadGateway)
	return true
}

func (s *Server) nextReplica(projectID string, n int) int {
	if n <= 0 {
		return 0
	}
	s.rrMu.Lock()
	defer s.rrMu.Unlock()
	i := s.rr[projectID]
	s.rr[projectID] = (i + 1) % n
	return i % n
}
