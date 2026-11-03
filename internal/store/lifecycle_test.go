package store

import (
	"testing"
	"time"

	"phleet/internal/model"
)

func TestDeleteServerRemovesPlacementsAndRelays(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertServer(model.Server{ID: "phone-1", Name: "Phone"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProject(model.Project{ID: "p1", Name: "App", Slug: "app", Kind: "app", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlacement(model.Placement{
		ProjectID: "p1", ServerID: "phone-1", Port: 4100, Status: "running", Desired: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAddonRelay(model.AddonRelay{AddonID: "a1", ServerID: "phone-1", Port: 16500}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteServer("phone-1"); err != nil {
		t.Fatal(err)
	}
	if placements, err := st.ListPlacementsForServer("phone-1"); err != nil || len(placements) != 0 {
		t.Fatalf("placements left behind: %v %d", err, len(placements))
	}
	if relays, err := st.ListAddonRelaysForServer("phone-1"); err != nil || len(relays) != 0 {
		t.Fatalf("relays left behind: %v %d", err, len(relays))
	}
	if _, err := st.GetServer("phone-1"); err == nil {
		t.Fatal("server row still exists")
	}
}

func TestBackupCreatesUsableSnapshot(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddEvent("ui", "before-backup", "", ""); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/snapshot.db"
	if err := st.Backup(path); err != nil {
		t.Fatal(err)
	}
	copyStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer copyStore.Close()
	events, _, err := copyStore.ListEventsPage(EventQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "before-backup" {
		t.Fatalf("snapshot events = %+v", events)
	}
}

func TestPruneEventsKeepsRecentRows(t *testing.T) {
	st := newTestStore(t)
	old := time.Now().Add(-100 * 24 * time.Hour).Unix()
	if _, err := st.DB().Exec(`INSERT INTO events (ts,actor,action,target,detail) VALUES (?,?,?,?,?)`,
		old, "ui", "old", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.AddEvent("ui", "new", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.PruneEvents(time.Now().Add(-90 * 24 * time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	events, _, err := st.ListEventsPage(EventQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "new" {
		t.Fatalf("events after prune = %+v", events)
	}
}
