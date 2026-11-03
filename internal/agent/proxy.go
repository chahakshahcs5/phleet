//go:build linux

package agent

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"phleet/internal/tunnel"
)

// appProxy keeps a stable public loopback port while the supervised app moves
// between internal ports during a blue-green deploy. Existing connections stay
// on their original backend; new connections use the current target.
type appProxy struct {
	publicPort int
	ln         net.Listener

	mu     sync.RWMutex
	target int

	closeOnce sync.Once
	done      chan struct{}
}

func newAppProxy(publicPort int) (*appProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(publicPort))
	if err != nil {
		return nil, fmt.Errorf("listen on public port %d: %w", publicPort, err)
	}
	p := &appProxy{publicPort: publicPort, ln: ln, done: make(chan struct{})}
	go p.accept()
	return p, nil
}

func (p *appProxy) setTarget(port int) {
	p.mu.Lock()
	p.target = port
	p.mu.Unlock()
}

func (p *appProxy) currentTarget() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

func (p *appProxy) close() {
	p.closeOnce.Do(func() {
		close(p.done)
		_ = p.ln.Close()
	})
}

func (p *appProxy) accept() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			select {
			case <-p.done:
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		go p.serve(conn)
	}
}

func (p *appProxy) serve(conn net.Conn) {
	target := p.currentTarget()
	if target <= 0 {
		_ = conn.Close()
		return
	}
	up, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(target), 3*time.Second)
	if err != nil {
		_ = conn.Close()
		return
	}
	tunnel.Bridge(conn, up)
}
