package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"phleet/internal/agentproto"
)

// handleServiceLogs returns the supervised process log (stdout/stderr) for a
// service. Addon logs are provisioning/backup events; service users expect the
// app's own output, which the agent keeps under the app log path.
func (s *Server) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil || !a.Service {
		apiErr(w, 404, errors.New("service not found"))
		return
	}
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil || len(placements) == 0 {
		apiErr(w, 409, errors.New("service has no placement"))
		return
	}
	target := ""
	for _, pl := range placements {
		if pl.Role != "primary" && pl.Role != "" {
			continue
		}
		if s.hub.Online(pl.ServerID) {
			target = pl.ServerID
			break
		}
	}
	if target == "" {
		apiErr(w, 409, errors.New("service phone is offline"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, target, agentproto.TypeAppLogs, agentproto.AppLogsRequest{Slug: a.Slug, Bytes: 64 << 10})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.AppLogsResponse
	if err := json.Unmarshal(env.Data, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"logs":      string(res.Data),
		"path":      res.Path,
		"size":      res.Size,
		"truncated": res.Truncated,
		"serverId":  target,
	})
}
