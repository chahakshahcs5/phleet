//go:build linux

package agent

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSetTokenPersistsForReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.token")
	a := &Agent{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		tokenPath: path,
		helloAck:  make(chan struct{}, 1),
	}
	a.setToken("device-token")
	if got := a.currentToken(); got != "device-token" {
		t.Fatalf("currentToken = %q", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "device-token\n" {
		t.Fatalf("token file = %q", b)
	}
}
