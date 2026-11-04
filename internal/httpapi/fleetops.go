package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"phleet/internal/agentproto"
)

type fleetResult struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name,omitempty"`
	OK       bool   `json:"ok"`
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetServer(id); err != nil {
		apiErr(w, 404, err)
		return
	}
	if !s.hub.Online(id) {
		apiErr(w, 409, errors.New("the phone is offline"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if _, err := s.hub.Call(ctx, id, agentproto.TypeAgentUpdate, agentproto.Empty{}); err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.AddEvent("ui", "agent.update", id, "manual check")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleUpdateAllAgents(w http.ResponseWriter, r *http.Request) {
	servers, err := s.st.ListServers()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	results := []fleetResult{}
	for _, srv := range servers {
		res := fleetResult{ServerID: srv.ID, Name: srv.Name}
		if !s.hub.Online(srv.ID) {
			res.Error = "offline"
			results = append(results, res)
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		_, err := s.hub.Call(ctx, srv.ID, agentproto.TypeAgentUpdate, agentproto.Empty{})
		cancel()
		if err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
			res.Output = "update check requested"
		}
		results = append(results, res)
	}
	_ = s.st.AddEvent("ui", "agent.update_all", "", fmt.Sprintf("%d phones", len(results)))
	writeJSON(w, 200, map[string]any{"results": results})
}

func (s *Server) handleBatchExec(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerIDs  []string `json:"serverIds"`
		Command    string   `json:"command"`
		TimeoutSec int      `json:"timeoutSec"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	command := strings.TrimSpace(body.Command)
	if command == "" {
		apiErr(w, 400, errors.New("command is required"))
		return
	}
	if body.TimeoutSec <= 0 {
		body.TimeoutSec = 30
	}
	if body.TimeoutSec > 300 {
		body.TimeoutSec = 300
	}
	ids := body.ServerIDs
	if len(ids) == 0 {
		servers, err := s.st.ListServers()
		if err != nil {
			apiErr(w, 500, err)
			return
		}
		for _, srv := range servers {
			if s.hub.Online(srv.ID) {
				ids = append(ids, srv.ID)
			}
		}
	}
	results := []fleetResult{}
	for _, id := range ids {
		srv, err := s.st.GetServer(id)
		if err != nil {
			results = append(results, fleetResult{ServerID: id, Error: "not found"})
			continue
		}
		res := fleetResult{ServerID: id, Name: srv.Name}
		if !s.hub.Online(id) {
			res.Error = "offline"
			results = append(results, res)
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(body.TimeoutSec+10)*time.Second)
		env, err := s.hub.Call(ctx, id, agentproto.TypeExec, agentproto.ExecRequest{
			Command: command, TimeoutSec: body.TimeoutSec,
		})
		cancel()
		if err != nil {
			res.Error = err.Error()
		} else {
			var er agentproto.ExecResponse
			_ = json.Unmarshal(env.Data, &er)
			res.OK = er.ExitCode == 0
			res.Output = er.Output
			res.ExitCode = er.ExitCode
		}
		results = append(results, res)
	}
	_ = s.st.AddEvent("ui", "fleet.exec", "", fmt.Sprintf("%s on %d phone(s)", command, len(results)))
	writeJSON(w, 200, map[string]any{"results": results})
}

func (s *Server) handleRetireServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	srv, err := s.st.GetServer(id)
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		PurgeAddons bool `json:"purgeAddons"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &body, 1<<16); err != nil {
			apiErr(w, 400, err)
			return
		}
	}
	addonPlacements, err := s.st.ListAddonPlacementsForServer(id)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if len(addonPlacements) > 0 && !body.PurgeAddons {
		names := []string{}
		for _, pl := range addonPlacements {
			if a, err := s.st.GetAddon(pl.AddonID); err == nil {
				names = append(names, a.Name)
			} else {
				names = append(names, pl.AddonID)
			}
		}
		apiErr(w, 409, fmt.Errorf("this phone hosts %s; pass purgeAddons=true to delete them and all of their data",
			strings.Join(names, ", ")))
		return
	}
	// Stop project replicas while the connection is still usable.
	if placements, err := s.st.ListPlacementsForServer(id); err == nil {
		for _, pl := range placements {
			if !s.hub.Online(id) {
				break
			}
			p, err := s.st.GetProject(pl.ProjectID)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			_, _ = s.hub.Call(ctx, id, agentproto.TypeAppControl,
				agentproto.AppControlRequest{Slug: p.Slug, Action: "delete"})
			cancel()
		}
	}
	if body.PurgeAddons {
		for _, pl := range addonPlacements {
			a, err := s.st.GetAddon(pl.AddonID)
			if err != nil {
				continue
			}
			if err := s.deleteAddon(r.Context(), a, true); err != nil {
				apiErr(w, 500, fmt.Errorf("delete %s: %w", a.Name, err))
				return
			}
		}
	}
	_ = s.st.DeleteTasksForServer(id)
	s.hub.Kick(id)
	if err := s.st.DeleteServer(id); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "server.retire", id, serverLabel(srv))
	writeJSON(w, 200, map[string]any{"ok": true})
}
