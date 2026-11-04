//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"phleet/internal/agentproto"
)

func TestAppLogsOffsets(t *testing.T) {
	home := t.TempDir()
	a := &Agent{home: home}
	logPath := filepath.Join(home, "apps", "demo", "logs", "app.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := a.appLogs(agentproto.AppLogsRequest{Slug: "demo", Bytes: 4, FromStart: true})
	if err != nil || string(res.Data) != "0123" || res.Next != 4 || res.Size != 10 {
		t.Fatalf("from start: %v %+v %q", err, res, res.Data)
	}
	res, err = a.appLogs(agentproto.AppLogsRequest{Slug: "demo", Bytes: 4, Offset: 4})
	if err != nil || string(res.Data) != "4567" || res.Next != 8 || res.Rotated {
		t.Fatalf("from offset: %v %+v %q", err, res, res.Data)
	}
	res, err = a.appLogs(agentproto.AppLogsRequest{Slug: "demo", Bytes: 4})
	if err != nil || string(res.Data) != "6789" || res.Offset != 6 || !res.Truncated {
		t.Fatalf("tail: %v %+v %q", err, res, res.Data)
	}
	res, err = a.appLogs(agentproto.AppLogsRequest{Slug: "demo", Bytes: 4, Offset: 20})
	if err != nil || !res.Rotated || string(res.Data) != "0123" || res.Next != 4 {
		t.Fatalf("rotation: %v %+v %q", err, res, res.Data)
	}
}

func TestProcUsageForCurrentProcess(t *testing.T) {
	u, err := readProcUsage(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if u.pgrp <= 0 || u.rss <= 0 {
		t.Fatalf("unexpected usage: %+v", u)
	}
	groups := scanProcessGroups()
	if _, ok := groups[u.pgrp]; !ok {
		t.Fatalf("process group %d missing from scan", u.pgrp)
	}
}
