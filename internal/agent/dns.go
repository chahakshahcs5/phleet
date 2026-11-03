//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

var dnsDebug = os.Getenv("PHLEET_DNS_DEBUG") != ""

func dnsLogf(format string, args ...any) {
	if dnsDebug {
		fmt.Fprintf(os.Stderr, "phleet dns: "+format+"\n", args...)
	}
}

// Termux on Android has no /etc/resolv.conf and Go's pure resolver cannot use
// Android's netd DNS proxy. Direct IP traffic and TLS work, so we resolve
// hostnames with DNS-over-HTTPS (Cloudflare, then Google) and cache the answer.
type dohDialer struct {
	mu    sync.Mutex
	cache map[string]cacheEntry
	dial  net.Dialer
}

type cacheEntry struct {
	ips []net.IP
	exp time.Time
}

func newDoHHTTPClient() *http.Client {
	d := &dohDialer{
		cache: map[string]cacheEntry{},
		dial:  net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second},
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           d.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}

func (d *dohDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return d.dial.DialContext(ctx, network, addr)
	}
	if net.ParseIP(host) != nil {
		return d.dial.DialContext(ctx, network, addr)
	}
	ips := d.lookup(ctx, host)
	var lastErr error
	for _, ip := range ips {
		c, err := d.dial.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	// Last resort: the system resolver.
	if c, err := d.dial.DialContext(ctx, network, addr); err == nil {
		return c, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no addresses for %s", host)
	}
	return nil, lastErr
}

func (d *dohDialer) lookup(ctx context.Context, host string) []net.IP {
	d.mu.Lock()
	if e, ok := d.cache[host]; ok && time.Now().Before(e.exp) {
		d.mu.Unlock()
		return e.ips
	}
	d.mu.Unlock()

	ips := dohQuery(ctx, "https://1.1.1.1/dns-query", "1.1.1.1:443", host, "A")
	if len(ips) == 0 {
		ips = dohQuery(ctx, "https://dns.google/resolve", "8.8.8.8:443", host, "A")
	}
	if len(ips) == 0 {
		ips = dohQuery(ctx, "https://1.1.1.1/dns-query", "1.1.1.1:443", host, "AAAA")
	}
	dnsLogf("lookup %s -> %v", host, ips)
	if len(ips) > 0 {
		d.mu.Lock()
		d.cache[host] = cacheEntry{ips: ips, exp: time.Now().Add(5 * time.Minute)}
		d.mu.Unlock()
	} else {
		// Do not repeat three slow DoH queries on every reconnect attempt.
		d.mu.Lock()
		d.cache[host] = cacheEntry{exp: time.Now().Add(30 * time.Second)}
		d.mu.Unlock()
	}
	return ips
}

func dohQuery(parent context.Context, endpoint, dialAddr, host, qtype string) []net.IP {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "tcp", dialAddr)
		},
		TLSHandshakeTimeout: 6 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
	u := endpoint + "?name=" + url.QueryEscape(host) + "&type=" + url.QueryEscape(qtype)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := client.Do(req)
	if err != nil {
		dnsLogf("%s: %v", endpoint, err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		dnsLogf("%s: http %d", endpoint, resp.StatusCode)
		return nil
	}
	var body struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != 0 {
		dnsLogf("%s: decode status=%d err=%v", endpoint, body.Status, err)
		return nil
	}
	out := make([]net.IP, 0, len(body.Answer))
	for _, a := range body.Answer {
		if ip := net.ParseIP(a.Data); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}
