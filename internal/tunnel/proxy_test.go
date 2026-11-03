package tunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// TestProxyTCP exercises the raw TCP path used by the control-plane bridge and
// by standby replication: ProxyTCP -> yamux -> handleTCPStream -> local socket.
func TestProxyTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				c.Close()
			}()
		}
	}()

	a, b := net.Pipe()
	cpSess, err := yamux.Client(a, Config())
	if err != nil {
		t.Fatal(err)
	}
	defer cpSess.Close()
	agentSess, err := yamux.Server(b, Config())
	if err != nil {
		t.Fatal(err)
	}
	defer agentSess.Close()
	go ServeStreams(agentSess, func(p int) bool { return p == port })

	stream, err := ProxyTCP(cpSess, port, "", "tcp")
	if err != nil {
		t.Fatalf("proxy tcp: %v", err)
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("unexpected echo %q", buf)
	}
}

func TestProxyWebSocket(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			typ, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if err := conn.Write(context.Background(), typ, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	}))
	defer backend.Close()

	port := backend.Listener.Addr().(*net.TCPAddr).Port

	a, b := net.Pipe()
	clientSess, err := yamux.Client(a, Config())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSess.Close()
	serverSess, err := yamux.Server(b, Config())
	if err != nil {
		t.Fatal(err)
	}
	defer serverSess.Close()
	go ServeStreams(serverSess, func(p int) bool { return p == port })

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := ProxyHTTP(w, r, clientSess, port, ""); err != nil {
			t.Errorf("proxy: %v", err)
		}
	}))
	defer front.Close()

	wsURL := "ws" + strings.TrimPrefix(front.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText || string(data) != "echo:ping" {
		t.Fatalf("unexpected reply %d %q", typ, data)
	}
}

// TestProxyUpgradeRejected checks that a non-websocket upgrade still gets a
// clean error instead of hanging.
func TestProxyUpgradeRejected(t *testing.T) {
	a, b := net.Pipe()
	clientSess, _ := yamux.Client(a, Config())
	defer clientSess.Close()
	serverSess, _ := yamux.Server(b, Config())
	defer serverSess.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	r.Header.Set("Upgrade", "h2c")
	r.Header.Set("Connection", "Upgrade")

	if err := ProxyHTTP(w, r, clientSess, 1, ""); err != nil {
		t.Fatalf("proxy: %v", err)
	}
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", w.Code)
	}
	resp := w.Result()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "websocket") {
		t.Fatalf("unexpected body %q", body)
	}
}
