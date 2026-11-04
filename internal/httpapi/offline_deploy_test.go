package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"phleet/internal/agentproto"
	"phleet/internal/model"
)

func TestOfflineFailsInflightDeployments(t *testing.T) {
	s := testServer(t)
	p := model.Project{ID: "p-offline", Name: "Offline", Slug: "offline", Kind: "app", Replicas: 1}
	if err := s.st.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	if err := s.st.UpsertPlacement(model.Placement{
		ProjectID: p.ID, ServerID: "phone-off", Port: 4100, Status: "building", Desired: "running",
	}); err != nil {
		t.Fatal(err)
	}
	live := model.Deployment{ID: "d-live", ProjectID: p.ID, ServerID: "phone-off", Status: "building"}
	done := model.Deployment{ID: "d-done", ProjectID: p.ID, ServerID: "phone-off", Status: "active"}
	for _, d := range []model.Deployment{live, done} {
		if err := s.st.CreateDeployment(d); err != nil {
			t.Fatal(err)
		}
	}

	s.failInflightDeployments("phone-off", deployOfflineDetail)

	got, err := s.st.GetDeployment(live.ID)
	if err != nil || got.Status != "failed" || got.Detail != deployOfflineDetail || got.FinishedAt == 0 {
		t.Fatalf("inflight deployment = %+v, err = %v", got, err)
	}
	if got, _ := s.st.GetDeployment(done.ID); got.Status != "active" {
		t.Fatalf("terminal deployment changed: %+v", got)
	}
	placements, _ := s.st.ListProjectPlacements(p.ID)
	if len(placements) != 1 || placements[0].Status != "failed" {
		t.Fatalf("placement after offline = %+v", placements)
	}
}

func TestOfflineFailsInflightAddonWork(t *testing.T) {
	s := testServer(t)
	a := model.Addon{
		ID: "a-offline", Name: "DB", Slug: "db-offline", Engine: "postgres",
		Kind: "database", PinnedServer: "phone-addon", Status: "provisioning",
	}
	if err := s.st.CreateAddon(a); err != nil {
		t.Fatal(err)
	}
	if err := s.st.UpsertAddonPlacement(model.AddonPlacement{
		AddonID: a.ID, ServerID: "phone-addon", Role: "primary", Status: "provisioning", Desired: "running",
	}); err != nil {
		t.Fatal(err)
	}
	b := model.Backup{ID: "b-offline", AddonID: a.ID, ServerID: "phone-addon", Status: "running"}
	if err := s.st.CreateBackup(b); err != nil {
		t.Fatal(err)
	}

	s.failOfflineAddonWork("phone-addon")

	gotAddon, _ := s.st.GetAddon(a.ID)
	if gotAddon.Status != "failed" {
		t.Fatalf("addon status = %q, want failed", gotAddon.Status)
	}
	gotBackup, _ := s.st.GetBackup(b.ID)
	if gotBackup.Status != "failed" || gotBackup.Error == "" {
		t.Fatalf("backup after offline = %+v", gotBackup)
	}
}

func TestOfflineDeploymentRecoversFromHeartbeat(t *testing.T) {
	s := testServer(t)
	p := model.Project{ID: "p-recover", Name: "Recover", Slug: "recover", Kind: "app", Replicas: 1}
	if err := s.st.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	if err := s.st.UpsertPlacement(model.Placement{
		ProjectID: p.ID, ServerID: "phone-back", Port: 4100, Status: "failed", Desired: "running",
	}); err != nil {
		t.Fatal(err)
	}
	dep := model.Deployment{
		ID: "d-recover", ProjectID: p.ID, ServerID: "phone-back",
		Status: "failed", Release: "release-1", Detail: deployOfflineDetail,
		StartedAt: time.Now().Add(-time.Minute).Unix(),
	}
	if err := s.st.CreateDeployment(dep); err != nil {
		t.Fatal(err)
	}

	s.syncAppStatus("phone-back", agentproto.AppStatus{
		Slug: p.Slug, State: "running", DeploymentID: dep.ID, DeployStatus: "active", PID: 123,
	})

	got, err := s.st.GetDeployment(dep.ID)
	if err != nil || got.Status != "active" || got.FinishedAt == 0 {
		t.Fatalf("recovered deployment = %+v, err = %v", got, err)
	}
	placements, _ := s.st.ListProjectPlacements(p.ID)
	if len(placements) != 1 || placements[0].Status != "running" {
		t.Fatalf("placement after reconnect = %+v", placements)
	}
}

func queuedDeployFixture(t *testing.T, s *Server, serverID string) (model.Project, model.Placement) {
	t.Helper()
	p := model.Project{
		ID: "p-" + serverID, Name: "Queue " + serverID, Slug: "queue-" + serverID,
		Kind: "app", Replicas: 1, Repo: "https://example.com/x.git", Branch: "main",
		StartCmd: "node server.js", SourceType: "git",
	}
	if err := s.st.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	pl := model.Placement{
		ProjectID: p.ID, ServerID: serverID, Port: 4100, Status: "pending", Desired: "running",
	}
	if err := s.st.UpsertPlacement(pl); err != nil {
		t.Fatal(err)
	}
	return p, pl
}

func TestOfflineDeployIsQueuedAndSurvivesOfflineCleanup(t *testing.T) {
	s := testServer(t)
	p, pl := queuedDeployFixture(t, s, "phone-queue")

	dep, err := s.queueDeployment(p, pl, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if dep.Status != "queued" || dep.Detail != deployWaitingDetail || dep.Ref != "v1.2.3" {
		t.Fatalf("queued deployment = %+v", dep)
	}
	list, err := s.st.ListQueuedDeployments("phone-queue")
	if err != nil || len(list) != 1 || list[0].ID != dep.ID {
		t.Fatalf("queued list = %+v, err = %v", list, err)
	}

	// A second offline event must not fail a request that was never sent.
	s.failInflightDeployments("phone-queue", deployOfflineDetail)
	got, err := s.st.GetDeployment(dep.ID)
	if err != nil || got.Status != "queued" || got.Detail != deployWaitingDetail || got.Ref != "v1.2.3" {
		t.Fatalf("queued deployment changed while offline: %+v, err = %v", got, err)
	}

	// Dispatch is a no-op while the phone is still offline.
	s.dispatchQueuedDeployment(got)
	got, _ = s.st.GetDeployment(dep.ID)
	if got.Status != "queued" || got.Detail != deployWaitingDetail {
		t.Fatalf("dispatch while offline changed the request: %+v", got)
	}
}

func TestNewDeploySupersedesQueuedRequest(t *testing.T) {
	s := testServer(t)
	p, pl := queuedDeployFixture(t, s, "phone-super")

	dep, err := s.queueDeployment(p, pl, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !s.releaseQueuedDeployments(p.ID) {
		t.Fatal("a waiting request must not block a newer deploy")
	}
	got, _ := s.st.GetDeployment(dep.ID)
	if got.Status != "failed" || !strings.Contains(got.Detail, "superseded") {
		t.Fatalf("superseded deployment = %+v", got)
	}

	// A deployment already accepted by the phone still blocks a new request.
	live := model.Deployment{
		ID: "d-live", ProjectID: p.ID, ServerID: pl.ServerID, Status: "building",
		StartedAt: time.Now().Unix(),
	}
	if err := s.st.CreateDeployment(live); err != nil {
		t.Fatal(err)
	}
	if s.releaseQueuedDeployments(p.ID) {
		t.Fatal("an in-flight deployment must block a new request")
	}
}

func TestStoppingProjectCancelsQueuedDeploy(t *testing.T) {
	s := testServer(t)
	p, pl := queuedDeployFixture(t, s, "phone-stop")

	dep, err := s.queueDeployment(p, pl, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.runProjectAction(context.Background(), p, "stop"); ok != 0 {
		t.Fatalf("offline stop reported %d successful replicas", ok)
	}
	got, _ := s.st.GetDeployment(dep.ID)
	if got.Status != "failed" || !strings.Contains(got.Detail, "stopped") {
		t.Fatalf("queued deploy survived stop: %+v", got)
	}
	placements, _ := s.st.ListProjectPlacements(p.ID)
	if len(placements) != 1 || placements[0].Desired != "stopped" {
		t.Fatalf("placement after stop = %+v", placements)
	}
}

func TestStoppedProjectRejectsDeploy(t *testing.T) {
	s := testServer(t)
	p, _ := queuedDeployFixture(t, s, "phone-stopped")
	if err := s.st.SetProjectDesired(p.ID, "stopped"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/deploy", nil)
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminToken)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "stopped") {
		t.Fatalf("stopped project deploy = %d %s", rr.Code, rr.Body.String())
	}
}

func TestStoppedStackRejectsDeploy(t *testing.T) {
	s := testServer(t)
	parent := model.Project{ID: "stack-stopped", Name: "Stack", Slug: "stack-stopped", Kind: "stack", Replicas: 1}
	child := model.Project{
		ID: "svc-stopped", ParentID: parent.ID, Name: "API", Slug: "svc-stopped",
		Kind: "app", Replicas: 1, PinnedServer: "phone-stack",
		Repo: "https://example.com/x.git", Branch: "main", StartCmd: "node server.js", SourceType: "git",
	}
	for _, p := range []model.Project{parent, child} {
		if err := s.st.CreateProject(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.st.UpsertPlacement(model.Placement{
		ProjectID: child.ID, ServerID: "phone-stack", Port: 4100, Status: "stopped", Desired: "stopped",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+parent.ID+"/deploy", nil)
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminToken)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "stopped service") {
		t.Fatalf("stopped stack deploy = %d %s", rr.Code, rr.Body.String())
	}
}

func TestPlacementsStopped(t *testing.T) {
	if placementsStopped(nil) {
		t.Fatal("no placements must not count as stopped")
	}
	if placementsStopped([]model.Placement{{Desired: "stopped"}, {Desired: "running"}}) {
		t.Fatal("mixed placements must not count as stopped")
	}
	if !placementsStopped([]model.Placement{{Desired: "stopped"}, {Desired: "stopped"}}) {
		t.Fatal("all-stopped placements must count as stopped")
	}
}
