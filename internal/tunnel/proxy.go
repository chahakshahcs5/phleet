package tunnel

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	maxHeader  = 1 << 20
	maxBody    = 32 << 20
	reqTimeout = 5 * time.Minute
)

type RequestHeader struct {
	// Kind selects the stream mode: "" (HTTP), "tcp" (raw port forward) or
	// "relay" (agent → control plane → another agent).
	Kind          string              `json:"kind,omitempty"`
	AddonID       string              `json:"addonId,omitempty"`
	Port          int                 `json:"port"`
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	Host          string              `json:"host"`
	Headers       map[string][]string `json:"headers"`
	ContentLength int64               `json:"contentLength"`
	RemoteAddr    string              `json:"remoteAddr"`
	Upgrade       bool                `json:"upgrade,omitempty"`
}

type ResponseHeader struct {
	Status        int                 `json:"status"`
	Headers       map[string][]string `json:"headers"`
	ContentLength int64               `json:"contentLength"`
	Error         string              `json:"error,omitempty"`
}

func writeFramed(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxHeader {
		return errors.New("header too large")
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readFramed(r io.Reader, v any) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > maxHeader {
		return fmt.Errorf("invalid header length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ReadFrame and WriteFrame expose the stream framing to the control plane's
// relay handler without duplicating the wire format.
func ReadFrame(r io.Reader, v any) error  { return readFramed(r, v) }
func WriteFrame(w io.Writer, v any) error { return writeFramed(w, v) }

var hopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"content-length":      true,
	// 100-continue is a connection-level handshake. We stream bodies straight
	// through, so the origin must not send an interim response the framing
	// does not expect.
	"expect": true,
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, v := range r.Header.Values("Connection") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
				return true
			}
		}
	}
	return false
}

// upgradeHeaders keeps the hop-by-hop headers a protocol switch needs while
// still dropping the ones that only make sense on a single connection.
func upgradeHeaders(in http.Header, stripCookie string) map[string][]string {
	out := cleanHeaders(in, stripCookie)
	for _, k := range []string{"Connection", "Upgrade"} {
		if vs := in.Values(k); len(vs) > 0 {
			out[k] = vs
		}
	}
	return out
}

func cleanHeaders(in http.Header, stripCookie string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, vs := range in {
		lk := strings.ToLower(k)
		if hopHeaders[lk] {
			continue
		}
		if lk == "cookie" && stripCookie != "" {
			filtered := make([]string, 0, len(vs))
			for _, v := range vs {
				parts := strings.Split(v, ";")
				kept := make([]string, 0, len(parts))
				for _, part := range parts {
					name := strings.TrimSpace(part)
					if strings.HasPrefix(name, stripCookie+"=") {
						continue
					}
					if strings.TrimSpace(part) != "" {
						kept = append(kept, strings.TrimSpace(part))
					}
				}
				if len(kept) > 0 {
					filtered = append(filtered, strings.Join(kept, "; "))
				}
			}
			if len(filtered) > 0 {
				out["Cookie"] = filtered
			}
			continue
		}
		out[k] = vs
	}
	return out
}

// ProxyOptions tunes request buffering. Storage hosts stream large S3 bodies
// instead of buffering them; regular apps keep the bounded behaviour.
type ProxyOptions struct {
	MaxBody int64
	Stream  bool
}

// ProxyHTTP forwards one request through an agent tunnel stream and streams the
// response back. Body handling is bounded by default; ProxyHTTPOpts can stream
// a body of known length directly (S3 PUTs) without buffering it in memory.
func ProxyHTTP(w http.ResponseWriter, r *http.Request, sess *yamux.Session, port int, stripCookie string) error {
	return ProxyHTTPOpts(w, r, sess, port, stripCookie, ProxyOptions{})
}

func ProxyHTTPOpts(w http.ResponseWriter, r *http.Request, sess *yamux.Session, port int, stripCookie string, opts ProxyOptions) error {
	if sess == nil {
		return ErrNoTunnel
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = maxBody
	}
	upgrade := isWebSocketUpgrade(r)
	if r.Header.Get("Upgrade") != "" && !upgrade {
		http.Error(w, "only websocket upgrades are supported through phleet", http.StatusNotImplemented)
		return nil
	}
	if upgrade {
		if _, ok := w.(http.Hijacker); !ok {
			http.Error(w, "websocket upgrades need an HTTP/1.1 client", http.StatusNotImplemented)
			return nil
		}
	}
	if r.ContentLength > opts.MaxBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return nil
	}
	streaming := opts.Stream && !upgrade && r.ContentLength >= 0
	var body []byte
	if !streaming && r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, opts.MaxBody+1))
		if err != nil {
			return err
		}
		if int64(len(body)) > opts.MaxBody {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil
		}
	}
	stream, err := sess.OpenStream()
	if err != nil {
		return err
	}
	defer stream.Close()
	if upgrade || streaming {
		// Upgraded and raw-stream connections are long-lived; a wall-clock
		// deadline would tear them down mid-transfer.
		_ = stream.SetDeadline(time.Time{})
	} else {
		_ = stream.SetDeadline(time.Now().Add(reqTimeout))
	}

	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	headers := cleanHeaders(r.Header, stripCookie)
	if upgrade {
		headers = upgradeHeaders(r.Header, stripCookie)
	}
	length := int64(len(body))
	if streaming {
		length = r.ContentLength
	}
	hdr := RequestHeader{
		Port:          port,
		Method:        r.Method,
		Path:          r.URL.RequestURI(),
		Host:          host,
		Headers:       headers,
		ContentLength: length,
		RemoteAddr:    r.RemoteAddr,
		Upgrade:       upgrade,
	}
	if err := writeFramed(stream, hdr); err != nil {
		return err
	}
	if streaming && length > 0 {
		if _, err := io.CopyN(stream, r.Body, length); err != nil {
			return err
		}
	} else if len(body) > 0 {
		if _, err := stream.Write(body); err != nil {
			return err
		}
	}

	var res ResponseHeader
	if err := readFramed(stream, &res); err != nil {
		return err
	}
	if res.Error != "" {
		http.Error(w, "app error: "+res.Error, http.StatusBadGateway)
		return nil
	}
	if upgrade && res.Status == http.StatusSwitchingProtocols {
		return bridgeUpgrade(w, stream, res)
	}
	for k, vs := range res.Headers {
		if hopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if res.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(res.ContentLength, 10))
	}
	w.WriteHeader(res.Status)
	_, err = io.Copy(w, stream)
	return err
}

// ProxyTCP opens a raw byte stream to an agent-side TCP port. Kind is "tcp"
// for a direct port forward and "relay" when another agent is asking the
// control plane to reach an addon. The returned stream has no deadline.
func ProxyTCP(sess *yamux.Session, port int, addonID, kind string) (*yamux.Stream, error) {
	if sess == nil {
		return nil, ErrNoTunnel
	}
	stream, err := sess.OpenStream()
	if err != nil {
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	hdr := RequestHeader{Kind: kind, Port: port, AddonID: addonID}
	if err := writeFramed(stream, hdr); err != nil {
		_ = stream.Close()
		return nil, err
	}
	var res ResponseHeader
	if err := readFramed(stream, &res); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if res.Status != http.StatusOK {
		_ = stream.Close()
		msg := res.Error
		if msg == "" {
			msg = fmt.Sprintf("agent returned %d", res.Status)
		}
		return nil, errors.New(msg)
	}
	return stream, nil
}

// OpenRelay opens a relay stream from an agent to the control plane. The
// control plane validates the addon and connects it to whichever agent holds
// the current primary, then answers with a 200 before bytes flow.
func OpenRelay(sess *yamux.Session, addonID string) (*yamux.Stream, error) {
	if sess == nil {
		return nil, ErrNoTunnel
	}
	stream, err := sess.OpenStream()
	if err != nil {
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	if err := writeFramed(stream, RequestHeader{Kind: "relay", AddonID: addonID}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	var res ResponseHeader
	if err := readFramed(stream, &res); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if res.Status != http.StatusOK {
		_ = stream.Close()
		msg := res.Error
		if msg == "" {
			msg = fmt.Sprintf("control plane returned %d", res.Status)
		}
		return nil, errors.New(msg)
	}
	return stream, nil
}

// Bridge copies bytes in both directions until either side closes.
func Bridge(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// bridgeUpgrade hijacks the downstream connection and copies bytes in both
// directions until either side closes.
func bridgeUpgrade(w http.ResponseWriter, stream *yamux.Stream, res ResponseHeader) error {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket upgrades need an HTTP/1.1 client", http.StatusNotImplemented)
		return nil
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Time{})

	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		return err
	}
	for k, vs := range res.Headers {
		lk := strings.ToLower(k)
		if hopHeaders[lk] && lk != "connection" && lk != "upgrade" {
			continue
		}
		for _, v := range vs {
			if _, err := fmt.Fprintf(rw, "%s: %s\r\n", k, v); err != nil {
				return err
			}
		}
	}
	if _, err := rw.WriteString("\r\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stream, rw.Reader)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, stream)
		done <- struct{}{}
	}()
	<-done
	// Closing both ends unblocks the remaining copy goroutine.
	_ = stream.Close()
	_ = conn.Close()
	<-done
	return nil
}

// ServeStreams handles streams accepted by the agent-side yamux session.
// allowedPort must confirm the port belongs to a supervised app.
func ServeStreams(sess *yamux.Session, allowedPort func(int) bool) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go handleStream(stream, allowedPort)
	}
}

func handleStream(stream *yamux.Stream, allowedPort func(int) bool) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(reqTimeout))

	var req RequestHeader
	if err := readFramed(stream, &req); err != nil {
		return
	}
	if req.Port <= 0 || (allowedPort != nil && !allowedPort(req.Port)) {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "port is not managed by phleet"})
		return
	}
	switch req.Kind {
	case "relay":
		// Relay frames terminate at the control plane, never on an agent.
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "relay streams are not accepted here"})
		return
	case "tcp":
		handleTCPStream(stream, req)
		return
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", req.Port), 5*time.Second)
	if err != nil {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "app is not listening on port"})
		return
	}
	defer conn.Close()

	if req.Upgrade {
		handleUpgradeStream(stream, conn, req)
		return
	}
	_ = conn.SetDeadline(time.Now().Add(reqTimeout))

	httpReq := &http.Request{
		Method:        req.Method,
		URL:           mustURL(req.Path),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Host:          req.Host,
		Header:        http.Header(req.Headers),
		ContentLength: req.ContentLength,
	}
	if req.ContentLength > 0 {
		httpReq.Body = io.NopCloser(io.LimitReader(stream, req.ContentLength))
	}
	httpReq.Header.Set("X-Forwarded-Host", req.Host)
	httpReq.Header.Set("X-Forwarded-Proto", "https")
	httpReq.Header.Set("X-Real-IP", req.RemoteAddr)
	if err := httpReq.Write(conn); err != nil {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "could not write request"})
		return
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, httpReq)
	if err != nil {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "invalid response from app"})
		return
	}
	defer resp.Body.Close()

	out := ResponseHeader{
		Status:        resp.StatusCode,
		Headers:       cleanHeaders(resp.Header, ""),
		ContentLength: resp.ContentLength,
	}
	if err := writeFramed(stream, out); err != nil {
		return
	}
	_, _ = io.Copy(stream, resp.Body)
}

// handleTCPStream bridges a tunnel stream to a loopback TCP port. Used by the
// control-plane bridge (localhost → phone) and by the relay path.
func handleTCPStream(stream *yamux.Stream, req RequestHeader) {
	_ = stream.SetDeadline(time.Time{})
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", req.Port), 5*time.Second)
	if err != nil {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "port is not listening"})
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if err := writeFramed(stream, ResponseHeader{Status: 200, ContentLength: -1}); err != nil {
		_ = conn.Close()
		return
	}
	Bridge(stream, conn)
}

// handleUpgradeStream performs the HTTP/1.1 protocol switch with the app and
// then bridges the tunnel stream and the app socket as raw bytes.
func handleUpgradeStream(stream *yamux.Stream, conn net.Conn, req RequestHeader) {
	_ = stream.SetDeadline(time.Time{})
	_ = conn.SetDeadline(time.Time{})

	httpReq := &http.Request{
		Method:        req.Method,
		URL:           mustURL(req.Path),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Host:          req.Host,
		Header:        http.Header(req.Headers),
		ContentLength: 0,
	}
	httpReq.Header.Set("X-Forwarded-Host", req.Host)
	httpReq.Header.Set("X-Forwarded-Proto", "https")
	httpReq.Header.Set("X-Real-IP", req.RemoteAddr)
	if err := httpReq.Write(conn); err != nil {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "could not write upgrade request"})
		return
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, httpReq)
	if err != nil {
		_ = writeFramed(stream, ResponseHeader{Status: 502, Error: "invalid response from app"})
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		out := ResponseHeader{
			Status:        resp.StatusCode,
			Headers:       cleanHeaders(resp.Header, ""),
			ContentLength: resp.ContentLength,
		}
		if err := writeFramed(stream, out); err != nil {
			return
		}
		_, _ = io.Copy(stream, resp.Body)
		return
	}

	out := ResponseHeader{
		Status:        resp.StatusCode,
		Headers:       upgradeHeaders(resp.Header, ""),
		ContentLength: -1,
	}
	if err := writeFramed(stream, out); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, stream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(stream, br)
		done <- struct{}{}
	}()
	<-done
	_ = stream.Close()
	_ = conn.Close()
	<-done
}

func mustURL(path string) *url.URL {
	u, err := url.ParseRequestURI(path)
	if err != nil {
		return &url.URL{Path: "/"}
	}
	return u
}
