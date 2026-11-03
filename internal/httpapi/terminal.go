package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/hub"
)

type termMsg struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Code int    `json:"code,omitempty"`
	B64  string `json:"b64,omitempty"`
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.hub.Online(id) {
		apiErr(w, 409, hub.ErrOffline)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "done")
	ws.SetReadLimit(1 << 20)

	session := uuid.NewString()
	events, cancel, err := s.hub.SubscribePTY(id, session)
	if err != nil {
		_ = ws.Close(websocket.StatusInternalError, "agent offline")
		return
	}
	defer cancel()
	defer s.hub.ClosePTY(id, session)

	if err := s.hub.OpenPTY(id, agentproto.PTYOpen{Session: session, Cols: 120, Rows: 32}); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "could not open shell")
		return
	}
	_ = s.st.AddEvent("ui", "terminal.open", id, session)

	ctx, stop := context.WithCancel(r.Context())
	defer stop()

	readErr := make(chan error, 1)
	go func() {
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var m termMsg
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			switch m.Type {
			case "input":
				b, err := base64.StdEncoding.DecodeString(m.Data)
				if err == nil && len(b) > 0 {
					_ = s.hub.WritePTY(id, session, b)
				}
			case "resize":
				if m.Cols > 0 && m.Rows > 0 {
					_ = s.hub.ResizePTY(id, session, m.Cols, m.Rows)
				}
			case "close":
				readErr <- io_EOF
				return
			}
		}
	}()

	write := func(m termMsg) error {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(ctx, 20*time.Second)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageText, b)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-readErr:
			if err != nil && !errors.Is(err, io_EOF) {
				_ = ws.Close(websocket.StatusNormalClosure, "closed")
			}
			return
		case ev := <-events:
			if ev.Exit {
				_ = write(termMsg{Type: "exit", Code: ev.ExitCode})
				return
			}
			if err := write(termMsg{Type: "output", B64: base64.StdEncoding.EncodeToString(ev.Data)}); err != nil {
				return
			}
		}
	}
}

var io_EOF = errors.New("eof")
