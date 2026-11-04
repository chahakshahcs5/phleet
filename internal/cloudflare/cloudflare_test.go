package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type dnsTestServer struct {
	records  []DNSRecord
	patched  int
	created  int
	lastBody map[string]any
}

func newDNSTestServer(t *testing.T, s *dnsTestServer) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			raw, _ := json.Marshal(s.records)
			_ = json.NewEncoder(w).Encode(envelope{Success: true, Result: raw})
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
		switch r.Method {
		case http.MethodPatch:
			s.patched++
		case http.MethodPost:
			s.created++
		}
		_ = json.NewEncoder(w).Encode(envelope{Success: true, Result: json.RawMessage(`{}`)})
	}))
	t.Cleanup(srv.Close)
	c := New("test-token")
	c.Base = srv.URL
	return c
}

func TestUpsertCNAMEUpdatesExistingCNAME(t *testing.T) {
	s := &dnsTestServer{records: []DNSRecord{{
		ID: "c1", Type: "CNAME", Name: "fleet.example.com", Content: "old.example", Proxied: true,
	}}}
	c := newDNSTestServer(t, s)
	result, err := c.UpsertCNAME(context.Background(), "zone", "fleet.example.com", "new.example")
	if err != nil {
		t.Fatal(err)
	}
	if result != "updated" || s.patched != 1 || s.created != 0 {
		t.Fatalf("result=%q patched=%d created=%d", result, s.patched, s.created)
	}
	if s.lastBody["content"] != "new.example" {
		t.Fatalf("patched body = %#v", s.lastBody)
	}
}

func TestUpsertCNAMEUnchanged(t *testing.T) {
	s := &dnsTestServer{records: []DNSRecord{{
		ID: "c1", Type: "CNAME", Name: "fleet.example.com", Content: "new.example", Proxied: true,
	}}}
	c := newDNSTestServer(t, s)
	result, err := c.UpsertCNAME(context.Background(), "zone", "fleet.example.com", "new.example")
	if err != nil {
		t.Fatal(err)
	}
	if result != "unchanged" || s.patched != 0 || s.created != 0 {
		t.Fatalf("result=%q patched=%d created=%d", result, s.patched, s.created)
	}
}

func TestUpsertCNAMERefusesOtherRecordType(t *testing.T) {
	s := &dnsTestServer{records: []DNSRecord{{
		ID: "a1", Type: "A", Name: "fleet.example.com", Content: "203.0.113.10", Proxied: true,
	}}}
	c := newDNSTestServer(t, s)
	_, err := c.UpsertCNAME(context.Background(), "zone", "fleet.example.com", "new.example")
	if err == nil || !strings.Contains(err.Error(), "existing A record") {
		t.Fatalf("err = %v, want existing A record error", err)
	}
	if s.patched != 0 || s.created != 0 {
		t.Fatalf("must not mutate other record types: patched=%d created=%d", s.patched, s.created)
	}
}

func TestUpsertCNAMECreatesWhenMissing(t *testing.T) {
	s := &dnsTestServer{}
	c := newDNSTestServer(t, s)
	result, err := c.UpsertCNAME(context.Background(), "zone", "fleet.example.com", "new.example")
	if err != nil {
		t.Fatal(err)
	}
	if result != "created" || s.created != 1 || s.patched != 0 {
		t.Fatalf("result=%q patched=%d created=%d", result, s.patched, s.created)
	}
}
