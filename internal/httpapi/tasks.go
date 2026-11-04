package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/cron"
	"phleet/internal/model"
)

const (
	defaultTaskTimeout = 300
	maxTaskTimeout     = 3600
)

type taskView struct {
	model.Task
	NextRun     int64  `json:"nextRun"`
	ServerName  string `json:"serverName,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	Available   bool   `json:"available"`
}

type taskBody struct {
	Name       string `json:"name"`
	ServerID   string `json:"serverId"`
	ProjectID  string `json:"projectId"`
	Command    string `json:"command"`
	Cron       string `json:"cron"`
	TimeoutSec int    `json:"timeoutSec"`
	Enabled    *bool  `json:"enabled"`
}

type taskPatch struct {
	Name       *string `json:"name"`
	ServerID   *string `json:"serverId"`
	ProjectID  *string `json:"projectId"`
	Command    *string `json:"command"`
	Cron       *string `json:"cron"`
	TimeoutSec *int    `json:"timeoutSec"`
	Enabled    *bool   `json:"enabled"`
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.st.ListTasks()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	admin := s.panelRole(r) == "admin"
	views := make([]taskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, s.buildTaskView(task, admin))
	}
	writeJSON(w, 200, map[string]any{"tasks": views})
}

func (s *Server) buildTaskView(task model.Task, admin bool) taskView {
	view := taskView{Task: task}
	// The list stays small; full output is fetched from the phone on demand.
	view.LastOutput = ""
	if !admin {
		view.Command = ""
	}
	view.Available = s.taskAvailable(task)
	if srv, err := s.st.GetServer(task.ServerID); err == nil {
		view.ServerName = srv.Name
	}
	if task.ProjectID != "" {
		if p, err := s.st.GetProject(task.ProjectID); err == nil {
			view.ProjectName = p.Name
		}
	}
	if task.Enabled {
		if sched, err := cron.Parse(task.Cron); err == nil {
			if next := sched.Next(time.Now()); !next.IsZero() {
				view.NextRun = next.Unix()
			}
		}
	}
	return view
}

func (s *Server) taskAvailable(task model.Task) bool {
	if task.ProjectID == "" {
		_, err := s.st.GetServer(task.ServerID)
		return err == nil
	}
	if _, err := s.st.GetProject(task.ProjectID); err != nil {
		return false
	}
	placements, err := s.st.ListProjectPlacements(task.ProjectID)
	if err != nil {
		return false
	}
	for _, pl := range placements {
		if pl.ServerID == task.ServerID {
			return true
		}
	}
	return false
}

func (s *Server) validateTask(task model.Task) (int, error) {
	if strings.TrimSpace(task.Name) == "" {
		return 0, errors.New("task name is required")
	}
	task.ServerID = strings.TrimSpace(task.ServerID)
	if task.ServerID == "" {
		return 0, errors.New("a phone is required")
	}
	srv, err := s.st.GetServer(task.ServerID)
	if err != nil {
		return 0, errors.New("selected phone was not found")
	}
	task.Command = strings.TrimSpace(task.Command)
	if task.Command == "" {
		return 0, errors.New("command is required")
	}
	task.Cron = strings.TrimSpace(task.Cron)
	if _, err := cron.Parse(task.Cron); err != nil {
		return 0, fmt.Errorf("schedule: %w", err)
	}
	task.ProjectID = strings.TrimSpace(task.ProjectID)
	if task.ProjectID != "" {
		p, err := s.st.GetProject(task.ProjectID)
		if err != nil {
			return 0, errors.New("selected project was not found")
		}
		if p.Kind == "stack" {
			return 0, errors.New("pick a service in the stack, not the stack itself")
		}
		placements, err := s.st.ListProjectPlacements(p.ID)
		if err != nil {
			return 0, err
		}
		found := false
		for _, pl := range placements {
			if pl.ServerID == task.ServerID {
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("%s is not placed on %s; pick a phone that runs the project", p.Name, srv.Name)
		}
	}
	if task.TimeoutSec <= 0 {
		task.TimeoutSec = defaultTaskTimeout
	}
	if task.TimeoutSec > maxTaskTimeout {
		task.TimeoutSec = maxTaskTimeout
	}
	return task.TimeoutSec, nil
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var body taskBody
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	task := model.Task{
		ID: uuid.NewString(), Name: strings.TrimSpace(body.Name),
		ServerID: strings.TrimSpace(body.ServerID), ProjectID: strings.TrimSpace(body.ProjectID),
		Command: strings.TrimSpace(body.Command), Cron: strings.TrimSpace(body.Cron),
		TimeoutSec: body.TimeoutSec, Enabled: enabled, CreatedAt: time.Now().Unix(),
	}
	timeout, err := s.validateTask(task)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	task.TimeoutSec = timeout
	if err := s.st.CreateTask(task); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "task.create", task.ID, task.Name+" on "+task.ServerID)
	go s.syncTasksForServer(task.ServerID)
	writeJSON(w, 201, map[string]any{"task": s.buildTaskView(task, true)})
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.st.GetTask(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body taskPatch
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	oldServer := task.ServerID
	if body.Name != nil {
		task.Name = strings.TrimSpace(*body.Name)
	}
	if body.ServerID != nil {
		task.ServerID = strings.TrimSpace(*body.ServerID)
	}
	if body.ProjectID != nil {
		task.ProjectID = strings.TrimSpace(*body.ProjectID)
	}
	if body.Command != nil {
		task.Command = strings.TrimSpace(*body.Command)
	}
	if body.Cron != nil {
		task.Cron = strings.TrimSpace(*body.Cron)
	}
	if body.TimeoutSec != nil {
		task.TimeoutSec = *body.TimeoutSec
	}
	if body.Enabled != nil {
		task.Enabled = *body.Enabled
	}
	timeout, err := s.validateTask(task)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	task.TimeoutSec = timeout
	if err := s.st.UpdateTask(task); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "task.update", task.ID, task.Name+" on "+task.ServerID)
	go s.syncTasksForServer(oldServer)
	if task.ServerID != oldServer {
		go s.syncTasksForServer(task.ServerID)
	}
	writeJSON(w, 200, map[string]any{"task": s.buildTaskView(task, true)})
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.st.GetTask(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if err := s.st.DeleteTask(task.ID); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "task.delete", task.ID, task.Name)
	go s.syncTasksForServer(task.ServerID)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.st.GetTask(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if !s.hub.Online(task.ServerID) {
		apiErr(w, 409, errors.New("the phone is offline"))
		return
	}
	// Make sure the agent has the current definition before asking it to run.
	s.syncTasksForServer(task.ServerID)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := s.hub.Call(ctx, task.ServerID, agentproto.TypeTaskRun, agentproto.TaskRunRequest{TaskID: task.ID}); err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.TouchTaskRun(task.ID, time.Now().Unix(), "running", 0, "", "")
	_ = s.st.AddEvent("ui", "task.run", task.ID, task.Name)
	writeJSON(w, 202, map[string]any{"ok": true})
}

func (s *Server) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	task, err := s.st.GetTask(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if !s.hub.Online(task.ServerID) {
		apiErr(w, 409, errors.New("the phone is offline; logs are stored on the device"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, task.ServerID, agentproto.TypeTaskLogs, agentproto.TaskLogsRequest{
		TaskID: task.ID, Bytes: 128 << 10,
	})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.TaskLogsResponse
	if err := json.Unmarshal(env.Data, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"taskId": res.TaskID, "path": res.Path, "size": res.Size,
		"truncated": res.Truncated, "logs": string(res.Data),
	})
}

// syncTasksForServer pushes the full schedule for one phone. The agent keeps
// executing it while the panel is unreachable, so this is a snapshot, not a
// per-run RPC.
func (s *Server) syncTasksForServer(serverID string) {
	if !s.hub.Online(serverID) {
		return
	}
	tasks, err := s.st.ListTasksForServer(serverID)
	if err != nil {
		s.log.Warn("task sync: list", "server", serverID, "err", err)
		return
	}
	specs := make([]agentproto.TaskSpec, 0, len(tasks))
	for _, task := range tasks {
		spec := agentproto.TaskSpec{
			ID: task.ID, Name: task.Name, Command: task.Command, Cron: task.Cron,
			TimeoutSec: task.TimeoutSec, Enabled: task.Enabled,
		}
		if task.ProjectID != "" {
			p, err := s.st.GetProject(task.ProjectID)
			if err != nil {
				continue
			}
			if !s.projectPlacedOn(p.ID, serverID) {
				// The project moved away; remove the task from this phone.
				continue
			}
			spec.Project = true
			spec.Slug = p.Slug
			spec.Env = s.taskEnvFor(p, serverID)
		}
		specs = append(specs, spec)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := s.hub.Call(ctx, serverID, agentproto.TypeTaskSync, agentproto.TaskSyncRequest{Tasks: specs}); err != nil {
		s.log.Warn("task sync", "server", serverID, "err", err)
		return
	}
	s.log.Debug("tasks synced", "server", serverID, "count", len(specs))
}

func (s *Server) projectPlacedOn(projectID, serverID string) bool {
	placements, err := s.st.ListProjectPlacements(projectID)
	if err != nil {
		return false
	}
	for _, pl := range placements {
		if pl.ServerID == serverID {
			return true
		}
	}
	return false
}

func (s *Server) syncTasksForProject(projectID string) {
	// Sync every phone that currently hosts a task for this project, even if
	// the project has moved away: that is how the old phone learns to drop it.
	tasks, err := s.st.ListTasksForProject(projectID)
	if err != nil {
		return
	}
	servers := map[string]bool{}
	for _, task := range tasks {
		servers[task.ServerID] = true
	}
	placements, _ := s.st.ListProjectPlacements(projectID)
	for _, pl := range placements {
		servers[pl.ServerID] = true
	}
	for serverID := range servers {
		s.syncTasksForServer(serverID)
	}
}

// taskEnvFor builds the deploy-time environment for a task: stack-shared values,
// sibling loopback URLs, then addon credentials. A stopped addon is logged and
// skipped rather than removing the task from the phone.
func (s *Server) taskEnvFor(p model.Project, serverID string) map[string]string {
	env, err := s.decryptEnv(p)
	if err != nil {
		s.log.Warn("task env: decrypt", "project", p.Slug, "err", err)
		env = map[string]string{}
	}
	if p.ParentID != "" {
		if parent, err := s.st.GetProject(p.ParentID); err == nil {
			if shared, err := s.decryptEnv(parent); err == nil {
				for k, v := range env {
					shared[k] = v
				}
				env = shared
			}
		}
		if siblings, err := s.st.ListChildProjects(p.ParentID); err == nil {
			for _, sibling := range siblings {
				if sibling.ID == p.ID {
					continue
				}
				placements, _ := s.st.ListProjectPlacements(sibling.ID)
				for _, sp := range placements {
					if sp.ServerID != serverID || sp.Port <= 0 {
						continue
					}
					key := "PHLEET_SERVICE_" + strings.ToUpper(strings.ReplaceAll(sibling.Slug, "-", "_")) + "_URL"
					env[key] = fmt.Sprintf("http://127.0.0.1:%d", sp.Port)
				}
			}
		}
	}
	if err := s.injectAddonEnv(p, serverID, env); err != nil {
		s.log.Warn("task env: addons", "project", p.Slug, "err", err)
	}
	return env
}

func (s *Server) onTaskEvent(serverID string, ev agentproto.TaskEvent) {
	if ev.TaskID == "" {
		return
	}
	status := ev.Status
	if status == "" {
		status = "finished"
	}
	lastRun := ev.StartedAt
	if lastRun == 0 {
		lastRun = time.Now().Unix()
	}
	if err := s.st.TouchTaskRun(ev.TaskID, lastRun, status, ev.ExitCode, ev.Error, ev.Output); err != nil {
		s.log.Warn("task event", "task", ev.TaskID, "err", err)
		return
	}
	if status == "failed" || status == "interrupted" {
		_ = s.st.AddEvent("agent", "task."+status, ev.TaskID, ev.Name+" on "+serverID)
	}
}

// taskSyncLoop is a safety net: a task create/update normally syncs immediately,
// but a busy agent, a panel restart or an addon change should not leave the
// phone with an old schedule for long.
func (s *Server) taskSyncLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.syncAllTaskServers()
		}
	}
}

func (s *Server) syncAllTaskServers() {
	tasks, err := s.st.ListTasks()
	if err != nil {
		return
	}
	servers := map[string]bool{}
	for _, task := range tasks {
		servers[task.ServerID] = true
	}
	for serverID := range servers {
		if s.hub.Online(serverID) {
			s.syncTasksForServer(serverID)
		}
	}
}
