//go:build linux

package agent

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/hashicorp/yamux"

	"phleet/internal/agentproto"
	"phleet/internal/tunnel"
)

// relayListener is a loopback facade on an app phone. Apps connect to
// 127.0.0.1:<LocalPort>; each connection is carried over the agent's existing
// outbound yamux session to the control plane, which forwards it to the phone
// that actually hosts the addon. No inbound port and no Cloudflare rule.
type relayListener struct {
	addonID   string
	localPort int

	mu   sync.Mutex
	ln   net.Listener
	done chan struct{}
}

func (a *Agent) setTunnelSession(sess *yamux.Session) {
	a.tunMu.Lock()
	a.tunSess = sess
	a.tunMu.Unlock()
}

func (a *Agent) tunnelSession() *yamux.Session {
	a.tunMu.Lock()
	defer a.tunMu.Unlock()
	return a.tunSess
}

func (a *Agent) relayControl(req agentproto.AddonRelaySpec) error {
	switch req.Action {
	case "start":
		if req.LocalPort <= 0 || req.LocalPort > 65535 {
			return errors.New("invalid relay port")
		}
		return a.startRelay(req)
	case "stop":
		a.stopRelay(req.AddonID)
		return nil
	default:
		return fmt.Errorf("unknown relay action %q", req.Action)
	}
}

func (a *Agent) startRelay(req agentproto.AddonRelaySpec) error {
	a.relayMu.Lock()
	cur := a.relays[req.AddonID]
	if cur != nil && cur.localPort == req.LocalPort {
		a.relayMu.Unlock()
		return nil
	}
	if cur != nil {
		delete(a.relays, req.AddonID)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(req.LocalPort))
	if err != nil {
		// Keep the old listener if the new port is unavailable.
		if cur != nil {
			a.relays[req.AddonID] = cur
		}
		a.relayMu.Unlock()
		return fmt.Errorf("relay listen: %w", err)
	}
	l := &relayListener{addonID: req.AddonID, localPort: req.LocalPort, ln: ln, done: make(chan struct{})}
	a.relays[req.AddonID] = l
	a.relayMu.Unlock()
	if cur != nil {
		cur.close()
	}
	a.log.Info("addon relay listening", "addon", req.AddonID, "port", req.LocalPort)
	go a.acceptRelay(l)
	return nil
}

func (a *Agent) stopRelay(addonID string) {
	a.relayMu.Lock()
	l := a.relays[addonID]
	delete(a.relays, addonID)
	a.relayMu.Unlock()
	if l != nil {
		l.close()
		a.log.Info("addon relay stopped", "addon", addonID, "port", l.localPort)
	}
}

func (a *Agent) stopAllRelays() {
	a.relayMu.Lock()
	all := make([]*relayListener, 0, len(a.relays))
	for _, l := range a.relays {
		all = append(all, l)
	}
	a.relays = map[string]*relayListener{}
	a.relayMu.Unlock()
	for _, l := range all {
		l.close()
	}
}

func (l *relayListener) close() {
	l.mu.Lock()
	if l.ln != nil {
		_ = l.ln.Close()
		l.ln = nil
	}
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	l.mu.Unlock()
}

func (a *Agent) acceptRelay(l *relayListener) {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
			}
			a.log.Warn("relay accept", "addon", l.addonID, "err", err)
			return
		}
		go a.serveRelayConn(l, conn)
	}
}

func (a *Agent) serveRelayConn(l *relayListener, conn net.Conn) {
	defer conn.Close()
	sess := a.tunnelSession()
	if sess == nil {
		return
	}
	stream, err := tunnel.OpenRelay(sess, l.addonID)
	if err != nil {
		a.log.Warn("relay open failed", "addon", l.addonID, "err", err)
		return
	}
	tunnel.Bridge(conn, stream)
}
