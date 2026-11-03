//go:build linux

package agent

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/yamux"

	"phleet/internal/tunnel"
)

func TestServeFileStream(t *testing.T) {
	dir := t.TempDir()
	content := []byte("streamed file contents")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		home:  dir,
		roots: []string{dir},
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	c1, c2 := net.Pipe()
	client, err := yamux.Client(c1, tunnel.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := yamux.Server(c2, tunnel.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go tunnel.ServeStreamsWithFiles(server, nil, a.serveFileStream)

	stream, err := client.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := tunnel.WriteFrame(stream, tunnel.RequestHeader{Kind: "fs", Path: "hello.txt"}); err != nil {
		t.Fatal(err)
	}
	var res tunnel.ResponseHeader
	if err := tunnel.ReadFrame(stream, &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.ContentLength != int64(len(content)) {
		t.Fatalf("response = %+v", res)
	}
	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("body = %q", got)
	}
}
