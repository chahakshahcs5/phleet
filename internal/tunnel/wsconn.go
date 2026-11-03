// Package tunnel carries HTTP traffic to agents over a yamux session layered
// on a WebSocket. The agent dials out; the control plane opens streams.
package tunnel

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// WSConn adapts a WebSocket into a net.Conn so yamux can run on top of it.
// Each WS message is a chunk of the byte stream.
type WSConn struct {
	ws *websocket.Conn

	readMu  sync.Mutex
	buf     []byte
	readBy  atomic.Int64 // UnixNano deadline, 0 = none
	writeBy atomic.Int64

	closeOnce sync.Once
}

func NewWSConn(ws *websocket.Conn) *WSConn {
	return &WSConn{ws: ws}
}

func (c *WSConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.buf) == 0 {
		ctx, cancel := c.deadlineContext(true)
		typ, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			return 0, err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		c.buf = data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *WSConn) Write(p []byte) (int, error) {
	ctx, cancel := c.deadlineContext(false)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *WSConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.ws.Close(websocket.StatusNormalClosure, "closed")
	})
	return err
}

func (c *WSConn) LocalAddr() net.Addr  { return wsAddr("agent") }
func (c *WSConn) RemoteAddr() net.Addr { return wsAddr("control-plane") }

func (c *WSConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	c.SetWriteDeadline(t)
	return nil
}

func (c *WSConn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		c.readBy.Store(0)
	} else {
		c.readBy.Store(t.UnixNano())
	}
	return nil
}

func (c *WSConn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.writeBy.Store(0)
	} else {
		c.writeBy.Store(t.UnixNano())
	}
	return nil
}

func (c *WSConn) deadlineContext(read bool) (context.Context, context.CancelFunc) {
	var v int64
	if read {
		v = c.readBy.Load()
	} else {
		v = c.writeBy.Load()
	}
	if v == 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithDeadline(context.Background(), time.Unix(0, v))
}

type wsAddr string

func (a wsAddr) Network() string { return "ws" }
func (a wsAddr) String() string  { return string(a) }

var ErrNoTunnel = errors.New("no tunnel for server")
