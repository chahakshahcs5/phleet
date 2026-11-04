package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostGuardCoversPanelAndPublicURLOnly(t *testing.T) {
	s := testServer(t)
	s.SetEdge("https://panel.example.com", "fleet.example.com")
	h := s.hostGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		host string
		want int
	}{
		{"fleet.example.com", http.StatusNoContent},
		{"panel.example.com", http.StatusNoContent},
		{"localhost:8080", http.StatusNoContent},
		{"127.0.0.1:8080", http.StatusNoContent},
		{"memos.example.com", http.StatusNotFound},
		{"evil.example.net", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example/", nil)
		req.Host = tc.host
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("host %q = %d, want %d", tc.host, rr.Code, tc.want)
		}
	}
}
