//go:build linux

package agent

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phleet/internal/agentproto"
)

func testTaskAgent(t *testing.T) *Agent {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".phleet")
	a := &Agent{
		home:           home,
		shPath:         "/bin/sh",
		cfg:            Config{StateDir: state},
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		tasks:          map[string]agentproto.TaskSpec{},
		taskStates:     map[string]taskState{},
		taskRunning:    map[string]bool{},
		tasksPath:      filepath.Join(state, "tasks.json"),
		taskStatesPath: filepath.Join(state, "task-state.json"),
	}
	current := filepath.Join(home, "apps", "demo", "current")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	return a
}

func waitTask(t *testing.T, a *Agent, id string) agentproto.TaskStatus {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range a.taskStatuses() {
			if st.ID == id && !st.Running && st.Status != "" && st.Status != "running" {
				return st
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return agentproto.TaskStatus{}
}

func TestScheduledTaskRunsWithProjectEnv(t *testing.T) {
	a := testTaskAgent(t)
	if err := a.syncTasks(agentproto.TaskSyncRequest{Tasks: []agentproto.TaskSpec{{
		ID: "t1", Name: "hello", Project: true, Slug: "demo",
		Command: `echo "hello $PHLEET_TASK_ID in $PHLEET_APP" && test -n "$PHLEET_DATA_DIR"`,
		Cron:    "* * * * *", TimeoutSec: 10, Enabled: true,
	}}}); err != nil {
		t.Fatal(err)
	}
	a.runDueTasks(time.Now())
	st := waitTask(t, a, "t1")
	if st.Status != "succeeded" {
		t.Fatalf("status %q (%s)", st.Status, st.Error)
	}

	logs, err := a.taskLogs(agentproto.TaskLogsRequest{TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logs.Data), "hello t1 in demo") {
		t.Fatalf("task env missing from output: %q", logs.Data)
	}

	// The same minute must not run twice.
	before := st.LastRun
	a.runDueTasks(time.Now())
	time.Sleep(100 * time.Millisecond)
	for _, again := range a.taskStatuses() {
		if again.ID == "t1" && again.LastRun != before {
			t.Fatalf("task ran twice in the same minute: %d -> %d", before, again.LastRun)
		}
	}
}

func TestTaskFailureAndTimeout(t *testing.T) {
	a := testTaskAgent(t)
	if err := a.syncTasks(agentproto.TaskSyncRequest{Tasks: []agentproto.TaskSpec{
		{
			ID: "bad-exit", Name: "bad exit", Command: "exit 3",
			Cron: "* * * * *", TimeoutSec: 10, Enabled: true,
		},
		{
			ID: "slow", Name: "slow", Command: "sleep 5",
			Cron: "* * * * *", TimeoutSec: 1, Enabled: true,
		},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := a.runTaskNow("bad-exit"); err != nil {
		t.Fatal(err)
	}
	if st := waitTask(t, a, "bad-exit"); st.Status != "failed" || st.ExitCode != 3 {
		t.Fatalf("bad exit: %+v", st)
	}

	if err := a.runTaskNow("slow"); err != nil {
		t.Fatal(err)
	}
	if st := waitTask(t, a, "slow"); st.Status != "failed" || !strings.Contains(st.Error, "timed out") {
		t.Fatalf("timeout: %+v", st)
	}
}

func TestTaskSyncRejectsBadCron(t *testing.T) {
	a := testTaskAgent(t)
	err := a.syncTasks(agentproto.TaskSyncRequest{Tasks: []agentproto.TaskSpec{{
		ID: "t1", Name: "bad", Command: "true", Cron: "not a cron", Enabled: true,
	}}})
	if err == nil {
		t.Fatal("invalid cron must be rejected")
	}
}
