//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"phleet/internal/agentproto"
	"phleet/internal/cron"
)

var taskIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const (
	defaultTaskTimeout = 300
	maxTaskTimeout     = 3600
	taskLogMaxBytes    = 1 << 20
	taskEventOutput    = 16 << 10
)

type taskState struct {
	LastRun   int64  `json:"lastRun"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exitCode"`
	Error     string `json:"error,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

type taskStateFile struct {
	Tasks map[string]taskState `json:"tasks"`
}

func (a *Agent) loadTasks() error {
	if b, err := os.ReadFile(a.tasksPath); err == nil {
		var specs []agentproto.TaskSpec
		if err := json.Unmarshal(b, &specs); err != nil {
			return fmt.Errorf("tasks.json: %w", err)
		}
		a.taskMu.Lock()
		for _, spec := range specs {
			if taskIDRe.MatchString(spec.ID) {
				a.tasks[spec.ID] = spec
			}
		}
		a.taskMu.Unlock()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if b, err := os.ReadFile(a.taskStatesPath); err == nil {
		var f taskStateFile
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("task-state.json: %w", err)
		}
		a.taskMu.Lock()
		for id, st := range f.Tasks {
			// A run cannot survive an agent restart. Mark it rather than
			// leaving a stale "running" badge in the panel forever.
			if st.Status == "running" {
				st.Status = "interrupted"
				st.Error = "agent restarted while the task was running"
			}
			a.taskStates[id] = st
		}
		a.taskMu.Unlock()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (a *Agent) saveTasks() {
	a.taskMu.Lock()
	specs := make([]agentproto.TaskSpec, 0, len(a.tasks))
	for _, spec := range a.tasks {
		specs = append(specs, spec)
	}
	a.taskMu.Unlock()
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Name == specs[j].Name {
			return specs[i].ID < specs[j].ID
		}
		return specs[i].Name < specs[j].Name
	})
	b, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return
	}
	writeTaskState(a.tasksPath, append(b, '\n'))
}

func (a *Agent) saveTaskStates() {
	a.taskMu.Lock()
	file := taskStateFile{Tasks: make(map[string]taskState, len(a.taskStates))}
	for id, st := range a.taskStates {
		file.Tasks[id] = st
	}
	a.taskMu.Unlock()
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	writeTaskState(a.taskStatesPath, append(b, '\n'))
}

func writeTaskState(path string, b []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// syncTasks replaces the local schedule. Run state is preserved by ID so a
// control-plane restart or an env refresh does not replay a task.
func (a *Agent) syncTasks(req agentproto.TaskSyncRequest) error {
	next := make(map[string]agentproto.TaskSpec, len(req.Tasks))
	for _, spec := range req.Tasks {
		if spec.ID == "" {
			return errors.New("task id is empty")
		}
		if !taskIDRe.MatchString(spec.ID) {
			return fmt.Errorf("task id %q is invalid", spec.ID)
		}
		if strings.TrimSpace(spec.Command) == "" {
			return fmt.Errorf("task %s has no command", spec.ID)
		}
		if _, err := cron.Parse(spec.Cron); err != nil {
			return fmt.Errorf("task %s: %w", spec.ID, err)
		}
		if spec.Project && !slugRe.MatchString(spec.Slug) {
			return fmt.Errorf("task %s has an invalid project slug", spec.ID)
		}
		if spec.TimeoutSec <= 0 {
			spec.TimeoutSec = defaultTaskTimeout
		}
		if spec.TimeoutSec > maxTaskTimeout {
			spec.TimeoutSec = maxTaskTimeout
		}
		next[spec.ID] = spec
	}
	a.taskMu.Lock()
	a.tasks = next
	for id := range a.taskStates {
		if _, ok := next[id]; !ok {
			delete(a.taskStates, id)
		}
	}
	a.taskMu.Unlock()
	a.saveTasks()
	a.saveTaskStates()
	return nil
}

func (a *Agent) taskLoop(ctx context.Context) {
	a.runDueTasks(time.Now())
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.runDueTasks(now)
		}
	}
}

func (a *Agent) runDueTasks(now time.Time) {
	minute := now.Truncate(time.Minute).Unix()
	var due []agentproto.TaskSpec
	a.taskMu.Lock()
	for id, spec := range a.tasks {
		if !spec.Enabled || a.taskRunning[id] {
			continue
		}
		st := a.taskStates[id]
		if st.LastRun >= minute {
			continue
		}
		sched, err := cron.Parse(spec.Cron)
		if err != nil || !sched.Matches(now) {
			continue
		}
		due = append(due, spec)
	}
	a.taskMu.Unlock()
	for _, spec := range due {
		a.startTask(spec, minute)
	}
}

// startTask marks a task running and launches it. It is the single place that
// prevents overlap between a scheduled run and a manual run.
func (a *Agent) startTask(spec agentproto.TaskSpec, minute int64) bool {
	a.taskMu.Lock()
	if a.taskRunning[spec.ID] {
		a.taskMu.Unlock()
		return false
	}
	a.taskRunning[spec.ID] = true
	a.taskStates[spec.ID] = taskState{
		LastRun: minute, Status: "running", UpdatedAt: time.Now().Unix(),
	}
	a.taskMu.Unlock()
	a.saveTaskStates()
	go a.executeTask(spec, minute)
	return true
}

func (a *Agent) executeTask(spec agentproto.TaskSpec, minute int64) {
	startedAt := time.Now().Unix()
	cwd, err := a.taskCwd(spec)
	if err == nil {
		logPath := a.taskLogPath(spec)
		var exit int
		var output string
		var runErr error
		exit, output, runErr = a.runTaskCommand(spec, cwd, logPath, a.taskEnv(spec))
		if runErr == nil && exit != 0 {
			runErr = fmt.Errorf("exit code %d", exit)
		}
		if runErr != nil {
			a.finishTask(spec, minute, "failed", exit, runErr.Error(), output, startedAt)
			return
		}
		a.finishTask(spec, minute, "succeeded", 0, "", output, startedAt)
		return
	}
	a.finishTask(spec, minute, "failed", -1, err.Error(), "", startedAt)
}

func (a *Agent) finishTask(spec agentproto.TaskSpec, minute int64, status string, exit int, errMsg, output string, startedAt int64) {
	finishedAt := time.Now().Unix()
	a.taskMu.Lock()
	a.taskRunning[spec.ID] = false
	// Keep a newer manual run's state if one somehow completed first.
	if cur := a.taskStates[spec.ID]; cur.LastRun <= minute {
		a.taskStates[spec.ID] = taskState{
			LastRun: minute, Status: status, ExitCode: exit, Error: errMsg, UpdatedAt: finishedAt,
		}
	}
	a.taskMu.Unlock()
	a.saveTaskStates()
	ev := agentproto.TaskEvent{
		TaskID: spec.ID, Name: spec.Name, Status: status, ExitCode: exit,
		StartedAt: startedAt, FinishedAt: finishedAt, Output: tailString(output, taskEventOutput), Error: errMsg,
	}
	if err := a.send(agentproto.TypeTaskEvent, "", ev); err != nil {
		a.log.Debug("task event", "task", spec.ID, "err", err)
	}
	a.log.Info("task finished", "task", spec.Name, "status", status, "exit", exit)
}

func (a *Agent) runTaskNow(id string) error {
	a.taskMu.Lock()
	spec, ok := a.tasks[id]
	running := a.taskRunning[id]
	a.taskMu.Unlock()
	if !ok {
		return fmt.Errorf("task %q is not installed", id)
	}
	if running {
		return errors.New("task is already running")
	}
	if !a.startTask(spec, time.Now().Truncate(time.Minute).Unix()) {
		return errors.New("task is already running")
	}
	return nil
}

func (a *Agent) taskCwd(spec agentproto.TaskSpec) (string, error) {
	if !spec.Project {
		return a.home, nil
	}
	dir := filepath.Join(a.appBase(spec.Slug), "current")
	st, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("project %s has no current release on this phone", spec.Slug)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("project %s current release is not a directory", spec.Slug)
	}
	return dir, nil
}

func (a *Agent) taskLogPath(spec agentproto.TaskSpec) string {
	if spec.Project {
		return filepath.Join(a.appBase(spec.Slug), "logs", "task-"+spec.ID+".log")
	}
	return filepath.Join(a.cfg.StateDir, "logs", "task-"+spec.ID+".log")
}

func (a *Agent) taskEnv(spec agentproto.TaskSpec) []string {
	m := envToMap(sanitizeEnv(os.Environ()))
	m["PHLEET_TASK_ID"] = spec.ID
	m["PHLEET_TASK_NAME"] = spec.Name
	if spec.Project {
		dataDir := filepath.Join(a.appBase(spec.Slug), "shared", "data")
		_ = os.MkdirAll(dataDir, 0o755)
		m["NODE_ENV"] = "production"
		m["PHLEET_APP"] = spec.Slug
		m["PHLEET_DATA_DIR"] = dataDir
	}
	// Task env is applied last so it can override inherited/built-in values.
	applyEnv(m, spec.Env)
	return envFromMap(m)
}

func (a *Agent) runTaskCommand(spec agentproto.TaskSpec, cwd, logPath string, env []string) (int, string, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return -1, "", err
	}
	rotateLog(logPath, taskLogMaxBytes)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return -1, "", err
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] task %s started: %s\n", time.Now().Format("2006-01-02 15:04:05"), spec.Name, spec.Command)

	tail := &tailBuffer{max: 64 << 10}
	out := io.MultiWriter(f, tail)
	timeout := time.Duration(spec.TimeoutSec) * time.Second
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.shPath, "-c", spec.Command)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out
	prepareCommandGroup(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(f, "[%s] task %s could not start: %v\n", time.Now().Format("2006-01-02 15:04:05"), spec.Name, err)
		return -1, tail.String(), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		killProcessGroup(cmd.Process.Pid)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			runErr = fmt.Errorf("timed out after %ds", spec.TimeoutSec)
		} else {
			runErr = errors.New("agent is stopping")
		}
	}
	exit := 0
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	if runErr != nil && exit == 0 {
		exit = -1
	}
	status := "ok"
	if runErr != nil {
		status = runErr.Error()
	}
	fmt.Fprintf(f, "[%s] task %s finished (exit %d): %s\n", time.Now().Format("2006-01-02 15:04:05"), spec.Name, exit, status)
	return exit, tail.String(), runErr
}

func (a *Agent) taskStatuses() []agentproto.TaskStatus {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	out := make([]agentproto.TaskStatus, 0, len(a.tasks))
	for id := range a.tasks {
		st := a.taskStates[id]
		out = append(out, agentproto.TaskStatus{
			ID: id, LastRun: st.LastRun, Status: st.Status, ExitCode: st.ExitCode,
			Error: st.Error, Running: a.taskRunning[id],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (a *Agent) taskLogs(req agentproto.TaskLogsRequest) (agentproto.TaskLogsResponse, error) {
	a.taskMu.Lock()
	spec, ok := a.tasks[req.TaskID]
	a.taskMu.Unlock()
	if !ok {
		return agentproto.TaskLogsResponse{}, fmt.Errorf("task %q is not installed", req.TaskID)
	}
	path := a.taskLogPath(spec)
	f, err := os.Open(path)
	if err != nil {
		return agentproto.TaskLogsResponse{TaskID: req.TaskID, Path: path}, nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return agentproto.TaskLogsResponse{}, err
	}
	max := int64(req.Bytes)
	if max <= 0 || max > 512<<10 {
		max = 64 << 10
	}
	start := int64(0)
	if st.Size() > max {
		start = st.Size() - max
	}
	if _, err := f.Seek(start, 0); err != nil {
		return agentproto.TaskLogsResponse{}, err
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.Read(buf); err != nil {
		return agentproto.TaskLogsResponse{}, err
	}
	return agentproto.TaskLogsResponse{
		TaskID: req.TaskID, Path: path, Data: buf, Size: st.Size(), Truncated: start > 0,
	}, nil
}

func tailString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if t.max > 0 && len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
