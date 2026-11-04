package httpapi

import (
	"context"
	"net/http"
	"sort"
	"time"

	"phleet/internal/agentproto"
	"phleet/internal/model"
	"phleet/internal/placement"
)

// StartBackground launches the failover reconciler and the alert evaluator.
func (s *Server) StartBackground(ctx context.Context) {
	s.bridges.syncAll()
	go s.reconcileLoop(ctx)
	go s.alertLoop(ctx)
	go s.tunnel.Run(ctx)
	go s.backupLoop(ctx)
	go s.taskSyncLoop(ctx)
	go s.requestLogLoop(ctx)
	go s.drillLoop(ctx)
	go s.uptimeLoop(ctx)
	go s.quotaLoop(ctx)
}

func (s *Server) triggerReconcile() {
	select {
	case s.reconcileCh <- struct{}{}:
	default:
	}
}

func (s *Server) reconcileLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	// Let agents settle after a control-plane restart.
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	s.reconcileAll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcileAll()
		case <-s.reconcileCh:
			s.reconcileAll()
		}
	}
}

func (s *Server) reconcileAll() {
	projects, err := s.st.ListProjects()
	if err != nil {
		s.log.Warn("reconcile: list projects", "err", err)
		return
	}
	for _, p := range projects {
		s.expireStaleDeployments(p)
		s.reconcileProject(p)
	}
	if addons, err := s.st.ListAddons(); err == nil {
		for _, a := range addons {
			s.reconcileAddon(a)
		}
	}
	if s.bridges != nil {
		s.bridges.syncAll()
	}
}

// expireStaleDeployments unblocks scheduling when an agent vanished mid-deploy.
func (s *Server) expireStaleDeployments(p model.Project) {
	list, err := s.st.ListDeployments(p.ID, 20)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-20 * time.Minute).Unix()
	for _, d := range list {
		if isTerminalDeploy(d.Status) || d.StartedAt > cutoff {
			continue
		}
		_ = s.st.FinishDeployment(d.ID, "failed", d.Release, "deployment timed out")
		_ = s.st.UpdatePlacementStatusFor(p.ID, d.ServerID, "failed", 0, 0, "deployment timed out", d.Release)
		_ = s.st.AddEvent("scheduler", "deploy.timeout", p.ID, d.ID)
	}
}

// reconcileProject keeps the desired number of running replicas on eligible
// phones, replacing drained/offline placements once a replacement is healthy.
func (s *Server) reconcileProject(p model.Project) {
	if p.Kind == "stack" {
		return
	}
	placements, err := s.st.ListProjectPlacements(p.ID)
	if err != nil {
		return
	}
	desired := p.Replicas
	if desired < 1 {
		desired = 1
	}
	allStopped := len(placements) > 0
	for _, pl := range placements {
		if pl.Desired != "stopped" {
			allStopped = false
			break
		}
	}
	if allStopped {
		return
	}
	if len(placements) == 0 && !projectDeployable(p) {
		return
	}
	if s.hasInflight(p.ID) {
		return
	}

	states, byID, err := s.serverStates()
	if err != nil {
		return
	}
	sel := selectorFor(p)
	eligible := func(serverID string) bool {
		srv, ok := byID[serverID]
		if !ok {
			return false
		}
		return placement.Eligible(statesByID(states, serverID), sel, p.PinnedServer) && !srv.Drain
	}

	active := make([]model.Placement, 0, len(placements))
	stale := make([]model.Placement, 0)
	for _, pl := range placements {
		if eligible(pl.ServerID) {
			active = append(active, pl)
		} else {
			stale = append(stale, pl)
		}
	}
	runningActive := 0
	for _, pl := range active {
		if pl.Status == "running" {
			runningActive++
		}
	}

	if len(active) < desired {
		if !s.coolingDown(p.ID) {
			if s.addReplicas(p, states, active, desired) {
				s.markReconciled(p.ID)
			}
			return
		}
	}

	// Only remove old placements once replacements are actually serving.
	if runningActive >= desired {
		for _, pl := range stale {
			s.removePlacement(p, pl)
		}
		if len(active) > desired {
			sort.Slice(active, func(i, j int) bool { return active[i].UpdatedAt < active[j].UpdatedAt })
			for _, pl := range active[desired:] {
				s.removePlacement(p, pl)
			}
		}
	}
}

func statesByID(states []placement.Server, id string) placement.Server {
	for _, st := range states {
		if st.ID == id {
			return st
		}
	}
	return placement.Server{ID: id}
}

// coolingDown prevents a failing deploy from being retried every tick.
func (s *Server) coolingDown(projectID string) bool {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	last, ok := s.recAt[projectID]
	return ok && time.Since(last) < 2*time.Minute
}

func (s *Server) markReconciled(projectID string) {
	s.recMu.Lock()
	s.recAt[projectID] = time.Now()
	s.recMu.Unlock()
}

func (s *Server) addReplicas(p model.Project, states []placement.Server, active []model.Placement, desired int) bool {
	have := map[string]bool{}
	for _, pl := range active {
		have[pl.ServerID] = true
	}
	plan := placement.Plan(states, placement.Need{
		Selector: selectorFor(p), Pinned: p.PinnedServer, Replicas: desired, Existing: have,
	})
	if len(plan) == 0 {
		return false
	}
	deployed := 0
	for _, serverID := range plan {
		port := 0
		var err error
		locked := p.Kind != "worker"
		if locked {
			s.portMu.Lock()
			port, err = s.allocatePort(serverID)
			if err != nil {
				s.portMu.Unlock()
				s.log.Warn("reconcile: allocate port", "server", serverID, "err", err)
				continue
			}
		}
		pl := model.Placement{
			ProjectID: p.ID, ServerID: serverID, Port: port, Status: "pending", Desired: "running",
			UpdatedAt: time.Now().Unix(),
		}
		err = s.st.UpsertPlacement(pl)
		if locked {
			s.portMu.Unlock()
		}
		if err != nil {
			s.log.Warn("reconcile: add placement", "err", err)
			continue
		}
		_ = s.st.AddEvent("scheduler", "placement.add", p.ID, p.Name+" → "+serverID)
		go s.syncTasksForServer(serverID)
		if !projectDeployable(p) {
			continue
		}
		if _, err := s.startDeployment(p, pl, ""); err != nil {
			s.log.Warn("reconcile: deploy replica", "project", p.Slug, "server", serverID, "err", err)
			continue
		}
		deployed++
	}
	if deployed > 0 {
		s.log.Info("reconciled replicas", "project", p.Slug, "added", deployed)
	}
	return true
}

func (s *Server) removePlacement(p model.Project, pl model.Placement) {
	if s.hub.Online(pl.ServerID) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = s.hub.Call(ctx, pl.ServerID, agentproto.TypeAppControl,
				agentproto.AppControlRequest{Slug: p.Slug, Action: "delete"})
		}()
	}
	if err := s.st.DeletePlacement(p.ID, pl.ServerID); err != nil {
		s.log.Warn("reconcile: delete placement", "err", err)
		return
	}
	_ = s.st.AddEvent("scheduler", "placement.remove", p.ID, p.Name+" ← "+pl.ServerID)
	go s.syncTasksForServer(pl.ServerID)
}

// handleRebalance lets the UI ask for an immediate reconciliation.
func (s *Server) handleRebalance(w http.ResponseWriter, r *http.Request) {
	s.reconcileAll()
	writeJSON(w, 200, map[string]any{"ok": true})
}
