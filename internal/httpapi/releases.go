package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/model"
)

var releaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func (s *Server) pickProjectServer(p model.Project, requested string) string {
	requested = strings.TrimSpace(requested)
	placements, err := s.st.ListProjectPlacements(p.ID)
	if err != nil {
		return ""
	}
	target := ""
	for _, pl := range placements {
		if requested != "" && pl.ServerID != requested {
			continue
		}
		if !s.hub.Online(pl.ServerID) {
			continue
		}
		if target == "" || pl.Status == "running" {
			target = pl.ServerID
		}
	}
	return target
}

func (s *Server) handleProjectReleases(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if p.Kind == "stack" {
		apiErr(w, 400, errors.New("open a service in the stack to see its releases"))
		return
	}
	serverID := s.pickProjectServer(p, r.URL.Query().Get("serverId"))
	if serverID == "" {
		apiErr(w, 409, errors.New("no online replica for this project"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, serverID, agentproto.TypeAppReleases, agentproto.AppReleasesRequest{Slug: p.Slug})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.AppReleasesResponse
	if err := json.Unmarshal(env.Data, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"serverId": serverID, "releases": res.Releases})
}

// handleRollback handles both the original "roll back to the previous release
// on every replica" action and a specific release on one phone.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerID string `json:"serverId"`
		Release  string `json:"release"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &body, 1<<16); err != nil {
			apiErr(w, 400, err)
			return
		}
	}
	if body.ServerID == "" && body.Release == "" {
		s.handleProjectAction("rollback")(w, r)
		return
	}
	if body.ServerID == "" {
		apiErr(w, 400, errors.New("serverId is required when choosing a release"))
		return
	}
	if body.Release != "" && !releaseNamePattern.MatchString(body.Release) {
		apiErr(w, 400, errors.New("invalid release name"))
		return
	}
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if p.Kind == "stack" {
		apiErr(w, 400, errors.New("open a service in the stack to roll back a release"))
		return
	}
	placements, err := s.st.ListProjectPlacements(p.ID)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	found := false
	currentRelease := ""
	for _, pl := range placements {
		if pl.ServerID == body.ServerID {
			found = true
			currentRelease = pl.Release
			break
		}
	}
	if !found {
		apiErr(w, 404, errors.New("this project has no replica on that phone"))
		return
	}
	if !s.hub.Online(body.ServerID) {
		apiErr(w, 409, errors.New("the phone is offline"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	_, err = s.hub.Call(ctx, body.ServerID, agentproto.TypeAppControl, agentproto.AppControlRequest{
		Slug: p.Slug, Action: "rollback", Release: body.Release,
	})
	now := time.Now().Unix()
	dep := model.Deployment{
		ID: uuid.NewString(), ProjectID: p.ID, ServerID: body.ServerID,
		Status: "rolled_back", Release: body.Release, StartedAt: now, FinishedAt: now,
		Detail: "manual rollback",
	}
	if body.Release != "" {
		dep.Detail = "manual rollback to " + body.Release
	}
	if err != nil {
		dep.Status = "failed"
		dep.Detail = "manual rollback failed: " + err.Error()
		_ = s.st.CreateDeployment(dep)
		apiErr(w, 502, err)
		return
	}
	_ = s.st.CreateDeployment(dep)
	if body.Release != "" {
		_ = s.st.UpdatePlacementStatusFor(p.ID, body.ServerID, "running", 0, 0, "", body.Release)
		_ = s.st.AddEvent("ui", "project.rollback", p.ID, "release "+body.Release)
	} else {
		_ = s.st.UpdatePlacementStatusFor(p.ID, body.ServerID, "running", 0, 0, "", currentRelease)
		_ = s.st.AddEvent("ui", "project.rollback", p.ID, "previous release")
	}
	writeJSON(w, 200, map[string]any{"ok": true, "deployment": dep})
}
