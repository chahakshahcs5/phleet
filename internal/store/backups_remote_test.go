package store

import (
	"testing"
	"time"

	"phleet/internal/model"
)

func TestBackupRemoteAndDrills(t *testing.T) {
	st := newTestStore(t)
	b := model.Backup{ID: "b1", AddonID: "a1", ServerID: "phone-a", Status: "complete", Size: 10}
	if err := st.CreateBackup(b); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBackupRemote("b1", "uploaded", "remote/b1.enc", ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetBackup("b1")
	if err != nil || got.RemoteStatus != "uploaded" || got.RemoteKey != "remote/b1.enc" || got.RemoteAt == 0 {
		t.Fatalf("remote fields: %v %+v", err, got)
	}
	if err := st.SetBackupPath("b1", "/tmp/b1.enc"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetBackup("b1"); got.Path != "/tmp/b1.enc" {
		t.Fatalf("path: %+v", got)
	}

	now := time.Now().Unix()
	d := model.RestoreDrill{ID: "d1", AddonID: "a1", BackupID: "b1", Status: "running", StartedAt: now}
	if err := st.CreateDrill(d); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishDrill("d1", "passed", "ok"); err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestDrill("a1")
	if err != nil || latest.Status != "passed" || latest.Detail != "ok" || latest.FinishedAt == 0 {
		t.Fatalf("latest drill: %v %+v", err, latest)
	}
	list, err := st.ListDrills("a1", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list drills: %v %+v", err, list)
	}
	if err := st.PruneDrills(now + 1); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListDrills("a1", 10); len(list) != 0 {
		t.Fatalf("drills not pruned: %+v", list)
	}
}
