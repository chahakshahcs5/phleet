package store

import (
	"testing"

	"phleet/internal/model"
)

func TestServiceAddonFields(t *testing.T) {
	st := newTestStore(t)
	a := model.Addon{
		ID: "s1", Name: "Memos", Slug: "memos", Engine: "memos", Kind: "service",
		Service: true, AppID: "memos", Version: "26.11.0", PinnedServer: "phone-1",
		Port: 4101, DataDir: "/home/u/phleet/services/memos", Status: "pending",
	}
	if err := st.CreateAddon(a); err != nil {
		t.Fatalf("create service: %v", err)
	}
	got, err := st.GetAddon("s1")
	if err != nil || !got.Service || got.AppID != "memos" || got.Version != "26.11.0" {
		t.Fatalf("service fields lost: %+v %v", got, err)
	}
	if err := st.RenameAddon("s1", "Notes"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ = st.GetAddon("s1")
	if got.Name != "Notes" {
		t.Fatalf("rename did not persist: %+v", got)
	}
}

func TestAddonLifecycle(t *testing.T) {
	st := newTestStore(t)

	a := model.Addon{
		ID: "a1", Name: "App DB", Slug: "db-app-db", Engine: "postgres", Kind: "database",
		PinnedServer: "phone-1", Port: 4100, BridgePort: 15400, Hostname: "s3-app.localhost",
		DataDir: "/home/u/phleet/data/db-app-db",
		Status:  "pending", EnvEnc: "enc",
	}
	if err := st.CreateAddon(a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.GetAddon("a1")
	if err != nil || got.Slug != "db-app-db" || got.Port != 4100 || got.BridgePort != 15400 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.GetAddonBySlug("db-app-db"); err != nil {
		t.Fatalf("get by slug: %v", err)
	}
	if byHost, err := st.GetAddonByHostname("s3-app.localhost"); err != nil || byHost.ID != "a1" {
		t.Fatalf("get by hostname: %+v %v", byHost, err)
	}
	if ports, err := st.UsedBridgePorts(); err != nil || !ports[15400] {
		t.Fatalf("bridge port not tracked: %v %v", ports, err)
	}

	if err := st.UpsertAddonRelay(model.AddonRelay{AddonID: "a1", ServerID: "phone-2", Port: 16600}); err != nil {
		t.Fatalf("relay: %v", err)
	}
	if ports, err := st.UsedRelayPorts("phone-2"); err != nil || !ports[16600] {
		t.Fatalf("relay port not tracked: %v %v", ports, err)
	}
	if relays, err := st.ListAddonRelays("a1"); err != nil || len(relays) != 1 || relays[0].Port != 16600 {
		t.Fatalf("list relays: %+v %v", relays, err)
	}
	if _, err := st.GetAddonRelay("a1", "phone-2"); err != nil {
		t.Fatalf("get relay: %v", err)
	}
	if err := st.DeleteAddonRelay("a1", "phone-2"); err != nil {
		t.Fatalf("delete relay: %v", err)
	}
	if relays, _ := st.ListAddonRelays("a1"); len(relays) != 0 {
		t.Fatalf("relay survived delete: %+v", relays)
	}

	if err := st.UpsertAddonPlacement(model.AddonPlacement{
		AddonID: "a1", ServerID: "phone-1", Role: "primary", Status: "running", Port: 4100, LagMS: 42,
	}); err != nil {
		t.Fatalf("placement: %v", err)
	}
	pls, err := st.ListAddonPlacements("a1")
	if err != nil || len(pls) != 1 || pls[0].LagMS != 42 {
		t.Fatalf("placement lag: %+v %v", pls, err)
	}
	if err := st.SetAddonPlacementLag("a1", "phone-1", 7); err != nil {
		t.Fatalf("set lag: %v", err)
	}
	if err := st.SetAddonPlacementRole("a1", "phone-1", "standby", "running"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	pls, _ = st.ListAddonPlacements("a1")
	if pls[0].LagMS != 7 || pls[0].Role != "standby" {
		t.Fatalf("placement not updated: %+v", pls)
	}
	ports, err := st.UsedPorts("phone-1")
	if err != nil || !ports[4100] {
		t.Fatalf("addon port not reserved: %v %v", ports, err)
	}

	if err := st.BindAddon("a1", "project-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	list, err := st.ListProjectAddons("project-1")
	if err != nil || len(list) != 1 || list[0].ID != "a1" {
		t.Fatalf("project addons: %+v %v", list, err)
	}
	if err := st.UnbindAddon("a1", "project-1"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if list, _ := st.ListProjectAddons("project-1"); len(list) != 0 {
		t.Fatalf("binding survived unbind: %+v", list)
	}

	if err := st.SetAddonStatus("a1", "failed", "boom"); err != nil {
		t.Fatalf("status: %v", err)
	}
	got, _ = st.GetAddon("a1")
	if got.Status != "failed" || got.LastError != "boom" {
		t.Fatalf("status not persisted: %+v", got)
	}

	if err := st.DeleteAddon("a1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetAddon("a1"); err == nil {
		t.Fatal("addon survived delete")
	}
	if list, _ := st.ListAddonPlacements("a1"); len(list) != 0 {
		t.Fatalf("placements survived delete: %+v", list)
	}
}

func TestAddonBackups(t *testing.T) {
	st := newTestStore(t)
	for i := 0; i < 4; i++ {
		id := "b" + string(rune('0'+i))
		if err := st.CreateBackup(model.Backup{
			ID: id, AddonID: "a1", Status: "complete", Size: int64(i + 1), Path: "/tmp/" + id,
			CreatedAt: int64(100 + i),
		}); err != nil {
			t.Fatalf("create backup %s: %v", id, err)
		}
	}
	list, err := st.ListBackups("a1", 10)
	if err != nil || len(list) != 4 {
		t.Fatalf("list backups: %d %v", len(list), err)
	}
	if list[0].ID != "b3" {
		t.Fatalf("backups are not newest-first: %+v", list)
	}
	removed, err := st.PruneBackups("a1", 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 pruned, got %d", len(removed))
	}
	list, _ = st.ListBackups("a1", 10)
	if len(list) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(list))
	}
}

func TestAddonLogs(t *testing.T) {
	st := newTestStore(t)
	for _, line := range []string{"one", "two", "three"} {
		if err := st.AddAddonLog("a1", line); err != nil {
			t.Fatalf("log: %v", err)
		}
	}
	logs, err := st.ListAddonLogs("a1", 10)
	if err != nil || len(logs) != 3 {
		t.Fatalf("logs: %+v %v", logs, err)
	}
	if logs[0].Line != "one" || logs[2].Line != "three" {
		t.Fatalf("logs are not chronological: %+v", logs)
	}
}

func TestAddonUsageFields(t *testing.T) {
	st := newTestStore(t)
	a := model.Addon{ID: "u1", Name: "Usage", Slug: "usage", Engine: "sqlite", Kind: "database", SizeLimitMB: 100}
	if err := st.CreateAddon(a); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAddonUsage("u1", 123, 456); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAddon("u1")
	if err != nil || got.DataUsedMB != 123 || got.UsageCheckedAt != 456 || got.SizeLimitMB != 100 {
		t.Fatalf("usage fields: %v %+v", err, got)
	}
}
