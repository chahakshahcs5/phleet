//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
		start := time.Now()
		err := a.runTunnel(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 60*time.Second {
			backoff = time.Second
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
	// The data tunnel must use the token issued in hello.ack, not the shared
	// enrollment token left over on first install. Wait briefly for the control
	// loop to process the ack before dialing.
	select {
	case <-a.helloAck:
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}
	u, err := url.Parse(a.cfg.URL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimSuffix(u.Path, "/agent") + "/agent/data"
	u.RawQuery = ""
	ws, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
		HTTPClient: a.httpClient,
		HTTPHeader: http.Header{
			"Authorization":   []string{"Bearer " + a.currentToken()},
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
	tunnel.ServeStreamsWithFiles(sess, a.portAllowed, a.serveFileStream)
	_ = sess.Close()
	return errors.New("tunnel closed")
}

// serveFileStream streams one file from the sandboxed home directory. The
// control plane writes the path in a RequestHeader and reads the framed size
// followed by raw bytes, so a large download never has to be base64-encoded
// or held in memory on either side.
func (a *Agent) serveFileStream(stream *yamux.Stream, req tunnel.RequestHeader) error {
	fail := func(status int, msg string) error {
		_ = tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: status, Error: msg})
		return errors.New(msg)
	}
	abs, err := a.resolve(req.Path)
	if err != nil {
		return fail(http.StatusForbidden, err.Error())
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fail(http.StatusNotFound, err.Error())
	}
	if info.IsDir() {
		return fail(http.StatusBadRequest, "path is a directory")
	}
	f, err := os.Open(abs)
	if err != nil {
		return fail(http.StatusInternalServerError, err.Error())
	}
	defer f.Close()
	_ = stream.SetDeadline(time.Time{})
	if err := tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: http.StatusOK, ContentLength: info.Size()}); err != nil {
		return err
	}
	_, err = io.CopyN(stream, f, info.Size())
	return err
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
