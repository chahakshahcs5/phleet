package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Edge state is reloaded by the control plane when cloudflare.json changes, so
// `phleet setup-cloudflare` takes effect without restarting phleetd.
type edgeState struct {
	mu        sync.RWMutex
	publicURL string
	fleetHost string
}

func (s *Server) SetEdge(publicURL, fleetHost string) {
	s.edge.mu.Lock()
	s.edge.publicURL = publicURL
	s.edge.fleetHost = fleetHost
	s.edge.mu.Unlock()
}

func (s *Server) Edge() (publicURL, fleetHost string) {
	s.edge.mu.RLock()
	defer s.edge.mu.RUnlock()
	return s.edge.publicURL, s.edge.fleetHost
}

// hostGuard keeps unknown public subdomains from serving the panel. Loopback
// and private-network addresses are always allowed so LAN access keeps working.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, fleetHost := s.Edge()
		if fleetHost == "" {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if strings.EqualFold(host, fleetHost) || host == "localhost" || isPrivateHost(host) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "no app is routed to %s\n", host)
	})
}

func isPrivateHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
