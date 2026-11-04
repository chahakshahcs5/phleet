package store

import (
	"testing"

	"phleet/internal/model"
)

func TestTaskDeletedWithProject(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateProject(model.Project{ID: "p1", Name: "App", Slug: "app", Kind: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTask(model.Task{ID: "t1", Name: "task", ServerID: "phone-a", ProjectID: "p1", Command: "true", Cron: "* * * * *"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTask("t1"); err == nil {
		t.Fatal("task should be deleted with its project")
	}
}

func TestTaskCRUDAndRunState(t *testing.T) {
	st := newTestStore(t)
	task := model.Task{
		ID: "t1", Name: "Nightly", ServerID: "phone-a", ProjectID: "p1",
		Command: "npm run prune", Cron: "0 3 * * *", TimeoutSec: 300, Enabled: true,
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask("t1")
	if err != nil || got.Name != "Nightly" || !got.Enabled || got.Cron != "0 3 * * *" {
		t.Fatalf("get: %v %+v", err, got)
	}
	byServer, err := st.ListTasksForServer("phone-a")
	if err != nil || len(byServer) != 1 {
		t.Fatalf("by server: %v %+v", err, byServer)
	}
	byProject, err := st.ListTasksForProject("p1")
	if err != nil || len(byProject) != 1 {
		t.Fatalf("by project: %v %+v", err, byProject)
	}

	if err := st.TouchTaskRun("t1", 100, "failed", 2, "boom", "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTask("t1")
	if got.LastRun != 100 || got.LastStatus != "failed" || got.LastExit != 2 || got.LastOutput == "" {
		t.Fatalf("run state: %+v", got)
	}
	// A heartbeat status update carries no output and must not clear it.
	if err := st.TouchTaskRun("t1", 101, "running", 0, "", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTask("t1")
	if got.LastOutput == "" || got.LastStatus != "running" {
		t.Fatalf("heartbeat erased output: %+v", got)
	}

	task.Enabled = false
	task.Name = "Nightly (paused)"
	if err := st.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTask("t1")
	if got.Enabled || got.Name != "Nightly (paused)" {
		t.Fatalf("update: %+v", got)
	}

	if err := st.DeleteTasksForProject("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTask("t1"); err == nil {
		t.Fatal("task should be deleted with its project")
	}
}
