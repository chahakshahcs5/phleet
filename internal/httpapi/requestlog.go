package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"phleet/internal/model"
)

// responseRecorder captures what the project proxy sent upstream. It is only
// used for non-upgrade requests; WebSocket sessions bypass it because
// bridgeUpgrade hijacks the connection.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) requestLogLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case l := <-s.reqLogCh:
			if err := s.st.InsertRequestLog(l); err != nil {
				s.log.Debug("request log", "err", err)
			}
		}
	}
}

func (s *Server) logRequest(l model.RequestLog) {
	select {
	case s.reqLogCh <- l:
	default:
		// Observability must never back-pressure the proxy. Dropping is the
		// right failure mode under load.
	}
}

func websocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func requestIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			v = v[:i]
		}
		return strings.TrimSpace(v)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func requestPath(r *http.Request) string {
	p := r.URL.RequestURI()
	if len(p) > 512 {
		p = p[:512] + "…"
	}
	return p
}

func (s *Server) recordRequest(projectID, serverID string, r *http.Request, status int, bytes int64, started time.Time) {
	if status == 0 {
		status = http.StatusBadGateway
	}
	s.logRequest(model.RequestLog{
		ProjectID: projectID, ServerID: serverID, TS: time.Now().Unix(),
		Method: r.Method, Path: requestPath(r), Status: status, Bytes: bytes,
		DurationMS: time.Since(started).Milliseconds(),
		Host:       r.Host, IP: requestIP(r),
	})
}
