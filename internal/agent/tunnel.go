//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"phleet/internal/tunnel"
)

// tunnelLoop maintains the data connection used to proxy HTTP traffic to apps.
func (a *Agent) tunnelLoop(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := a.runTunnel(ctx)
		if ctx.Err() != nil {
			return
		}
		a.log.Warn("data tunnel lost", "err", err, "retryIn", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (a *Agent) runTunnel(ctx context.Context) error {
	u, err := url.Parse(a.cfg.URL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimSuffix(u.Path, "/agent") + "/agent/data"
	u.RawQuery = ""
	ws, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient: a.httpClient,
		HTTPHeader: http.Header{
			"Authorization":   []string{"Bearer " + a.cfg.Token},
			"X-Phleet-Server": []string{a.cfg.ID},
		},
	})
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial tunnel: %w (http %d)", err, resp.StatusCode)
		}
		return fmt.Errorf("dial tunnel: %w", err)
	}
	ws.SetReadLimit(4 << 20)
	conn := tunnel.NewWSConn(ws)
	sess, err := yamux.Client(conn, tunnel.Config())
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "yamux")
		return err
	}
	go func() {
		<-ctx.Done()
		_ = sess.Close()
	}()
	a.setTunnelSession(sess)
	defer a.setTunnelSession(nil)
	a.log.Info("data tunnel connected")
	tunnel.ServeStreams(sess, a.portAllowed)
	_ = sess.Close()
	return errors.New("tunnel closed")
}

// portAllowed ensures the control plane can only reach supervised app ports.
func (a *Agent) portAllowed(port int) bool {
	a.appsMu.Lock()
	defer a.appsMu.Unlock()
	for _, p := range a.apps {
		p.mu.Lock()
		ok := p.spec.Port == port
		p.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}
