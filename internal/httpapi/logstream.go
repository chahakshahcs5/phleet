package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"phleet/internal/agentproto"
	"phleet/internal/model"
)

const logTailBytes = 64 << 10

func (s *Server) handleProjectLogStream(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	placements, err := s.st.ListProjectPlacements(p.ID)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	requested := strings.TrimSpace(r.URL.Query().Get("serverId"))
	target := model.Placement{}
	for _, pl := range placements {
		if requested != "" && pl.ServerID != requested {
			continue
		}
		if !s.hub.Online(pl.ServerID) {
			continue
		}
		if target.ServerID == "" || pl.Status == "running" {
			target = pl
		}
	}
	if target.ServerID == "" {
		apiErr(w, 409, errors.New("no online replica for this project"))
		return
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !s.originAllowed(origin, r.Host) {
		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "done")
	ws.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The browser has nothing to say on this socket, but a read loop is how we
	// notice it closing the logs tab.
	readErr := make(chan error, 1)
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				readErr <- err
				return
			}
		}
	}()

	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(ctx, 20*time.Second)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageText, b)
	}

	res, err := s.fetchAppLog(ctx, target.ServerID, p.Slug, 0, false)
	if err != nil {
		_ = write(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	if err := write(map[string]any{
		"type": "initial", "slug": p.Slug, "path": res.Path, "size": res.Size,
		"truncated": res.Truncated, "logs": string(res.Data),
	}); err != nil {
		return
	}
	if res.Size > 0 && res.Next == 0 {
		_ = write(map[string]any{"type": "error", "error": "the phone agent is too old for live logs; update it"})
		return
	}
	offset := res.Next

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-readErr:
			return
		case <-ticker.C:
			res, err := s.fetchAppLog(ctx, target.ServerID, p.Slug, offset, offset == 0)
			if err != nil {
				_ = write(map[string]any{"type": "error", "error": err.Error()})
				return
			}
			if res.Size > 0 && res.Next == 0 {
				_ = write(map[string]any{"type": "error", "error": "the phone agent is too old for live logs; update it"})
				return
			}
			if res.Rotated {
				if err := write(map[string]any{"type": "reset", "size": res.Size}); err != nil {
					return
				}
			}
			if len(res.Data) > 0 {
				if err := write(map[string]any{"type": "chunk", "data": string(res.Data)}); err != nil {
					return
				}
			}
			offset = res.Next
		}
	}
}

func (s *Server) fetchAppLog(ctx context.Context, serverID, slug string, offset int64, fromStart bool) (agentproto.AppLogsResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	env, err := s.hub.Call(callCtx, serverID, agentproto.TypeAppLogs, agentproto.AppLogsRequest{
		Slug: slug, Bytes: logTailBytes, Offset: offset, FromStart: fromStart,
	})
	if err != nil {
		return agentproto.AppLogsResponse{}, err
	}
	var res agentproto.AppLogsResponse
	if err := json.Unmarshal(env.Data, &res); err != nil {
		return agentproto.AppLogsResponse{}, err
	}
	return res, nil
}
