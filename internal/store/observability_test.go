package store

import (
	"testing"
	"time"

	"phleet/internal/model"
)

func TestAppMetricsAndRequestLogs(t *testing.T) {
	st := newTestStore(t)
	now := (time.Now().Unix()/30)*30 + 30
	for i, cpu := range []float64{10, 20, 30} {
		if err := st.InsertAppMetric("p1", "phone-a", now+int64(i*15), cpu, int64(100+i*10)); err != nil {
			t.Fatal(err)
		}
	}
	samples, err := st.QueryAppMetrics("p1", now-1, 30)
	if err != nil || len(samples) != 2 {
		t.Fatalf("app metrics: %v %+v", err, samples)
	}
	if samples[0].CPUPercent != 15 || samples[0].RSS != 105 {
		t.Fatalf("unexpected first bucket: %+v", samples[0])
	}
	if err := st.PruneAppMetrics(now + 100); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.QueryAppMetrics("p1", 0, 1); len(left) != 0 {
		t.Fatalf("app metrics not pruned: %+v", left)
	}

	for i := 0; i < 3; i++ {
		if err := st.InsertRequestLog(model.RequestLog{
			ProjectID: "p1", ServerID: "phone-a", TS: now + int64(i),
			Method: "GET", Path: "/x", Status: 200, Bytes: 5, DurationMS: 2, Host: "app.example.com",
		}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := st.ListRequestLogs("p1", 2, now-1)
	if err != nil || len(logs) != 2 {
		t.Fatalf("request logs: %v %+v", err, logs)
	}
	if logs[0].TS < logs[1].TS {
		t.Fatalf("request logs are not newest first: %+v", logs)
	}
	if err := st.PruneRequestLogs(now + 100); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.ListRequestLogs("p1", 10, 0); len(left) != 0 {
		t.Fatalf("request logs not pruned: %+v", left)
	}
}

func TestProjectSecretsAndUptime(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetProjectSecret("p1", "a.json", "cipher"); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListProjectSecrets("p1")
	if err != nil || len(list) != 1 || list[0].Name != "a.json" || list[0].ContentEnc != "cipher" {
		t.Fatalf("secrets: %v %+v", err, list)
	}
	if err := st.DeleteProjectSecret("p1", "a.json"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListProjectSecrets("p1"); len(list) != 0 {
		t.Fatalf("secret not deleted: %+v", list)
	}

	if err := st.SetUptimeStatus(model.UptimeStatus{ProjectID: "p1", CheckedAt: 100, OK: true, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetUptimeStatus("p1")
	if err != nil || !u.OK || u.StatusCode != 200 || u.CheckedAt != 100 {
		t.Fatalf("uptime: %v %+v", err, u)
	}
	if all, err := st.ListUptimeStatuses(); err != nil || len(all) != 1 {
		t.Fatalf("uptime list: %v %+v", err, all)
	}
}
