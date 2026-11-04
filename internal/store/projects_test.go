package store

import (
	"testing"

	"phleet/internal/model"
)

func TestProjectPlacementAndDeployments(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertServer(model.Server{ID: "phone-a", Name: "phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	p := model.Project{
		ID: "p1", Name: "Demo", Slug: "demo", Repo: "https://example.com/x.git",
		Branch: "main", BuildCmd: "npm ci", StartCmd: "node server.js",
		HealthPath: "/health", Hostname: "demo.example.com", EnvEnc: "cipher",
		GitAuthType: "https", GitAuthUsername: "deploy", GitAuthEnc: "git-cipher",
	}
	if err := st.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetProjectBySlug("demo")
	if err != nil || got.ID != "p1" || got.HealthPath != "/health" {
		t.Fatalf("get by slug: %v %+v", err, got)
	}
	if got.GitAuthType != "https" || got.GitAuthUsername != "deploy" || got.GitAuthEnc != "git-cipher" {
		t.Fatalf("git auth did not round-trip: %+v", got)
	}
	if _, err := st.GetProjectByHostname("demo.example.com"); err != nil {
		t.Fatalf("get by hostname: %v", err)
	}

	if err := st.UpsertPlacement(model.Placement{ProjectID: "p1", ServerID: "phone-a", Port: 4100, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	pl, err := st.GetPlacement("p1")
	if err != nil || pl.Port != 4100 {
		t.Fatalf("placement: %v %+v", err, pl)
	}
	used, err := st.UsedPorts("phone-a")
	if err != nil || !used[4100] {
		t.Fatalf("used ports: %v %v", err, used)
	}
	if err := st.UpdatePlacementStatus("p1", "running", 42, 3, "", "/releases/1"); err != nil {
		t.Fatal(err)
	}
	pl, _ = st.GetPlacement("p1")
	if pl.Status != "running" || pl.PID != 42 || pl.Restarts != 3 {
		t.Fatalf("status not applied: %+v", pl)
	}

	dep := model.Deployment{ID: "d1", ProjectID: "p1", ServerID: "phone-a", Status: "queued"}
	if err := st.CreateDeployment(dep); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDeployLog("d1", "cloning"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddDeployLog("d1", "building"); err != nil {
		t.Fatal(err)
	}
	logs, err := st.ListDeployLogs("d1", 10)
	if err != nil || len(logs) != 2 || logs[0].Line != "cloning" {
		t.Fatalf("logs: %v %+v", err, logs)
	}
	if err := st.FinishDeployment("d1", "active", "/releases/1", ""); err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestDeployment("p1")
	if err != nil || latest.Status != "active" || latest.FinishedAt == 0 {
		t.Fatalf("latest: %v %+v", err, latest)
	}

	// Cascading delete removes placements, deployments and logs.
	if err := st.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetProject("p1"); err == nil {
		t.Fatal("project should be gone")
	}
	if _, err := st.GetPlacement("p1"); err == nil {
		t.Fatal("placement should be gone")
	}
	if logs, _ := st.ListDeployLogs("d1", 10); len(logs) != 0 {
		t.Fatalf("deploy logs should be gone, got %d", len(logs))
	}
}

func TestProjectKindsAndStaticDirRoundTrip(t *testing.T) {
	st := newTestStore(t)
	for _, p := range []model.Project{
		{ID: "s", Name: "Site", Slug: "site", Kind: "static", StaticDir: "dist"},
		{ID: "w", Name: "Worker", Slug: "worker", Kind: "worker", StartCmd: "node worker.js"},
	} {
		if err := st.CreateProject(p); err != nil {
			t.Fatal(err)
		}
	}
	site, err := st.GetProject("s")
	if err != nil || site.Kind != "static" || site.StaticDir != "dist" {
		t.Fatalf("static project: %v %+v", err, site)
	}
	worker, err := st.GetProject("w")
	if err != nil || worker.Kind != "worker" || worker.StartCmd != "node worker.js" {
		t.Fatalf("worker project: %v %+v", err, worker)
	}
}

func TestPruneProjectHistory(t *testing.T) {
	st := newTestStore(t)
	_ = st.CreateProject(model.Project{ID: "p", Name: "p", Slug: "p"})
	_ = st.CreateDeployment(model.Deployment{ID: "old", ProjectID: "p", StartedAt: 100})
	_ = st.CreateDeployment(model.Deployment{ID: "new", ProjectID: "p", StartedAt: 2_000_000_000})
	_ = st.AddDeployLog("old", "line")
	if err := st.PruneProjectHistory(1000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDeployment("old"); err == nil {
		t.Fatal("old deployment should be pruned")
	}
	if _, err := st.GetDeployment("new"); err != nil {
		t.Fatal("new deployment should remain")
	}
}
