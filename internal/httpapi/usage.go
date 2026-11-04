package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"phleet/internal/agentproto"
	"phleet/internal/model"
)

func (s *Server) measureAddonUsage(ctx context.Context, a model.Addon) (int64, error) {
	if a.DataDir == "" {
		return 0, errors.New("this addon has no data directory")
	}
	if !s.hub.Online(a.PinnedServer) {
		return 0, errors.New("the phone hosting this database is offline")
	}
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := "du -sm " + shellQuote(a.DataDir) + " 2>/dev/null | cut -f1"
	env, err := s.hub.Call(callCtx, a.PinnedServer, agentproto.TypeExec, agentproto.ExecRequest{Command: cmd, TimeoutSec: 20})
	if err != nil {
		return 0, err
	}
	var res agentproto.ExecResponse
	_ = json.Unmarshal(env.Data, &res)
	used, err := strconv.ParseInt(strings.TrimSpace(res.Output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not measure data usage: %q", strings.TrimSpace(res.Output))
	}
	return used, nil
}

// checkAddonUsage measures the data directory, stores the result and raises a
// soft-quota alert when the advisory size hint is exceeded.
func (s *Server) checkAddonUsage(ctx context.Context, a model.Addon) (int64, error) {
	used, err := s.measureAddonUsage(ctx, a)
	if err != nil {
		return 0, err
	}
	_ = s.st.SetAddonUsage(a.ID, used, time.Now().Unix())
	key := "quota:" + a.ID
	if a.SizeLimitMB > 0 && used > int64(a.SizeLimitMB) {
		msg := fmt.Sprintf("%s uses %d MB, above its %d MB hint", a.Name, used, a.SizeLimitMB)
		if used > int64(a.SizeLimitMB)*2 {
			s.fireAlert(key, "addon_quota", "critical", a.ID, msg)
		} else {
			s.fireAlert(key, "addon_quota", "warning", a.ID, msg)
		}
	} else {
		s.resolveAlert(key)
	}
	return used, nil
}

func (s *Server) handleAddonUsage(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	used, err := s.checkAddonUsage(r.Context(), a)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	limit := int64(a.SizeLimitMB)
	writeJSON(w, 200, map[string]any{
		"usedMb": used, "limitMb": limit, "over": limit > 0 && used > limit,
	})
}

func (s *Server) quotaLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkQuotas()
		}
	}
}

func (s *Server) checkQuotas() {
	addons, err := s.st.ListAddons()
	if err != nil {
		return
	}
	for _, a := range addons {
		if a.SizeLimitMB <= 0 || !s.hub.Online(a.PinnedServer) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = s.checkAddonUsage(ctx, a)
		cancel()
	}
}
