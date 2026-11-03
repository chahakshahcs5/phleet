//go:build linux

package agent

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func echoServer(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}

func dialEcho(t *testing.T, port int, msg string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func TestAppProxySwitchesBackend(t *testing.T) {
	a, closeA := echoServer(t)
	defer closeA()
	b, closeB := echoServer(t)
	defer closeB()

	proxy, err := newAppProxy(0)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.close()
	public := proxy.ln.Addr().(*net.TCPAddr).Port
	proxy.setTarget(a)
	if got := dialEcho(t, public, "one"); got != "one" {
		t.Fatalf("first backend = %q", got)
	}
	proxy.setTarget(b)
	if got := dialEcho(t, public, "two"); got != "two" {
		t.Fatalf("second backend = %q", got)
	}
}
