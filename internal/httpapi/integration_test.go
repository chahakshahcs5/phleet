package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"phleet/internal/agentproto"
	"phleet/internal/config"
	"phleet/internal/httpapi"
	"phleet/internal/model"
	"phleet/internal/store"
	"phleet/internal/tunnel"
)

type harness struct {
	api   *httpapi.Server
	srv   *httptest.Server
	cfg   *config.Config
	store *store.Store

	deviceMu sync.Mutex
	device   map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := httpapi.New(cfg, st, log)
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(func() {
		api.Close()
		ts.Close()
		st.Close()
	})
	return &harness{api: api, srv: ts, cfg: cfg, store: st, device: map[string]string{}}
}

func (h *harness) tokenFor(serverID string) string {
	h.deviceMu.Lock()
	defer h.deviceMu.Unlock()
	if tok := h.device[serverID]; tok != "" {
		return tok
	}
	return h.cfg.AgentToken
}

func (h *harness) rememberToken(serverID, tok string) {
	if tok == "" {
		return
	}
	h.deviceMu.Lock()
	h.device[serverID] = tok
	h.deviceMu.Unlock()
}

func (h *harness) wsURL() string {
	return "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/agent"
}

func (h *harness) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.AdminToken)
	req.Header.Set("Content-Type", "application/json")
	return h.doRequest(t, req, out)
}

func (h *harness) doWithHeaders(t *testing.T, method, path string, body []byte, headers map[string]string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return h.doRequest(t, req, out)
}

func (h *harness) doRequest(t *testing.T, req *http.Request, out any) int {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func dialAgent(t *testing.T, h *harness, id string) *websocket.Conn {
	t.Helper()
	return dialAgentCaps(t, h, id, map[string]any{"root": false})
}

func dialAgentCaps(t *testing.T, h *harness, id string, caps map[string]any) *websocket.Conn {
	t.Helper()
	if caps == nil {
		caps = map[string]any{}
	}
	if _, ok := caps["deviceTokens"]; !ok {
		caps["deviceTokens"] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + h.tokenFor(id)}},
	})
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "test done") })

	hello := agentproto.Hello{
		ID: id, Name: "Test Phone", Hostname: "test-host", OS: "linux/android-14",
		Arch: "arm64", AgentVersion: "test", Caps: caps,
	}
	writeEnv(t, ws, agentproto.TypeHello, "", hello)

	actx, acancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, data, err := ws.Read(actx)
	acancel()
	if err != nil {
		t.Fatalf("read hello.ack: %v", err)
	}
	var env agentproto.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type != agentproto.TypeHelloAck {
		t.Fatalf("expected hello.ack, got %s (%v)", env.Type, err)
	}
	var ack agentproto.HelloAck
	if err := json.Unmarshal(env.Data, &ack); err != nil || !ack.OK {
		t.Fatalf("bad hello.ack: %v", err)
	}
	h.rememberToken(id, ack.Token)
	return ws
}

func writeEnv(t *testing.T, ws *websocket.Conn, typ, replyTo string, data any) {
	t.Helper()
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	out, err := json.Marshal(agentproto.Envelope{Type: typ, ReplyTo: replyTo, Data: raw})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, out); err != nil {
		t.Fatalf("write %s: %v", typ, err)
	}
}

func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAgentRegistrationHeartbeatAndExec(t *testing.T) {
	h := newHarness(t)
	ws := dialAgent(t, h, "phone-test")

	// The agent answers exec requests like the real one does.
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, data, err := ws.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			var env agentproto.Envelope
			if json.Unmarshal(data, &env) != nil || env.Type != agentproto.TypeExec {
				continue
			}
			var req agentproto.ExecRequest
			_ = json.Unmarshal(env.Data, &req)
			res, _ := json.Marshal(agentproto.ExecResponse{Output: "ran: " + req.Command, ExitCode: 0})
			out, _ := json.Marshal(agentproto.Envelope{Type: "reply", ReplyTo: env.ID, Data: res})
			wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = ws.Write(wctx, websocket.MessageText, out)
			wcancel()
		}
	}()

	var list struct {
		Servers []struct {
			model.Server
			Online bool `json:"online"`
		} `json:"servers"`
	}
	waitFor(t, "agent online", func() bool {
		list.Servers = nil
		code := h.do(t, http.MethodGet, "/api/servers", nil, &list)
		return code == 200 && len(list.Servers) == 1 && list.Servers[0].Online
	})
	if list.Servers[0].Name != "Test Phone" {
		t.Fatalf("unexpected name: %q", list.Servers[0].Name)
	}

	writeEnv(t, ws, agentproto.TypeHeartbeat, "", agentproto.Heartbeat{
		Metrics: agentproto.Metrics{CPUPercent: 12.5, MemUsed: 200, MemTotal: 1000, TempC: 42.5, BatteryPct: 77, At: time.Now().Unix()},
	})
	waitFor(t, "heartbeat stored", func() bool {
		var resp struct {
			Server struct {
				Latest *model.Sample `json:"latest"`
			} `json:"server"`
		}
		code := h.do(t, http.MethodGet, "/api/servers/phone-test", nil, &resp)
		return code == 200 && resp.Server.Latest != nil && resp.Server.Latest.TempC == 42.5
	})

	var execRes agentproto.ExecResponse
	if code := h.do(t, http.MethodPost, "/api/servers/phone-test/exec", map[string]any{"command": "uname -a"}, &execRes); code != 200 {
		t.Fatalf("exec status %d", code)
	}
	if execRes.Output != "ran: uname -a" {
		t.Fatalf("unexpected exec output: %+v", execRes)
	}

	// Disconnect marks the server offline.
	ws.Close(websocket.StatusNormalClosure, "bye")
	waitFor(t, "agent offline", func() bool {
		list.Servers = nil
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		return len(list.Servers) == 1 && !list.Servers[0].Online
	})

	var offlineEvents struct {
		Events []model.Event `json:"events"`
	}
	waitFor(t, "offline event", func() bool {
		offlineEvents.Events = nil
		_ = h.do(t, http.MethodGet, "/api/events?limit=20&action=agent.offline", nil, &offlineEvents)
		return len(offlineEvents.Events) > 0
	})
	if !strings.Contains(offlineEvents.Events[0].Detail, "Test Phone") ||
		!strings.Contains(offlineEvents.Events[0].Detail, "connection closed") {
		t.Fatalf("offline event has no device detail: %q", offlineEvents.Events[0].Detail)
	}
}

func TestAgentReconnectKeepsServerOnline(t *testing.T) {
	h := newHarness(t)
	_ = dialAgent(t, h, "phone-flap")

	waitFor(t, "first connection online", func() bool {
		var list struct {
			Servers []struct {
				model.Server
				Online bool `json:"online"`
			} `json:"servers"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		return len(list.Servers) == 1 && list.Servers[0].Online
	})

	// Registering a replacement connection closes the old one. That must not
	// be treated as the phone going offline, even after the grace period.
	_ = dialAgent(t, h, "phone-flap")
	time.Sleep(7 * time.Second)

	var got struct {
		Server struct {
			model.Server
			Online bool `json:"online"`
		} `json:"server"`
	}
	if code := h.do(t, http.MethodGet, "/api/servers/phone-flap", nil, &got); code != 200 || !got.Server.Online {
		t.Fatalf("replacement connection marked the server offline: status=%d online=%v", code, got.Server.Online)
	}

	var events struct {
		Events []model.Event `json:"events"`
	}
	if code := h.do(t, http.MethodGet, "/api/events?limit=20&action=agent.offline", nil, &events); code != 200 {
		t.Fatalf("offline events status %d", code)
	}
	if len(events.Events) != 0 {
		t.Fatalf("reconnect produced a false offline event: %+v", events.Events)
	}
}

func TestAuthRequired(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.srv.URL + "/api/servers")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/servers", nil)
	req.Header.Set("Authorization", "Bearer "+h.cfg.AdminToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 with token, got %d", resp.StatusCode)
	}
}

func TestEventsPaginationAPI(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		action := "agent.online"
		if i == 3 {
			action = "deploy.failed"
		}
		if err := h.store.AddEvent("ui", action, fmt.Sprintf("target-%d", i), fmt.Sprintf("detail %d", i)); err != nil {
			t.Fatal(err)
		}
	}

	type eventsResp struct {
		Events []model.Event `json:"events"`
		Page   struct {
			Limit      int   `json:"limit"`
			HasMore    bool  `json:"hasMore"`
			Total      int   `json:"total"`
			NextBefore int64 `json:"nextBefore"`
		} `json:"page"`
	}

	var first eventsResp
	if code := h.do(t, http.MethodGet, "/api/events?limit=2", nil, &first); code != 200 {
		t.Fatalf("first page status %d", code)
	}
	if len(first.Events) != 2 || !first.Page.HasMore || first.Page.Total != 5 || first.Page.Limit != 2 {
		t.Fatalf("first page: %+v", first)
	}
	if first.Events[0].ID <= first.Events[1].ID {
		t.Fatalf("events are not newest-first: %+v", first.Events)
	}

	var second eventsResp
	if code := h.do(t, http.MethodGet, fmt.Sprintf("/api/events?limit=2&before=%d", first.Page.NextBefore), nil, &second); code != 200 {
		t.Fatalf("second page status %d", code)
	}
	if len(second.Events) != 2 || !second.Page.HasMore {
		t.Fatalf("second page: %+v", second)
	}
	if second.Events[0].ID >= first.Events[1].ID {
		t.Fatalf("pages overlap: %d >= %d", second.Events[0].ID, first.Events[1].ID)
	}

	var filtered eventsResp
	if code := h.do(t, http.MethodGet, "/api/events?limit=50&action=deploy.failed", nil, &filtered); code != 200 {
		t.Fatalf("filter status %d", code)
	}
	if len(filtered.Events) != 1 || filtered.Page.Total != 1 || filtered.Page.HasMore {
		t.Fatalf("filtered: %+v", filtered)
	}

	var found eventsResp
	if code := h.do(t, http.MethodGet, "/api/events?limit=50&q=TARGET-2", nil, &found); code != 200 {
		t.Fatalf("search status %d", code)
	}
	if len(found.Events) != 1 || found.Events[0].Target != "target-2" {
		t.Fatalf("search: %+v", found.Events)
	}

	var facets struct {
		Actions []string `json:"actions"`
		Actors  []string `json:"actors"`
	}
	if code := h.do(t, http.MethodGet, "/api/events/facets", nil, &facets); code != 200 {
		t.Fatalf("facets status %d", code)
	}
	if len(facets.Actions) != 2 || len(facets.Actors) != 1 || facets.Actors[0] != "ui" {
		t.Fatalf("facets: %+v", facets)
	}
}

type projectResp struct {
	Project struct {
		model.Project
		Env        map[string]string `json:"env"`
		Placements []model.Placement `json:"placements"`
		Addons     []model.Addon     `json:"addons"`
		URL        string            `json:"url"`
	} `json:"project"`
}

func TestProjectLifecycleAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{ID: "phone-x", Name: "Test Phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}

	var created projectResp
	code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Demo App", "repo": "https://example.com/demo.git",
		"branch": "main", "startCmd": "node server.js", "healthPath": "/healthz",
		"env": map[string]string{"TOKEN": "s3cret"}, "serverId": "phone-x",
	}, &created)
	if code != 201 {
		t.Fatalf("create status %d", code)
	}
	if created.Project.Hostname != "demo-app.localhost" {
		t.Fatalf("unexpected hostname %q", created.Project.Hostname)
	}
	if len(created.Project.Placements) != 1 {
		t.Fatalf("expected one placement, got %+v", created.Project.Placements)
	}
	if created.Project.Placements[0].Port < 4100 || created.Project.Placements[0].ServerID != "phone-x" {
		t.Fatalf("unexpected placement %+v", created.Project.Placements[0])
	}
	if created.Project.Env["TOKEN"] != "s3cret" {
		t.Fatalf("env not decrypted: %+v", created.Project.Env)
	}

	var got projectResp
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID, nil, &got); code != 200 {
		t.Fatalf("get status %d", code)
	}
	if got.Project.HealthPath != "/healthz" || got.Project.URL == "" {
		t.Fatalf("unexpected project: %+v", got.Project)
	}

	var patched projectResp
	if code := h.do(t, http.MethodPatch, "/api/projects/"+created.Project.ID, map[string]any{
		"name": "Renamed", "env": map[string]string{"TOKEN": "rotated"},
	}, &patched); code != 200 {
		t.Fatalf("patch status %d", code)
	}
	if patched.Project.Name != "Renamed" || patched.Project.Env["TOKEN"] != "rotated" {
		t.Fatalf("patch not applied: %+v", patched.Project)
	}

	// The phone is not connected, so the deploy is queued for reconnect.
	if code := h.do(t, http.MethodPost, "/api/projects/"+created.Project.ID+"/deploy", nil, nil); code != 202 {
		t.Fatalf("expected 202 queued deploy while offline, got %d", code)
	}
	var queued struct {
		Deployments []model.Deployment `json:"deployments"`
	}
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID+"/deployments", nil, &queued); code != 200 {
		t.Fatalf("deployments status %d", code)
	}
	if len(queued.Deployments) == 0 || queued.Deployments[0].Status != "queued" ||
		queued.Deployments[0].Detail != "waiting for the phone to come online" {
		t.Fatalf("offline deploy was not queued: %+v", queued.Deployments)
	}

	var list struct {
		Projects []struct {
			model.Project
			Env map[string]string `json:"env"`
		} `json:"projects"`
	}
	if code := h.do(t, http.MethodGet, "/api/projects", nil, &list); code != 200 || len(list.Projects) != 1 {
		t.Fatalf("list: %d %+v", code, list)
	}
	if list.Projects[0].Env != nil {
		t.Fatalf("list should not expose env values")
	}

	if code := h.do(t, http.MethodDelete, "/api/projects/"+created.Project.ID, nil, nil); code != 200 {
		t.Fatalf("delete status %d", code)
	}
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID, nil, nil); code != 404 {
		t.Fatalf("expected 404 after delete, got %d", code)
	}
}

func TestStaticAndWorkerProjects(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{ID: "phone-x", Name: "Test Phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}

	var static projectResp
	code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Docs", "kind": "static", "repo": "https://example.com/docs.git",
		"branch": "main", "staticDir": "dist", "serverId": "phone-x",
	}, &static)
	if code != 201 {
		t.Fatalf("create static status %d", code)
	}
	if static.Project.Kind != "static" || static.Project.StaticDir != "dist" || static.Project.StartCmd != "" {
		t.Fatalf("unexpected static project: %+v", static.Project)
	}
	if static.Project.Hostname == "" || static.Project.URL == "" {
		t.Fatalf("static project needs a URL: %+v", static.Project)
	}
	if len(static.Project.Placements) != 1 || static.Project.Placements[0].Port < 4100 {
		t.Fatalf("static placement: %+v", static.Project.Placements)
	}

	var worker projectResp
	code = h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Jobs", "kind": "worker", "repo": "https://example.com/jobs.git",
		"branch": "main", "startCmd": "node worker.js", "serverId": "phone-x",
	}, &worker)
	if code != 201 {
		t.Fatalf("create worker status %d", code)
	}
	if worker.Project.Kind != "worker" || worker.Project.Hostname != "" || worker.Project.URL != "" {
		t.Fatalf("unexpected worker project: %+v", worker.Project)
	}
	if len(worker.Project.Placements) != 1 || worker.Project.Placements[0].Port != 0 {
		t.Fatalf("worker must not reserve a port: %+v", worker.Project.Placements)
	}

	// Static directories are confined to the release tree.
	if code := h.do(t, http.MethodPatch, "/api/projects/"+static.Project.ID, map[string]any{
		"staticDir": "../etc",
	}, nil); code != 400 {
		t.Fatalf("expected 400 for escaping static dir, got %d", code)
	}
	// Workers have no public hostname.
	if code := h.do(t, http.MethodPatch, "/api/projects/"+worker.Project.ID, map[string]any{
		"customHostname": "jobs.example.com",
	}, nil); code != 400 {
		t.Fatalf("expected 400 for a worker custom domain, got %d", code)
	}
}

func TestScheduledTaskAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{ID: "phone-x", Name: "Test Phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	var created projectResp
	if code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Demo", "repo": "https://example.com/demo.git",
		"branch": "main", "startCmd": "node server.js", "serverId": "phone-x",
	}, &created); code != 201 {
		t.Fatalf("create project status %d", code)
	}

	var made struct {
		Task struct {
			model.Task
			NextRun   int64 `json:"nextRun"`
			Available bool  `json:"available"`
		} `json:"task"`
	}
	code := h.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"name": "Nightly prune", "serverId": "phone-x", "projectId": created.Project.ID,
		"command": "npm run prune", "cron": "0 3 * * *", "timeoutSec": 600,
	}, &made)
	if code != 201 {
		t.Fatalf("create task status %d", code)
	}
	if made.Task.Name != "Nightly prune" || !made.Task.Enabled || made.Task.NextRun == 0 || !made.Task.Available {
		t.Fatalf("unexpected task: %+v", made.Task)
	}

	if code := h.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"name": "Bad", "serverId": "phone-x", "command": "true", "cron": "nope",
	}, nil); code != 400 {
		t.Fatalf("invalid cron status %d", code)
	}
	if code := h.do(t, http.MethodPost, "/api/tasks", map[string]any{
		"name": "Second task", "serverId": "phone-x", "projectId": created.Project.ID,
		"command": "true", "cron": "* * * * *",
	}, nil); code != 201 {
		t.Fatalf("second task status %d", code)
	}

	// The phone is not connected, so run-now and logs must fail cleanly.
	if code := h.do(t, http.MethodPost, "/api/tasks/"+made.Task.ID+"/run", nil, nil); code != 409 {
		t.Fatalf("offline run status %d", code)
	}
	if code := h.do(t, http.MethodGet, "/api/tasks/"+made.Task.ID+"/logs", nil, nil); code != 409 {
		t.Fatalf("offline logs status %d", code)
	}

	var patched struct {
		Task model.Task `json:"task"`
	}
	if code := h.do(t, http.MethodPatch, "/api/tasks/"+made.Task.ID, map[string]any{
		"enabled": false, "cron": "0 4 * * *",
	}, &patched); code != 200 || patched.Task.Enabled || patched.Task.Cron != "0 4 * * *" {
		t.Fatalf("patch task: %d %+v", code, patched.Task)
	}

	var list struct {
		Tasks []struct {
			model.Task
			NextRun int64 `json:"nextRun"`
		} `json:"tasks"`
	}
	if code := h.do(t, http.MethodGet, "/api/tasks", nil, &list); code != 200 || len(list.Tasks) != 2 {
		t.Fatalf("list tasks: %d %+v", code, list)
	}
	if list.Tasks[0].Enabled && list.Tasks[0].NextRun == 0 {
		t.Fatalf("enabled task has no next run: %+v", list.Tasks[0])
	}

	if code := h.do(t, http.MethodDelete, "/api/tasks/"+made.Task.ID, nil, nil); code != 200 {
		t.Fatalf("delete task status %d", code)
	}
	if code := h.do(t, http.MethodDelete, "/api/tasks/"+made.Task.ID, nil, nil); code != 404 {
		t.Fatalf("second delete status %d", code)
	}
}

func TestWebhookAndDeployRefAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{ID: "phone-x", Name: "Test Phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	var created projectResp
	if code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Hooked", "repo": "https://example.com/hooked.git",
		"branch": "main", "startCmd": "node server.js", "serverId": "phone-x",
	}, &created); code != 201 {
		t.Fatalf("create project status %d", code)
	}

	var hook struct {
		Configured bool   `json:"configured"`
		Secret     string `json:"secret"`
		GithubURL  string `json:"githubUrl"`
		GitlabURL  string `json:"gitlabUrl"`
	}
	if code := h.do(t, http.MethodPost, "/api/projects/"+created.Project.ID+"/webhook/rotate", nil, &hook); code != 200 {
		t.Fatalf("rotate webhook status %d", code)
	}
	if !hook.Configured || hook.Secret == "" || hook.GithubURL == "" || hook.GitlabURL == "" {
		t.Fatalf("unexpected webhook response: %+v", hook)
	}

	body := []byte(`{"ref":"refs/heads/main","after":"0123456789abcdef0123456789abcdef01234567"}`)
	bad := map[string]string{"X-Hub-Signature-256": "sha256=deadbeef", "X-GitHub-Event": "push"}
	if code := h.doWithHeaders(t, http.MethodPost, "/hooks/github/"+created.Project.ID, body, bad, nil); code != 401 {
		t.Fatalf("bad github signature status %d", code)
	}
	mac := hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(body)
	good := map[string]string{
		"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil)),
		"X-GitHub-Event":      "push",
	}
	// The phone is not connected, so the signature passes and the deployment
	// is queued until the phone reconnects.
	if code := h.doWithHeaders(t, http.MethodPost, "/hooks/github/"+created.Project.ID, body, good, nil); code != 202 {
		t.Fatalf("valid github webhook status %d", code)
	}
	// A different branch is ignored before any deployment is attempted.
	wrongBranch := []byte(`{"ref":"refs/heads/develop","after":"0123456789abcdef0123456789abcdef01234567"}`)
	mac = hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(wrongBranch)
	good["X-Hub-Signature-256"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if code := h.doWithHeaders(t, http.MethodPost, "/hooks/github/"+created.Project.ID, wrongBranch, good, nil); code != 202 {
		t.Fatalf("wrong branch status %d", code)
	}

	if code := h.doWithHeaders(t, http.MethodPost, "/hooks/gitlab/"+created.Project.ID, body, map[string]string{
		"X-Gitlab-Token": "wrong", "X-Gitlab-Event": "Push Hook",
	}, nil); code != 401 {
		t.Fatalf("bad gitlab token status %d", code)
	}
	if code := h.doWithHeaders(t, http.MethodPost, "/hooks/gitlab/"+created.Project.ID, wrongBranch, map[string]string{
		"X-Gitlab-Token": hook.Secret, "X-Gitlab-Event": "Push Hook",
	}, nil); code != 202 {
		t.Fatalf("gitlab wrong branch status %d", code)
	}

	if code := h.do(t, http.MethodPost, "/api/projects/"+created.Project.ID+"/deploy", map[string]any{
		"ref": "../evil",
	}, nil); code != 400 {
		t.Fatalf("invalid ref status %d", code)
	}
	if code := h.do(t, http.MethodPost, "/api/projects/"+created.Project.ID+"/deploy", map[string]any{
		"ref": "v1.2.3",
	}, nil); code != 202 {
		t.Fatalf("valid ref while offline status %d", code)
	}
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID+"/releases?serverId=phone-x", nil, nil); code != 409 {
		t.Fatalf("offline releases status %d", code)
	}
	if code := h.do(t, http.MethodPost, "/api/projects/"+created.Project.ID+"/rollback", map[string]any{
		"serverId": "phone-x", "release": "20260101-000000",
	}, nil); code != 409 {
		t.Fatalf("offline release rollback status %d", code)
	}
}

func TestObservabilityAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{ID: "phone-x", Name: "Test Phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}
	var created projectResp
	if code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Observed", "repo": "https://example.com/obs.git",
		"branch": "main", "startCmd": "node server.js", "serverId": "phone-x",
	}, &created); code != 201 {
		t.Fatalf("create project status %d", code)
	}
	now := time.Now().Unix()
	if err := h.store.InsertAppMetric(created.Project.ID, "phone-x", now, 12.5, 4<<20); err != nil {
		t.Fatal(err)
	}
	var metrics struct {
		Samples []model.AppSample `json:"samples"`
	}
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID+"/metrics?minutes=60", nil, &metrics); code != 200 {
		t.Fatalf("metrics status %d", code)
	}
	if len(metrics.Samples) != 1 || metrics.Samples[0].CPUPercent != 12.5 || metrics.Samples[0].RSS != 4<<20 {
		t.Fatalf("metrics: %+v", metrics.Samples)
	}

	if err := h.store.InsertRequestLog(model.RequestLog{
		ProjectID: created.Project.ID, ServerID: "phone-x", TS: now,
		Method: "GET", Path: "/api/items?limit=2", Status: 200, Bytes: 42,
		DurationMS: 7, Host: "observed.localhost", IP: "203.0.113.9",
	}); err != nil {
		t.Fatal(err)
	}
	var requests struct {
		Requests []model.RequestLog `json:"requests"`
	}
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID+"/requests?limit=10", nil, &requests); code != 200 {
		t.Fatalf("requests status %d", code)
	}
	if len(requests.Requests) != 1 || requests.Requests[0].Path != "/api/items?limit=2" || requests.Requests[0].Status != 200 {
		t.Fatalf("requests: %+v", requests.Requests)
	}
}

func TestTunnelProxyRouting(t *testing.T) {
	h := newHarness(t)

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-App", "yes")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, "hello from app "+r.URL.RequestURI())
	}))
	defer app.Close()

	hostPort := strings.TrimPrefix(app.URL, "http://")
	_, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	// Simulate the agent data connection: yamux client over a WebSocket.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, h.wsURL()+"/data", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":   []string{"Bearer " + h.cfg.AgentToken},
			"X-Phleet-Server": []string{"phone-tunnel"},
		},
	})
	if err != nil {
		t.Fatalf("dial data tunnel: %v", err)
	}
	sess, err := yamux.Client(tunnel.NewWSConn(ws), tunnel.Config())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer sess.Close()
	go tunnel.ServeStreams(sess, func(p int) bool { return p == port })

	if err := h.store.CreateProject(model.Project{
		ID: "tunnel-project", Name: "Tunnel", Slug: "tunnel", Hostname: "tunnel.example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpsertPlacement(model.Placement{
		ProjectID: "tunnel-project", ServerID: "phone-tunnel", Port: port, Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "tunnel registered", func() bool {
		_, ok := h.api.Hub().Tunnel("phone-tunnel")
		return ok
	})

	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/hello?x=1", nil)
	req.Host = "tunnel.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatalf("expected app status 201, got %d (%s)", resp.StatusCode, body)
	}
	if resp.Header.Get("X-App") != "yes" {
		t.Fatalf("missing app header: %v", resp.Header)
	}
	if !strings.Contains(string(body), "/hello?x=1") {
		t.Fatalf("unexpected body %q", body)
	}

	// Unknown hosts still reach the panel mux (404 for the SPA route is fine),
	// but they must not be proxied to the app.
	req2, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/hello", nil)
	req2.Host = "other.example.com"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == 201 {
		t.Fatal("unknown host should not be proxied to the app")
	}
}

func TestReplicaSchedulingAndFailover(t *testing.T) {
	h := newHarness(t)
	ids := []string{"phone-1", "phone-2", "phone-3"}
	conns := map[string]*websocket.Conn{}
	for _, id := range ids {
		conns[id] = dialAgent(t, h, id)
	}
	waitFor(t, "three phones online", func() bool {
		var list struct {
			Servers []struct {
				ID     string `json:"id"`
				Online bool   `json:"online"`
			} `json:"servers"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		n := 0
		for _, s := range list.Servers {
			if s.Online {
				n++
			}
		}
		return n == 3
	})

	var created projectResp
	code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Replicated", "repo": "https://example.com/app.git",
		"startCmd": "node server.js", "replicas": 2,
	}, &created)
	if code != 201 {
		t.Fatalf("create status %d", code)
	}
	if len(created.Project.Placements) != 2 {
		t.Fatalf("expected 2 placements, got %+v", created.Project.Placements)
	}
	if created.Project.Placements[0].ServerID == created.Project.Placements[1].ServerID {
		t.Fatal("replicas landed on the same phone")
	}
	if created.Project.Placements[0].Port == 0 || created.Project.Placements[1].Port == 0 {
		t.Fatal("expected allocated ports")
	}

	// Lose one replica: reconciliation should add a new placement on the third phone.
	conns["phone-1"].Close(websocket.StatusNormalClosure, "test failure")
	waitFor(t, "phone-1 offline", func() bool {
		var resp struct {
			Server struct {
				Online bool `json:"online"`
			} `json:"server"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers/phone-1", nil, &resp)
		return !resp.Server.Online
	})
	if code := h.do(t, http.MethodPost, "/api/rebalance", nil, nil); code != 200 {
		t.Fatalf("rebalance status %d", code)
	}
	waitFor(t, "replacement placement", func() bool {
		var got projectResp
		_ = h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID, nil, &got)
		for _, pl := range got.Project.Placements {
			if pl.ServerID == "phone-3" {
				return true
			}
		}
		return false
	})
}

func TestAddonLifecycleAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{
		ID: "phone-db", Name: "DB Phone", Status: "online",
		Caps: map[string]any{
			"home":    "/data/data/com.termux/files/home",
			"engines": map[string]any{"postgres": true, "sqlite": true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var project projectResp
	if code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "App With DB", "repo": "https://example.com/app.git",
		"startCmd": "node server.js", "serverId": "phone-db",
	}, &project); code != 201 {
		t.Fatalf("create project status %d", code)
	}

	type addonResp struct {
		Addon struct {
			model.Addon
			Env        map[string]string      `json:"env"`
			URL        string                 `json:"url"`
			Bindings   []model.AddonBinding   `json:"bindings"`
			Placements []model.AddonPlacement `json:"placements"`
		} `json:"addon"`
	}
	var created addonResp
	code := h.do(t, http.MethodPost, "/api/addons", map[string]any{
		"name": "App DB", "engine": "postgres", "serverId": "phone-db",
		"projectId": project.Project.ID,
	}, &created)
	if code != 201 {
		t.Fatalf("create addon status %d", code)
	}
	if created.Addon.Port < 4100 || created.Addon.Port > 4999 {
		t.Fatalf("unexpected port %d", created.Addon.Port)
	}
	if created.Addon.Slug != "db-app-db" {
		t.Fatalf("unexpected slug %q", created.Addon.Slug)
	}
	if !strings.Contains(created.Addon.URL, "postgres://") || !strings.Contains(created.Addon.URL, "127.0.0.1") {
		t.Fatalf("bad connection url %q", created.Addon.URL)
	}
	if len(created.Addon.Bindings) != 1 || len(created.Addon.Placements) != 1 {
		t.Fatalf("binding/placement missing: %+v", created.Addon)
	}
	if created.Addon.DataDir != "/data/data/com.termux/files/home/phleet/data/db-app-db" {
		t.Fatalf("unexpected data dir %q", created.Addon.DataDir)
	}

	var got projectResp
	if code := h.do(t, http.MethodGet, "/api/projects/"+project.Project.ID, nil, &got); code != 200 {
		t.Fatalf("project status %d", code)
	}
	if len(got.Project.Addons) != 1 {
		t.Fatalf("project view lost its addon: %+v", got.Project)
	}

	var list struct {
		Addons []struct {
			model.Addon
			Env map[string]string `json:"env"`
		} `json:"addons"`
	}
	if code := h.do(t, http.MethodGet, "/api/addons", nil, &list); code != 200 || len(list.Addons) != 1 {
		t.Fatalf("list addons: %d %+v", code, list)
	}
	if list.Addons[0].Env != nil {
		t.Fatal("addon list must not expose credentials")
	}

	// A bound addon is protected from accidental deletion.
	if code := h.do(t, http.MethodDelete, "/api/addons/"+created.Addon.ID, nil, nil); code != 409 {
		t.Fatalf("expected 409 deleting a bound addon, got %d", code)
	}
	if code := h.do(t, http.MethodDelete, "/api/addons/"+created.Addon.ID+"/bind/"+project.Project.ID, nil, nil); code != 200 {
		t.Fatalf("unbind status %d", code)
	}
	if code := h.do(t, http.MethodDelete, "/api/addons/"+created.Addon.ID, nil, nil); code != 200 {
		t.Fatalf("delete status %d", code)
	}
	if code := h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, nil); code != 404 {
		t.Fatalf("expected 404 after delete, got %d", code)
	}
}

func TestServiceLifecycleAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{
		ID: "phone-svc", Name: "Service Phone", Status: "online",
		Caps: map[string]any{
			"home":       "/data/data/com.termux/files/home",
			"userland":   true,
			"pkgInstall": true,
			"engines":    map[string]any{"memos": true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	type serviceResp struct {
		Service struct {
			model.Addon
			Placements []model.AddonPlacement `json:"placements"`
		} `json:"service"`
	}
	var created serviceResp
	code := h.do(t, http.MethodPost, "/api/services", map[string]any{
		"name": "My Notes", "engine": "memos", "serverId": "phone-svc",
	}, &created)
	if code != 201 {
		t.Fatalf("create service status %d", code)
	}
	if !created.Service.Service || created.Service.AppID != "memos" || created.Service.Version != "26.11.0" {
		t.Fatalf("service metadata missing: %+v", created.Service)
	}
	if created.Service.Slug != "my-notes" || !strings.HasSuffix(created.Service.Hostname, ".localhost") {
		t.Fatalf("unexpected service identity: %+v", created.Service)
	}
	if !strings.Contains(created.Service.DataDir, "/phleet/services/") {
		t.Fatalf("unexpected service data dir %q", created.Service.DataDir)
	}
	if len(created.Service.Placements) != 1 {
		t.Fatalf("service placement missing: %+v", created.Service)
	}

	var services struct {
		Services []model.Addon `json:"services"`
	}
	if code := h.do(t, http.MethodGet, "/api/services", nil, &services); code != 200 || len(services.Services) != 1 {
		t.Fatalf("list services: %d %+v", code, services)
	}
	var addons struct {
		Addons []model.Addon `json:"addons"`
	}
	if code := h.do(t, http.MethodGet, "/api/addons", nil, &addons); code != 200 || len(addons.Addons) != 0 {
		t.Fatalf("addons must not include services: %d %+v", code, addons)
	}
	var catalogue struct {
		Engines []struct {
			ID      string `json:"id"`
			Service bool   `json:"service"`
		} `json:"engines"`
	}
	if code := h.do(t, http.MethodGet, "/api/services/engines", nil, &catalogue); code != 200 {
		t.Fatalf("service catalogue status %d", code)
	}
	for _, e := range catalogue.Engines {
		if !e.Service {
			t.Fatalf("service catalogue leaked addon %q", e.ID)
		}
	}
	if code := h.do(t, http.MethodDelete, "/api/services/"+created.Service.ID, nil, nil); code != 200 {
		t.Fatalf("delete service status %d", code)
	}
	if code := h.do(t, http.MethodGet, "/api/services/"+created.Service.ID, nil, nil); code != 404 {
		t.Fatalf("expected 404 after service delete, got %d", code)
	}
}

func TestAddonRejectsUnsafeLayouts(t *testing.T) {
	h := newHarness(t)
	_ = h.store.UpsertServer(model.Server{ID: "phone-db", Name: "DB Phone", Status: "online"})

	// No phone at all.
	if code := h.do(t, http.MethodPost, "/api/addons", map[string]any{
		"name": "Orphan", "engine": "postgres",
	}, nil); code != 400 {
		t.Fatalf("expected 400 without a phone, got %d", code)
	}
	// Unknown engine.
	if code := h.do(t, http.MethodPost, "/api/addons", map[string]any{
		"name": "Nope", "engine": "oracle", "serverId": "phone-db",
	}, nil); code != 400 {
		t.Fatalf("expected 400 for unknown engine, got %d", code)
	}
}

func (h *harness) doBody(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.AdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (h *harness) doRaw(t *testing.T, method, path string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.AdminToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func serveAgentReplies(ws *websocket.Conn) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, data, err := ws.Read(ctx)
		cancel()
		if err != nil {
			return
		}
		var env agentproto.Envelope
		if json.Unmarshal(data, &env) != nil || env.ID == "" {
			continue
		}
		out, _ := json.Marshal(agentproto.Envelope{Type: "reply", ReplyTo: env.ID})
		wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = ws.Write(wctx, websocket.MessageText, out)
		wcancel()
	}
}

func TestAddonStandbyAndPromotionAPI(t *testing.T) {
	h := newHarness(t)
	wsA := dialAgent(t, h, "phone-a")
	wsB := dialAgent(t, h, "phone-b")
	waitFor(t, "two phones online", func() bool {
		var list struct {
			Servers []struct {
				ID     string `json:"id"`
				Online bool   `json:"online"`
			} `json:"servers"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		n := 0
		for _, s := range list.Servers {
			if s.Online {
				n++
			}
		}
		return n == 2
	})

	type addonResp struct {
		Addon struct {
			model.Addon
			Placements []model.AddonPlacement `json:"placements"`
			Relays     []model.AddonRelay     `json:"relays"`
		} `json:"addon"`
	}
	var created addonResp
	if code := h.do(t, http.MethodPost, "/api/addons", map[string]any{
		"name": "Replicated DB", "engine": "postgres", "serverId": "phone-a",
	}, &created); code != 201 {
		t.Fatalf("create addon status %d", code)
	}
	if code := h.do(t, http.MethodPost, "/api/addons/"+created.Addon.ID+"/standby", map[string]any{
		"serverId": "phone-b",
	}, nil); code != 202 {
		t.Fatalf("standby status %d", code)
	}

	var withStandby addonResp
	if code := h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &withStandby); code != 200 {
		t.Fatalf("get addon status %d", code)
	}
	if len(withStandby.Addon.Placements) != 2 {
		t.Fatalf("expected two placements, got %+v", withStandby.Addon.Placements)
	}
	standbyPort := 0
	for _, pl := range withStandby.Addon.Placements {
		if pl.Role == "standby" {
			standbyPort = pl.Port
			if pl.Port < 4100 || pl.ServerID != "phone-b" {
				t.Fatalf("bad standby placement %+v", pl)
			}
		}
	}
	if standbyPort == 0 {
		t.Fatal("no standby placement")
	}
	// The standby phone needs a loopback facade to the primary.
	if len(withStandby.Addon.Relays) != 1 || withStandby.Addon.Relays[0].ServerID != "phone-b" {
		t.Fatalf("expected a relay on the standby phone: %+v", withStandby.Addon.Relays)
	}

	// Wait until the CP has sent the provisioning plan; otherwise the
	// heartbeat below races with the later "provisioning" event.
	waitFor(t, "standby provisioning started", func() bool {
		var got addonResp
		_ = h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &got)
		for _, pl := range got.Addon.Placements {
			if pl.ServerID == "phone-b" && pl.Status == "provisioning" {
				return true
			}
		}
		return false
	})
	// The standby reports itself running through the normal heartbeat path.
	writeEnv(t, wsB, agentproto.TypeHeartbeat, "", agentproto.Heartbeat{
		Apps: []agentproto.AppStatus{{
			Slug: created.Addon.Slug, Kind: "database", Role: "standby",
			State: "running", Port: standbyPort,
		}},
	})
	waitFor(t, "standby running", func() bool {
		var got addonResp
		_ = h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &got)
		for _, pl := range got.Addon.Placements {
			if pl.ServerID == "phone-b" && pl.Status == "running" {
				return true
			}
		}
		return false
	})

	go serveAgentReplies(wsA)
	go serveAgentReplies(wsB)
	var promoted addonResp
	if code, out := h.doBody(t, http.MethodPost, "/api/addons/"+created.Addon.ID+"/promote", map[string]any{
		"serverId": "phone-b",
	}); code != 200 {
		t.Fatalf("promote status %d: %s", code, out)
	} else if err := json.Unmarshal(out, &promoted); err != nil {
		t.Fatal(err)
	}
	if promoted.Addon.PinnedServer != "phone-b" || promoted.Addon.Port != standbyPort {
		t.Fatalf("addon was not repointed: %+v", promoted.Addon.Addon)
	}
	if promoted.Addon.Epoch != 2 {
		t.Fatalf("epoch did not advance: %d", promoted.Addon.Epoch)
	}
	roles := map[string]string{}
	for _, pl := range promoted.Addon.Placements {
		roles[pl.ServerID] = pl.Role
	}
	if roles["phone-a"] != "demoted" || roles["phone-b"] != "primary" {
		t.Fatalf("unexpected roles after promotion: %+v", roles)
	}

	// The demoted old primary can be removed, which is how it is re-admitted
	// as a fresh standby later.
	if code := h.do(t, http.MethodDelete, "/api/addons/"+created.Addon.ID+"/placement/phone-a", nil, nil); code != 200 {
		t.Fatalf("remove demoted placement status %d", code)
	}
	var after addonResp
	if code := h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &after); code != 200 {
		t.Fatalf("get after remove status %d", code)
	}
	if len(after.Addon.Placements) != 1 || after.Addon.Placements[0].ServerID != "phone-b" {
		t.Fatalf("demoted placement survived: %+v", after.Addon.Placements)
	}
}

func TestAddonAutoFailoverAPI(t *testing.T) {
	h := newHarness(t)
	wsA := dialAgent(t, h, "auto-a")
	wsB := dialAgent(t, h, "auto-b")
	waitFor(t, "two phones online", func() bool {
		var list struct {
			Servers []struct {
				ID     string `json:"id"`
				Online bool   `json:"online"`
			} `json:"servers"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		n := 0
		for _, s := range list.Servers {
			if s.Online {
				n++
			}
		}
		return n == 2
	})

	type addonResp struct {
		Addon struct {
			model.Addon
			Placements []model.AddonPlacement `json:"placements"`
		} `json:"addon"`
	}
	var created addonResp
	if code := h.do(t, http.MethodPost, "/api/addons", map[string]any{
		"name": "Auto DB", "engine": "postgres", "serverId": "auto-a",
	}, &created); code != 201 {
		t.Fatalf("create addon status %d", code)
	}
	if code := h.do(t, http.MethodPost, "/api/addons/"+created.Addon.ID+"/standby", map[string]any{
		"serverId": "auto-b",
	}, nil); code != 202 {
		t.Fatalf("standby status %d", code)
	}
	var view addonResp
	if code := h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &view); code != 200 {
		t.Fatalf("get addon status %d", code)
	}
	standbyPort := 0
	for _, pl := range view.Addon.Placements {
		if pl.Role == "standby" {
			standbyPort = pl.Port
		}
	}
	if standbyPort == 0 {
		t.Fatal("no standby placement")
	}
	primaryPort := 0
	for _, pl := range view.Addon.Placements {
		if pl.Role == "primary" || pl.Role == "" {
			primaryPort = pl.Port
		}
	}
	// The primary was healthy before the outage; report that as well so the
	// failover guard sees a real primary rather than a pending placement.
	writeEnv(t, wsA, agentproto.TypeHeartbeat, "", agentproto.Heartbeat{
		Apps: []agentproto.AppStatus{{
			Slug: created.Addon.Slug, Kind: "database", Role: "primary",
			State: "running", Port: primaryPort,
		}},
	})
	waitFor(t, "primary running", func() bool {
		var got addonResp
		_ = h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &got)
		for _, pl := range got.Addon.Placements {
			if pl.ServerID == "auto-a" && pl.Status == "running" {
				return true
			}
		}
		return false
	})
	// Wait until the CP has sent the provisioning plan, then report running
	// before the fake agents start replying (one writer per socket).
	waitFor(t, "standby provisioning started", func() bool {
		var got addonResp
		_ = h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &got)
		for _, pl := range got.Addon.Placements {
			if pl.ServerID == "auto-b" && pl.Status == "provisioning" {
				return true
			}
		}
		return false
	})
	// The standby reports running before the fake agents start replying, so
	// the test does not have two writers on one socket.
	writeEnv(t, wsB, agentproto.TypeHeartbeat, "", agentproto.Heartbeat{
		Apps: []agentproto.AppStatus{{
			Slug: created.Addon.Slug, Kind: "database", Role: "standby",
			State: "running", Port: standbyPort,
		}},
	})
	waitFor(t, "standby running", func() bool {
		var got addonResp
		_ = h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &got)
		for _, pl := range got.Addon.Placements {
			if pl.ServerID == "auto-b" && pl.Status == "running" {
				return true
			}
		}
		return false
	})

	go serveAgentReplies(wsA)
	go serveAgentReplies(wsB)
	if code := h.do(t, http.MethodPatch, "/api/addons/"+created.Addon.ID, map[string]any{
		"autoFailover": true, "failoverAfterSec": 0, "maxLagMs": 0, "leaseSec": 30,
	}, nil); code != 200 {
		t.Fatalf("enable auto failover status %d", code)
	}
	// Expire the lease, then let the reconciler run.
	if err := h.store.SetAddonLease(created.Addon.ID, 1); err != nil {
		t.Fatal(err)
	}
	if code := h.do(t, http.MethodPost, "/api/rebalance", nil, nil); code != 200 {
		t.Fatalf("rebalance status %d", code)
	}
	waitFor(t, "automatic promotion", func() bool {
		var got addonResp
		_ = h.do(t, http.MethodGet, "/api/addons/"+created.Addon.ID, nil, &got)
		return got.Addon.PinnedServer == "auto-b" && got.Addon.Epoch >= 2
	})
}

// A rooted phone running the agent without Termux reports userland=false. It
// must still register, but every path that would build or install something is
// rejected with a clear message instead of failing mid-run.
func TestMonitoringOnlyPhoneIsGated(t *testing.T) {
	h := newHarness(t)
	_ = dialAgentCaps(t, h, "monitor-a", map[string]any{
		"root": true, "termux": false, "userland": false, "pkg": false,
		"engines": map[string]any{"postgres": false, "redis": false, "sqlite": false, "rclone": false},
	})
	waitFor(t, "phone online", func() bool {
		var list struct {
			Servers []struct {
				ID     string `json:"id"`
				Online bool   `json:"online"`
			} `json:"servers"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		for _, s := range list.Servers {
			if s.ID == "monitor-a" && s.Online {
				return true
			}
		}
		return false
	})

	code, body := h.doBody(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Monitored", "repo": "https://example.com/app.git",
		"startCmd": "node server.js", "serverId": "monitor-a",
	})
	if code != 409 || !strings.Contains(string(body), "monitoring-only") {
		t.Fatalf("project create = %d %s, want 409 monitoring-only", code, body)
	}

	code, body = h.doBody(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Mon Stack", "stack": true, "repo": "https://example.com/app.git",
		"serverId": "monitor-a",
		"services": []map[string]any{{"name": "web", "startCmd": "node web.js"}},
	})
	if code != 409 || !strings.Contains(string(body), "monitoring-only") {
		t.Fatalf("stack create = %d %s, want 409 monitoring-only", code, body)
	}

	code, body = h.doBody(t, http.MethodPost, "/api/addons", map[string]any{
		"name": "Mon DB", "engine": "postgres", "serverId": "monitor-a",
	})
	if code != 409 || !strings.Contains(string(body), "monitoring-only") {
		t.Fatalf("addon create = %d %s, want 409 monitoring-only", code, body)
	}
}

func TestChunkedBackupUpload(t *testing.T) {
	h := newHarness(t)
	if err := h.store.CreateAddon(model.Addon{
		ID: "a-chunk", Name: "Chunk DB", Slug: "db-chunk", Engine: "postgres", Kind: "database",
		PinnedServer: "phone-x", Port: 4100, Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.CreateBackup(model.Backup{
		ID: "b-chunk", AddonID: "a-chunk", ServerID: "phone-x", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if code, body := h.doRaw(t, http.MethodPut, "/api/addons/a-chunk/backups/b-chunk?offset=0&last=false", []byte("hello ")); code != 200 {
		t.Fatalf("first chunk status %d: %s", code, body)
	}
	if code, body := h.doRaw(t, http.MethodPut, "/api/addons/a-chunk/backups/b-chunk?offset=6&last=true", []byte("world")); code != 200 {
		t.Fatalf("second chunk status %d: %s", code, body)
	}
	b, err := h.store.GetBackup("b-chunk")
	if err != nil || b.Status != "complete" || b.Size != 11 || b.Path == "" {
		t.Fatalf("backup not finalised: %+v %v", b, err)
	}
	sum := sha256.Sum256([]byte("hello world"))
	if b.Checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("backup checksum = %q", b.Checksum)
	}
	if !b.Encrypted {
		t.Fatal("new backups must be encrypted at rest")
	}
	raw, err := os.ReadFile(b.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hello world")) {
		t.Fatal("plaintext leaked into stored backup")
	}
	code, data := h.doRaw(t, http.MethodGet, "/api/addons/a-chunk/backups/b-chunk/download", nil)
	if code != 200 || string(data) != "hello world" {
		t.Fatalf("download status %d body %q", code, data)
	}
	// A gap is rejected instead of silently corrupting the dump.
	if code, _ := h.doRaw(t, http.MethodPut, "/api/addons/a-chunk/backups/b-chunk?offset=99&last=true", []byte("x")); code != 409 {
		t.Fatalf("expected 409 for a bad offset, got %d", code)
	}
}

func TestStreamingFileDownload(t *testing.T) {
	h := newHarness(t)
	sess := dialDataTunnel(t, h, "phone-fs")
	go tunnel.ServeStreamsWithFiles(sess, nil, func(stream *yamux.Stream, req tunnel.RequestHeader) error {
		if req.Kind != "fs" || req.Path != "hello.txt" {
			return tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: 404, Error: "not found"})
		}
		if err := tunnel.WriteFrame(stream, tunnel.ResponseHeader{Status: 200, ContentLength: 5}); err != nil {
			return err
		}
		_, err := stream.Write([]byte("hello"))
		return err
	})
	code, body := h.doRaw(t, http.MethodGet, "/api/servers/phone-fs/download?path=hello.txt", nil)
	if code != http.StatusOK || string(body) != "hello" {
		t.Fatalf("streamed download = %d %q", code, body)
	}
}

func dialDataTunnel(t *testing.T, h *harness, serverID string) *yamux.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, h.wsURL()+"/data", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":   []string{"Bearer " + h.cfg.AgentToken},
			"X-Phleet-Server": []string{serverID},
		},
	})
	if err != nil {
		t.Fatalf("dial data tunnel: %v", err)
	}
	sess, err := yamux.Client(tunnel.NewWSConn(ws), tunnel.Config())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	waitFor(t, "tunnel "+serverID, func() bool {
		_, ok := h.api.Hub().Tunnel(serverID)
		return ok
	})
	return sess
}

func TestRelayStreamCarriesTCP(t *testing.T) {
	h := newHarness(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				c.Close()
			}()
		}
	}()

	// Two phones: A runs an app, B hosts the database.
	sessA := dialDataTunnel(t, h, "relay-a")
	sessB := dialDataTunnel(t, h, "relay-b")
	go tunnel.ServeStreams(sessB, func(p int) bool { return p == port })

	if err := h.store.CreateAddon(model.Addon{
		ID: "relay-addon", Name: "Relayed", Slug: "db-relayed", Engine: "postgres",
		Kind: "database", PinnedServer: "relay-b", Port: port, Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpsertAddonPlacement(model.AddonPlacement{
		AddonID: "relay-addon", ServerID: "relay-b", Role: "primary", Status: "running",
		Desired: "running", Port: port,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpsertAddonRelay(model.AddonRelay{
		AddonID: "relay-addon", ServerID: "relay-a", Port: 16600,
	}); err != nil {
		t.Fatal(err)
	}

	stream, err := tunnel.OpenRelay(sessA, "relay-addon")
	if err != nil {
		t.Fatalf("open relay: %v", err)
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("unexpected relay echo %q", buf)
	}

	// A phone without an addon_relays row must be refused.
	sessC := dialDataTunnel(t, h, "relay-c")
	if _, err := tunnel.OpenRelay(sessC, "relay-addon"); err == nil {
		t.Fatal("unauthorized phone opened a relay")
	}
}

type stackProjectResp struct {
	Project struct {
		model.Project
		Services []struct {
			model.Project
			Placements []model.Placement `json:"placements"`
			URL        string            `json:"url"`
		} `json:"services"`
		URL string `json:"url"`
	} `json:"project"`
}

func TestStackLifecycleAPI(t *testing.T) {
	h := newHarness(t)
	if err := h.store.UpsertServer(model.Server{ID: "phone-x", Name: "Test Phone", Status: "online"}); err != nil {
		t.Fatal(err)
	}

	var created stackProjectResp
	code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Demo Stack", "slug": "demo", "repo": "https://example.com/demo.git",
		"branch": "main", "stack": true, "pinnedServer": "phone-x",
		"services": []map[string]any{
			{"name": "Web", "slug": "demo", "startCmd": "node web.js", "healthPath": "/"},
			{"name": "API", "slug": "demo-api", "startCmd": "node api.js", "healthPath": "/health"},
		},
	}, &created)
	if code != 201 {
		t.Fatalf("create stack status %d", code)
	}
	if created.Project.Kind != "stack" {
		t.Fatalf("expected stack kind, got %q", created.Project.Kind)
	}
	if created.Project.Slug != "demo-stack" {
		t.Fatalf("unexpected parent slug %q", created.Project.Slug)
	}
	if len(created.Project.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(created.Project.Services))
	}
	if created.Project.Services[0].Hostname != "demo.localhost" || created.Project.Services[1].Hostname != "demo-api.localhost" {
		t.Fatalf("unexpected service hostnames: %q %q",
			created.Project.Services[0].Hostname, created.Project.Services[1].Hostname)
	}
	if created.Project.Services[0].Kind != "app" || created.Project.Services[0].ParentID != created.Project.ID {
		t.Fatalf("service is not a child app: %+v", created.Project.Services[0].Project)
	}
	if len(created.Project.Services[0].Placements) != 1 || len(created.Project.Services[1].Placements) != 1 {
		t.Fatalf("expected one placement per service")
	}
	if created.Project.URL != "http://demo.localhost" {
		t.Fatalf("unexpected stack url %q", created.Project.URL)
	}

	var list struct {
		Projects []struct {
			model.Project
			Services []model.Project `json:"services"`
		} `json:"projects"`
	}
	if code := h.do(t, http.MethodGet, "/api/projects", nil, &list); code != 200 {
		t.Fatalf("list status %d", code)
	}
	if len(list.Projects) != 1 || list.Projects[0].ID != created.Project.ID {
		t.Fatalf("stack should be the only top-level project: %+v", list.Projects)
	}

	var got stackProjectResp
	if code := h.do(t, http.MethodGet, "/api/projects/"+created.Project.ID, nil, &got); code != 200 {
		t.Fatalf("get stack status %d", code)
	}
	if len(got.Project.Services) != 2 {
		t.Fatalf("stack detail lost services")
	}

	childID := created.Project.Services[1].ID
	if code := h.do(t, http.MethodDelete, "/api/projects/"+created.Project.ID, nil, nil); code != 200 {
		t.Fatalf("delete stack status %d", code)
	}
	if code := h.do(t, http.MethodGet, "/api/projects/"+childID, nil, nil); code != 404 {
		t.Fatalf("child service survived stack deletion: status %d", code)
	}
}
