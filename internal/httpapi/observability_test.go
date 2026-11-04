package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestResponseRecorderCounts(t *testing.T) {
	rec := httptest.NewRecorder()
	r := &responseRecorder{ResponseWriter: rec}
	r.WriteHeader(201)
	_, _ = r.Write([]byte("hello"))
	if r.status != 201 || r.bytes != 5 {
		t.Fatalf("status=%d bytes=%d", r.status, r.bytes)
	}
	// A second Write must not overwrite the recorded status.
	r.WriteHeader(500)
	if r.status != 201 {
		t.Fatalf("status changed to %d", r.status)
	}
}
