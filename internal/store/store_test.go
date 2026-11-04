package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"phleet/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestOpenFileDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "phleet.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open file db: %v", err)
	}
	defer st.Close()
	if err := st.UpsertServer(model.Server{ID: "p", Name: "p"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file missing: %v", err)
	}
}

func TestServerLifecycle(t *testing.T) {
	st := newTestStore(t)
	ts := time.Now().Unix()
	srv := model.Server{
		ID: "phone-1", Name: "Kitchen", Hostname: "phone-a", OS: "android-14", Arch: "arm64",
		AgentVersion: "0.1.0", Caps: map[string]any{"root": true}, Labels: map[string]string{},
		Status: "online", LastSeen: ts,
	}
	if err := st.UpsertServer(srv); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.GetServer("phone-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Kitchen" || got.Caps["root"] != true {
		t.Fatalf("unexpected server: %+v", got)
	}

	if err := st.UpdateServerMeta("phone-1", "Living room", map[string]string{"tier": "web"}); err != nil {
		t.Fatalf("meta: %v", err)
	}
	got, _ = st.GetServer("phone-1")
	if got.Name != "Living room" || got.Labels["tier"] != "web" {
		t.Fatalf("meta not applied: %+v", got)
	}

	// A reconnect must not clobber a user rename.
	srv.Name = "agent default"
	if err := st.UpsertServer(srv); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	got, _ = st.GetServer("phone-1")
	if got.Name != "Living room" {
		t.Fatalf("rename was clobbered: %q", got.Name)
	}

	list, err := st.ListServers()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}

	if err := st.DeleteServer("phone-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetServer("phone-1"); err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestMetricsAndPrune(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertServer(model.Server{ID: "p", Name: "p", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	now := (time.Now().Unix()/3600)*3600 + 600
	for i := 0; i < 5; i++ {
		err := st.InsertMetrics("p", model.Metrics{
			CPUPercent: float64(10 + i), MemUsed: 100, MemTotal: 1000,
			TempC: float64(40 + i), BatteryPct: 80, At: now - int64(i*5),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	samples, err := st.QueryMetrics("p", now-60, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 5 {
		t.Fatalf("expected 5 samples, got %d", len(samples))
	}
	latest, err := st.LatestMetrics("p")
	if err != nil {
		t.Fatal(err)
	}
	if latest.CPUPercent != 10 || latest.TempC != 40 {
		t.Fatalf("unexpected latest: %+v", latest)
	}

	// Coarser bucket collapses the window.
	coarse, err := st.QueryMetrics("p", now-60, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(coarse) != 1 {
		t.Fatalf("expected one bucket, got %d", len(coarse))
	}

	if err := st.PruneMetrics(now + 10); err != nil {
		t.Fatal(err)
	}
	samples, _ = st.QueryMetrics("p", 0, 5)
	if len(samples) != 0 {
		t.Fatalf("expected pruned samples, got %d", len(samples))
	}
}

// Regression: AVG() returns REAL, and a real phone has byte counts large
// enough that SQLite renders the average in exponent notation. Scanning that
// into int64 made QueryMetrics fail with "invalid syntax", which left every
// chart empty.
func TestQueryMetricsAveragesLargeIntegerColumns(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertServer(model.Server{ID: "p", Name: "p", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i := 0; i < 3; i++ {
		err := st.InsertMetrics("p", model.Metrics{
			CPUPercent:  41.5 + float64(i),
			MemUsed:     1_444_539_050 + int64(i),
			MemTotal:    3_900_000_000,
			DiskUsed:    27_600_000_000 + int64(i),
			DiskTotal:   54_000_000_000,
			TempC:       35.8,
			BatteryPct:  64 + i,
			BatteryTemp: 34,
			Charging:    true,
			Load1:       5.1,
			NetRx:       1_234_567_890 + int64(i),
			NetTx:       987_654_321 + int64(i),
			Uptime:      51_475 + int64(i),
			At:          now - int64(i*5),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	samples, err := st.QueryMetrics("p", now-60, 5)
	if err != nil {
		t.Fatalf("query metrics: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}
	if samples[0].MemTotal != 3_900_000_000 {
		t.Fatalf("unexpected mem total %d", samples[0].MemTotal)
	}
	if samples[0].MemUsed < 1_444_539_000 || samples[0].MemUsed > 1_444_539_100 {
		t.Fatalf("unexpected mem used %d", samples[0].MemUsed)
	}
	if samples[0].BatteryPct < 63 || samples[0].BatteryPct > 67 {
		t.Fatalf("unexpected battery pct %d", samples[0].BatteryPct)
	}
}

func TestStaleAndReset(t *testing.T) {
	st := newTestStore(t)
	old := time.Now().Add(-5 * time.Minute).Unix()
	_ = st.UpsertServer(model.Server{ID: "a", Name: "a", Status: "online", LastSeen: old})
	_ = st.UpsertServer(model.Server{ID: "b", Name: "b", Status: "online", LastSeen: time.Now().Unix()})

	n, err := st.MarkStaleOffline(time.Now().Add(-time.Minute).Unix())
	if err != nil || n != 1 {
		t.Fatalf("mark stale: %v n=%d", err, n)
	}
	a, _ := st.GetServer("a")
	if a.Status != "offline" {
		t.Fatalf("expected offline, got %q", a.Status)
	}

	if err := st.ResetServersOffline(); err != nil {
		t.Fatal(err)
	}
	b, _ := st.GetServer("b")
	if b.Status != "offline" {
		t.Fatalf("expected reset offline, got %q", b.Status)
	}
}

func TestEventsAndSettings(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddEvent("ui", "server.delete", "x", "detail"); err != nil {
		t.Fatal(err)
	}
	evts, err := st.ListEvents(10)
	if err != nil || len(evts) != 1 {
		t.Fatalf("events: %v %d", err, len(evts))
	}
	if evts[0].Action != "server.delete" {
		t.Fatalf("unexpected event: %+v", evts[0])
	}

	if err := st.SetSetting("k", "v"); err != nil {
		t.Fatal(err)
	}
	v, err := st.GetSetting("k")
	if err != nil || v != "v" {
		t.Fatalf("setting: %v %q", err, v)
	}
}

func TestEventPaginationAndFilters(t *testing.T) {
	st := newTestStore(t)
	for i := 1; i <= 7; i++ {
		action := "agent.online"
		if i%2 == 0 {
			action = "deploy.start"
		}
		if err := st.AddEvent("ui", action, fmt.Sprintf("target-%d", i), fmt.Sprintf("detail %d", i)); err != nil {
			t.Fatal(err)
		}
	}

	page1, more, err := st.ListEventsPage(EventQuery{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 3 || !more {
		t.Fatalf("page1: len=%d more=%v", len(page1), more)
	}
	if page1[0].ID <= page1[1].ID || page1[1].ID <= page1[2].ID {
		t.Fatalf("events are not newest-first: %+v", page1)
	}

	page2, more, err := st.ListEventsPage(EventQuery{Limit: 3, Before: page1[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 3 || !more {
		t.Fatalf("page2: len=%d more=%v", len(page2), more)
	}
	if page2[0].ID >= page1[2].ID {
		t.Fatalf("pages overlap: %d >= %d", page2[0].ID, page1[2].ID)
	}

	page3, more, err := st.ListEventsPage(EventQuery{Limit: 3, Before: page2[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 || more {
		t.Fatalf("page3: len=%d more=%v", len(page3), more)
	}

	filtered, more, err := st.ListEventsPage(EventQuery{Limit: 10, Action: "deploy.start"})
	if err != nil || len(filtered) != 3 || more {
		t.Fatalf("action filter: %v len=%d more=%v", err, len(filtered), more)
	}

	searched, _, err := st.ListEventsPage(EventQuery{Limit: 10, Search: "TARGET-5"})
	if err != nil || len(searched) != 1 || searched[0].Target != "target-5" {
		t.Fatalf("search: %v %+v", err, searched)
	}

	total, err := st.CountEvents(EventQuery{Action: "deploy.start", Before: page1[0].ID})
	if err != nil || total != 3 {
		t.Fatalf("count ignores cursor: %v %d", err, total)
	}

	actions, actors, err := st.EventFacets()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || actions[0] != "agent.online" || actions[1] != "deploy.start" {
		t.Fatalf("actions: %+v", actions)
	}
	if len(actors) != 1 || actors[0] != "ui" {
		t.Fatalf("actors: %+v", actors)
	}

	limited, _, err := st.ListEventsPage(EventQuery{Limit: 999})
	if err != nil || len(limited) != 7 {
		t.Fatalf("limit clamp: %v %d", err, len(limited))
	}
}
