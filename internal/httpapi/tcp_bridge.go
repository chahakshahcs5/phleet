package httpapi

import (
	"fmt"
	"net"
	"sync"
	"time"

	"phleet/internal/engines"
	"phleet/internal/model"
	"phleet/internal/tunnel"
)

// tcpBridge exposes an addon on the control plane's loopback so ordinary
// clients (psql, redis-cli, an S3 tool) can use a phone-hosted service without
// Cloudflare, a client-side cloudflared, or an inbound port on the phone.
type tcpBridge struct {
	s *Server

	mu        sync.Mutex
	listeners map[string]*bridgeListener
}

type bridgeListener struct {
	addonID string
	port    int
	ln      net.Listener
	done    chan struct{}
}

func newTCPBridge(s *Server) *tcpBridge {
	return &tcpBridge{s: s, listeners: map[string]*bridgeListener{}}
}

func (b *tcpBridge) active(addonID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.listeners[addonID]
	return ok
}

func (b *tcpBridge) syncAll() {
	addons, err := b.s.st.ListAddons()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, a := range addons {
		if eng, ok := engines.Get(a.Engine); !ok || eng.Embedded || a.BridgePort <= 0 {
			continue
		}
		seen[a.ID] = true
		if a.BridgePort > 0 {
			_ = b.start(a)
		}
	}
	b.mu.Lock()
	stale := make([]string, 0)
	for id := range b.listeners {
		if !seen[id] {
			stale = append(stale, id)
		}
	}
	b.mu.Unlock()
	for _, id := range stale {
		b.stop(id)
	}
}

func (b *tcpBridge) start(a model.Addon) error {
	if a.BridgePort <= 0 {
		return nil
	}
	b.mu.Lock()
	if _, ok := b.listeners[a.ID]; ok {
		b.mu.Unlock()
		return nil
	}
	addr := fmt.Sprintf("127.0.0.1:%d", a.BridgePort)
	if b.s.cfg.BridgeLAN {
		addr = fmt.Sprintf("0.0.0.0:%d", a.BridgePort)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		b.mu.Unlock()
		return fmt.Errorf("bridge listen %s: %w", addr, err)
	}
	l := &bridgeListener{addonID: a.ID, port: a.BridgePort, ln: ln, done: make(chan struct{})}
	b.listeners[a.ID] = l
	b.mu.Unlock()
	b.s.log.Info("addon bridge listening", "addon", a.Slug, "port", a.BridgePort, "lan", b.s.cfg.BridgeLAN)
	go b.accept(l)
	return nil
}

func (b *tcpBridge) stop(addonID string) {
	b.mu.Lock()
	l := b.listeners[addonID]
	delete(b.listeners, addonID)
	b.mu.Unlock()
	if l != nil {
		_ = l.ln.Close()
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
}

func (b *tcpBridge) stopAll() {
	b.mu.Lock()
	all := make([]*bridgeListener, 0, len(b.listeners))
	for id, l := range b.listeners {
		all = append(all, l)
		delete(b.listeners, id)
	}
	b.mu.Unlock()
	for _, l := range all {
		_ = l.ln.Close()
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
}

func (b *tcpBridge) accept(l *bridgeListener) {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			b.s.log.Warn("bridge accept", "addon", l.addonID, "err", err)
			return
		}
		go b.s.serveBridgeConn(l.addonID, conn)
	}
}

func (s *Server) serveBridgeConn(addonID string, conn net.Conn) {
	defer conn.Close()
	a, err := s.st.GetAddon(addonID)
	if err != nil {
		return
	}
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		return
	}
	target := model.AddonPlacement{}
	for _, pl := range placements {
		if (pl.Role == "primary" || pl.Role == "") && pl.Desired != "stopped" {
			target = pl
			break
		}
	}
	if target.ServerID == "" {
		return
	}
	sess, ok := s.hub.Tunnel(target.ServerID)
	if !ok {
		return
	}
	up, err := tunnel.ProxyTCP(sess, a.Port, "", "tcp")
	if err != nil {
		s.log.Warn("bridge connect failed", "addon", a.Slug, "err", err)
		return
	}
	s.log.Debug("bridge connection", "addon", a.Slug, "from", conn.RemoteAddr())
	tunnel.Bridge(conn, up)
}

func (s *Server) bridgeSyncSoon(a model.Addon) {
	go func() {
		time.Sleep(250 * time.Millisecond)
		if s.bridges != nil {
			_ = s.bridges.start(a)
		}
	}()
}
