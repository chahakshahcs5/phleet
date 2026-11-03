package tunnel

import (
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

// Config returns the yamux settings used on both ends. Keepalive frames travel
// over the WebSocket and also keep Cloudflare's idle timeout from firing.
func Config() *yamux.Config {
	c := yamux.DefaultConfig()
	c.LogOutput = io.Discard
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 15 * time.Second
	c.ConnectionWriteTimeout = 20 * time.Second
	c.MaxStreamWindowSize = 1024 * 1024
	c.StreamOpenTimeout = 30 * time.Second
	c.StreamCloseTimeout = 30 * time.Second
	return c
}
