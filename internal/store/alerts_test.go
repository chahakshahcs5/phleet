package store

import "testing"

func TestAlertLifecycle(t *testing.T) {
	st := newTestStore(t)
	created, err := st.UpsertAlert("server:a:temp", "high_temp", "warning", "a", "too hot")
	if err != nil || !created {
		t.Fatalf("first upsert: %v created=%v", err, created)
	}
	created, err = st.UpsertAlert("server:a:temp", "high_temp", "critical", "a", "still hot")
	if err != nil || created {
		t.Fatalf("refresh should not create: %v created=%v", err, created)
	}
	n, _ := st.CountActiveAlerts()
	if n != 1 {
		t.Fatalf("active = %d", n)
	}
	changed, err := st.ResolveAlert("server:a:temp")
	if err != nil || !changed {
		t.Fatalf("resolve: %v changed=%v", err, changed)
	}
	if changed, _ := st.ResolveAlert("server:a:temp"); changed {
		t.Fatal("resolving twice should be a no-op")
	}
	if n, _ := st.CountActiveAlerts(); n != 0 {
		t.Fatalf("active after resolve = %d", n)
	}
	created, _ = st.UpsertAlert("server:a:temp", "high_temp", "critical", "a", "hot again")
	if !created {
		t.Fatal("expected a reopened alert")
	}
	if err := st.AckAlert(1); err != nil {
		t.Fatal(err)
	}
	alerts, err := st.ListAlerts(true, 10)
	if err != nil || len(alerts) != 1 || alerts[0].Status != "ack" {
		t.Fatalf("list: %v %+v", err, alerts)
	}
}
