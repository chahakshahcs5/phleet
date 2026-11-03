package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phleet/internal/config"
	"phleet/internal/model"
	"phleet/internal/secrets"
	"phleet/internal/store"
	"phleet/internal/totp"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(s.Close)
	return s
}

func TestAgentTokenCannotAuthenticatePanelAPI(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+s.cfg.AgentToken)
	if s.panelAuthed(req) {
		t.Fatal("agent token must not authenticate the panel API")
	}
	if !s.agentTokenAuthed(req) {
		t.Fatal("agent token must authenticate agent-only endpoints")
	}

	admin := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	admin.Header.Set("Authorization", "Bearer "+s.cfg.AdminToken)
	if !s.panelAuthed(admin) {
		t.Fatal("admin token must authenticate the panel API")
	}
}

func TestGuardRejectsCrossSiteMutations(t *testing.T) {
	s := testServer(t)
	called := false
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name   string
		host   string
		origin string
		site   string
		want   int
	}{
		{"same origin", "fleet.example.com", "https://fleet.example.com", "same-origin", http.StatusNoContent},
		{"sibling app", "fleet.example.com", "https://memos.example.com", "same-site", http.StatusForbidden},
		{"external", "fleet.example.com", "https://evil.example", "cross-site", http.StatusForbidden},
		{"cross-site fetch", "fleet.example.com", "", "cross-site", http.StatusForbidden},
		{"non-browser client", "fleet.example.com", "", "", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/projects", strings.NewReader("{}"))
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
			if tc.want == http.StatusNoContent && !called {
				t.Fatal("handler was not called for an allowed request")
			}
		})
	}
}

func TestReadJSONRequiresJSONContentType(t *testing.T) {
	var out map[string]any
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Content-Type", "text/plain")
	if err := readJSON(req, &out, 1024); err == nil {
		t.Fatal("text/plain JSON must be rejected")
	}

	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if err := readJSON(req, &out, 1024); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	if out["ok"] != true {
		t.Fatalf("decoded value = %#v", out)
	}
}

func TestAgentTokenRouteScope(t *testing.T) {
	s := testServer(t)
	if err := s.st.CreateProject(model.Project{
		ID: "p-src", Name: "Source", Slug: "source", Kind: "app", Replicas: 1,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.st.UpsertServer(model.Server{ID: "phone-src", Name: "Source Phone"}); err != nil {
		t.Fatalf("create server: %v", err)
	}
	if err := s.st.UpsertPlacement(model.Placement{
		ProjectID: "p-src", ServerID: "phone-src", Port: 4100, Status: "running",
	}); err != nil {
		t.Fatalf("create placement: %v", err)
	}
	bundle := s.sourceBundlePath("p-src")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	get := func(path, token string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Phleet-Server", "phone-src")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/api/projects", s.cfg.AgentToken); code != http.StatusUnauthorized {
		t.Fatalf("agent token on panel API = %d, want 401", code)
	}
	if code := get("/api/projects/p-src/source", s.cfg.AgentToken); code != http.StatusOK {
		t.Fatalf("agent token on source download = %d, want 200", code)
	}
	if code := get("/api/projects", s.cfg.AdminToken); code != http.StatusOK {
		t.Fatalf("admin token on panel API = %d, want 200", code)
	}
}

func TestPerDeviceAgentEnrollment(t *testing.T) {
	s := testServer(t)
	if err := s.st.UpsertServer(model.Server{ID: "device-1", Name: "Device"}); err != nil {
		t.Fatal(err)
	}
	ok, device := s.agentHelloAuth(s.cfg.AgentToken, "device-1", true)
	if !ok || device == "" {
		t.Fatalf("first enrollment: ok=%v token=%q", ok, device)
	}
	s.setPendingDeviceToken("device-1", device)
	s.adoptPendingDeviceToken("device-1")

	if ok, _ := s.agentHelloAuth(s.cfg.AgentToken, "device-1", true); ok {
		t.Fatal("shared token must not re-enroll an enrolled device")
	}
	if ok, _ := s.agentHelloAuth(device, "device-1", true); !ok {
		t.Fatal("device token must be accepted for its own server")
	}
	if s.agentServerAuthed(device, "device-2") {
		t.Fatal("device token must not authorize another server")
	}
	if !s.agentServerAuthed(device, "device-1") {
		t.Fatal("device token must authorize its own server")
	}
	if ok, tok := s.agentHelloAuth(s.cfg.AgentToken, "legacy-1", false); !ok || tok != "" {
		t.Fatalf("legacy agent should connect without enrollment: ok=%v token=%q", ok, tok)
	}
}

func TestViewerAPITokenIsReadOnly(t *testing.T) {
	s := testServer(t)
	if err := s.st.CreateAPIToken(model.APIToken{
		ID: "viewer-token", Name: "watcher", Role: "viewer",
	}, tokenHash("viewer-secret")); err != nil {
		t.Fatal(err)
	}
	if err := s.st.CreateAPIToken(model.APIToken{
		ID: "admin-token", Name: "automation", Role: "admin",
	}, tokenHash("admin-secret")); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr.Code
	}
	if code := call(http.MethodGet, "/api/projects", "viewer-secret"); code != http.StatusOK {
		t.Fatalf("viewer read = %d", code)
	}
	if code := call(http.MethodPost, "/api/projects", "viewer-secret"); code != http.StatusForbidden {
		t.Fatalf("viewer mutation = %d", code)
	}
	if code := call(http.MethodGet, "/api/setup", "viewer-secret"); code != http.StatusForbidden {
		t.Fatalf("viewer secret endpoint = %d", code)
	}
	if code := call(http.MethodGet, "/api/api-tokens", "viewer-secret"); code != http.StatusForbidden {
		t.Fatalf("viewer token list = %d", code)
	}
	if code := call(http.MethodGet, "/api/projects", "admin-secret"); code != http.StatusOK {
		t.Fatalf("admin API token read = %d", code)
	}
}

func TestAdminTOTPIsRequired(t *testing.T) {
	s := testServer(t)
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := secrets.Encrypt(s.cfg.SessionKey, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.st.SetSetting(settingTOTPSecret, enc); err != nil {
		t.Fatal(err)
	}
	login := func(code string) int {
		body := `{"token":"` + s.cfg.AdminToken + `","code":"` + code + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr.Code
	}
	if code := login("abcdef"); code != http.StatusUnauthorized {
		t.Fatalf("wrong TOTP = %d", code)
	}
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status := login(code); status != http.StatusOK {
		t.Fatalf("correct TOTP = %d", status)
	}
}

func TestMetricsRequiresPanelAuth(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+s.cfg.AgentToken)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("agent token on /metrics = %d, want 401", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+s.cfg.AdminToken)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "phleet_agents_online") {
		t.Fatalf("admin metrics = %d %q", rr.Code, rr.Body.String())
	}
}

func TestSlugUniquenessSpansWorkloads(t *testing.T) {
	s := testServer(t)
	if err := s.st.CreateProject(model.Project{
		ID: "p1", Name: "Memos", Slug: "memos", Kind: "app", Replicas: 1,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if got, err := s.uniqueServiceSlug("Memos"); err != nil || got == "memos" {
		t.Fatalf("service slug = %q, err = %v; want a non-colliding slug", got, err)
	}
	if got, err := s.uniqueSlug("memos"); err != nil || got == "memos" {
		t.Fatalf("project slug = %q, err = %v; want a non-colliding slug", got, err)
	}
	if err := s.st.CreateProject(model.Project{
		ID: "p2", Name: "DB", Slug: "db-postgres", Kind: "app", Replicas: 1,
	}); err != nil {
		t.Fatalf("create second project: %v", err)
	}
	if got, err := s.uniqueAddonSlug("postgres"); err != nil || got == "db-postgres" {
		t.Fatalf("addon slug = %q, err = %v; want a non-colliding slug", got, err)
	}
}
