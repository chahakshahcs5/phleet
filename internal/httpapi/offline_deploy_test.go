package httpapi

import (
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
