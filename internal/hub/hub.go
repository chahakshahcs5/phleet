// Package hub tracks live agent connections and routes messages between the
// control plane and agents.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/hashicorp/yamux"

	"phleet/internal/agentproto"
)

var (
	ErrOffline   = errors.New("server is offline")
	ErrTimeout   = errors.New("agent did not respond in time")
	ErrNoSession = errors.New("terminal session not found")
)

type PTYEvent struct {
	Data     []byte
	Exit     bool
	ExitCode int
}

type Conn struct {
	ServerID string
	ws       *websocket.Conn

	writeMu sync.Mutex
	reqMu   sync.Mutex
	pending map[string]chan agentproto.Envelope

	ptyMu sync.Mutex
	pty   map[string]chan PTYEvent

	closeOnce sync.Once
	done      chan struct{}
}

func NewConn(serverID string, ws *websocket.Conn) *Conn {
	return newConn(serverID, ws)
}

func newConn(serverID string, ws *websocket.Conn) *Conn {
	return &Conn{
		ServerID: serverID,
		ws:       ws,
		pending:  map[string]chan agentproto.Envelope{},
		pty:      map[string]chan PTYEvent{},
		done:     make(chan struct{}),
	}
}

func (c *Conn) write(env agentproto.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (c *Conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.ws.Close(websocket.StatusNormalClosure, "closing")
	})
}

type Callbacks struct {
	OnHello       func(agentproto.Hello) error
	OnHeartbeat   func(serverID string, hb agentproto.Heartbeat)
	OnDisconnect  func(serverID string)
	OnDeployEvent func(serverID string, ev agentproto.DeployEvent)
	OnAddonEvent  func(serverID string, ev agentproto.AddonEvent)
}

type Hub struct {
	mu        sync.RWMutex
	conns     map[string]*Conn
	callbacks Callbacks

	tunMu   sync.Mutex
	tunnels map[string]*yamux.Session
}

func New(cb Callbacks) *Hub {
	return &Hub{conns: map[string]*Conn{}, callbacks: cb, tunnels: map[string]*yamux.Session{}}
}

// Serve runs the read loop for an agent connection. It returns when the
// connection dies. hello must already have been read by the caller.
func (h *Hub) Serve(c *Conn, hb agentproto.Hello) {
	h.mu.Lock()
	if old, ok := h.conns[c.ServerID]; ok && old != c {
		old.close()
	}
	h.conns[c.ServerID] = c
	h.mu.Unlock()

	// The agent has a 90s read deadline; without traffic from us it would
	// reconnect every 90 seconds. A tiny ping keeps the session alive.
	go h.keepalive(c)

	if h.callbacks.OnHello != nil {
		if err := h.callbacks.OnHello(hb); err != nil {
			_ = c.write(agentproto.Envelope{Type: "error", Error: "registration failed: " + err.Error()})
			c.close()
			h.remove(c)
			return
		}
	}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		typ, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		h.handle(c, env)
	}
	c.close()
	h.remove(c)
}

func (h *Hub) remove(c *Conn) {
	h.mu.Lock()
	wasCurrent := false
	if cur, ok := h.conns[c.ServerID]; ok && cur == c {
		delete(h.conns, c.ServerID)
		wasCurrent = true
	}
	h.mu.Unlock()
	if wasCurrent {
		h.closeTunnel(c.ServerID)
	}
	// Fail pending requests and terminate PTY subscribers.
	c.reqMu.Lock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		select {
		case ch <- agentproto.Envelope{ReplyTo: id, Error: "agent disconnected"}:
		default:
		}
	}
	c.reqMu.Unlock()
	c.ptyMu.Lock()
	for sid, ch := range c.pty {
		delete(c.pty, sid)
		select {
		case ch <- PTYEvent{Exit: true}:
		default:
		}
	}
	c.ptyMu.Unlock()
	// A replacement connection may already be live. Only the connection that
	// is still current represents a real disconnect; otherwise closing the old
	// socket would flash the phone offline and trigger a false offline alert.
	if wasCurrent && h.callbacks.OnDisconnect != nil {
		h.callbacks.OnDisconnect(c.ServerID)
	}
}

func (h *Hub) handle(c *Conn, env agentproto.Envelope) {
	switch {
	case env.Type == agentproto.TypeHeartbeat:
		var hb agentproto.Heartbeat
		if err := json.Unmarshal(env.Data, &hb); err == nil && h.callbacks.OnHeartbeat != nil {
			h.callbacks.OnHeartbeat(c.ServerID, hb)
		}
	case env.Type == agentproto.TypePTYData:
		var d agentproto.PTYData
		if err := json.Unmarshal(env.Data, &d); err == nil {
			h.pushPTY(c, d.Session, PTYEvent{Data: d.Data})
		}
	case env.Type == agentproto.TypePTYExit:
		var d agentproto.PTYExit
		if err := json.Unmarshal(env.Data, &d); err == nil {
			h.pushPTY(c, d.Session, PTYEvent{Exit: true, ExitCode: d.ExitCode})
		}
	case env.Type == agentproto.TypeDeployEvent:
		var ev agentproto.DeployEvent
		if err := json.Unmarshal(env.Data, &ev); err == nil && h.callbacks.OnDeployEvent != nil {
			h.callbacks.OnDeployEvent(c.ServerID, ev)
		}
	case env.Type == agentproto.TypeAddonEvent:
		var ev agentproto.AddonEvent
		if err := json.Unmarshal(env.Data, &ev); err == nil && h.callbacks.OnAddonEvent != nil {
			h.callbacks.OnAddonEvent(c.ServerID, ev)
		}
	case env.ReplyTo != "":
		c.reqMu.Lock()
		ch, ok := c.pending[env.ReplyTo]
		if ok {
			delete(c.pending, env.ReplyTo)
		}
		c.reqMu.Unlock()
		if ok {
			select {
			case ch <- env:
			default:
			}
		}
	}
}

func (h *Hub) pushPTY(c *Conn, session string, ev PTYEvent) {
	c.ptyMu.Lock()
	ch, ok := c.pty[session]
	c.ptyMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- ev:
	default:
		// Slow consumer: drop a chunk rather than block the agent read loop.
	}
}

func (h *Hub) Get(serverID string) (*Conn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.conns[serverID]
	return c, ok
}

func (h *Hub) Online(serverID string) bool {
	_, ok := h.Get(serverID)
	return ok
}

// Kick closes a live agent connection, used when a server is removed.
func (h *Hub) Kick(serverID string) {
	if c, ok := h.Get(serverID); ok {
		c.close()
	}
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

func (h *Hub) IDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.conns))
	for id := range h.conns {
		out = append(out, id)
	}
	return out
}

func (h *Hub) Send(serverID, typ string, data any) error {
	c, ok := h.Get(serverID)
	if !ok {
		return ErrOffline
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.write(agentproto.Envelope{Type: typ, ID: uuid.NewString(), Data: raw})
}

// Call performs a request/response round trip with an agent.
func (h *Hub) Call(ctx context.Context, serverID, typ string, data any) (agentproto.Envelope, error) {
	c, ok := h.Get(serverID)
	if !ok {
		return agentproto.Envelope{}, ErrOffline
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return agentproto.Envelope{}, err
	}
	id := uuid.NewString()
	ch := make(chan agentproto.Envelope, 1)
	c.reqMu.Lock()
	c.pending[id] = ch
	c.reqMu.Unlock()
	defer func() {
		c.reqMu.Lock()
		delete(c.pending, id)
		c.reqMu.Unlock()
	}()

	if err := c.write(agentproto.Envelope{Type: typ, ID: id, Data: raw}); err != nil {
		return agentproto.Envelope{}, err
	}
	select {
	case env := <-ch:
		if env.Error != "" {
			return env, errors.New(env.Error)
		}
		return env, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return agentproto.Envelope{}, ErrTimeout
		}
		return agentproto.Envelope{}, ctx.Err()
	}
}

// SubscribePTY registers a terminal subscriber. The returned cancel function
// must be called when the UI disconnects.
func (h *Hub) SubscribePTY(serverID, session string) (<-chan PTYEvent, func(), error) {
	c, ok := h.Get(serverID)
	if !ok {
		return nil, nil, ErrOffline
	}
	ch := make(chan PTYEvent, 256)
	c.ptyMu.Lock()
	c.pty[session] = ch
	c.ptyMu.Unlock()
	cancel := func() {
		c.ptyMu.Lock()
		if cur, ok := c.pty[session]; ok && cur == ch {
			delete(c.pty, session)
		}
		c.ptyMu.Unlock()
	}
	return ch, cancel, nil
}

func (h *Hub) OpenPTY(serverID string, req agentproto.PTYOpen) error {
	return h.Send(serverID, agentproto.TypePTYOpen, req)
}

func (h *Hub) WritePTY(serverID, session string, data []byte) error {
	return h.Send(serverID, agentproto.TypePTYData, agentproto.PTYData{Session: session, Data: data})
}

func (h *Hub) ResizePTY(serverID, session string, cols, rows uint16) error {
	return h.Send(serverID, agentproto.TypePTYResize, agentproto.PTYResize{Session: session, Cols: cols, Rows: rows})
}

func (h *Hub) ClosePTY(serverID, session string) error {
	return h.Send(serverID, agentproto.TypePTYClose, agentproto.PTYSession{Session: session})
}

func (h *Hub) CloseAll() {
	h.mu.RLock()
	conns := make([]*Conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
		c.close()
	}
}

// ---- data tunnels ----

func (h *Hub) SetTunnel(serverID string, sess *yamux.Session) {
	h.tunMu.Lock()
	old := h.tunnels[serverID]
	h.tunnels[serverID] = sess
	h.tunMu.Unlock()
	if old != nil && old != sess {
		_ = old.Close()
	}
}

func (h *Hub) ClearTunnel(serverID string, sess *yamux.Session) {
	h.tunMu.Lock()
	if cur, ok := h.tunnels[serverID]; ok && cur == sess {
		delete(h.tunnels, serverID)
	}
	h.tunMu.Unlock()
}

func (h *Hub) Tunnel(serverID string) (*yamux.Session, bool) {
	h.tunMu.Lock()
	defer h.tunMu.Unlock()
	s, ok := h.tunnels[serverID]
	return s, ok
}

func (h *Hub) closeTunnel(serverID string) {
	h.tunMu.Lock()
	s := h.tunnels[serverID]
	delete(h.tunnels, serverID)
	h.tunMu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}

func (h *Hub) keepalive(c *Conn) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			if err := c.write(agentproto.Envelope{Type: "ping"}); err != nil {
				return
			}
		}
	}
}
