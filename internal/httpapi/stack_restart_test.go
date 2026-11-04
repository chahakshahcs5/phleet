package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"phleet/internal/agentproto"
	"phleet/internal/model"
)

type stackAgentAction struct {
	kind   string
	target string
	action string
}

func recordStackAgentActions(ws *websocket.Conn, addonIDs map[string]bool, slug string, out chan<- stackAgentAction) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, data, err := ws.Read(ctx)
		cancel()
		if err != nil {
			return
		}
		var env agentproto.Envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Type {
		case agentproto.TypeAddonControl:
			var req agentproto.AddonControlRequest
			if json.Unmarshal(env.Data, &req) == nil && addonIDs[req.AddonID] {
				out <- stackAgentAction{kind: "addon", target: req.AddonID, action: req.Action}
			}
		case agentproto.TypeAppControl:
			var req agentproto.AppControlRequest
			if json.Unmarshal(env.Data, &req) == nil && req.Slug == slug {
				out <- stackAgentAction{kind: "app", target: req.Slug, action: req.Action}
			}
		}
		if env.ID != "" {
			reply, _ := json.Marshal(agentproto.Envelope{Type: "reply", ReplyTo: env.ID})
			wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := ws.Write(wctx, websocket.MessageText, reply)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}

func TestStackRestartRestartsAddonsBeforeServices(t *testing.T) {
	h := newHarness(t)
	ws := dialAgent(t, h, "phone-x")
	waitFor(t, "phone online", func() bool {
		var list struct {
			Servers []struct {
				ID     string `json:"id"`
				Online bool   `json:"online"`
			} `json:"servers"`
		}
		_ = h.do(t, http.MethodGet, "/api/servers", nil, &list)
		for _, srv := range list.Servers {
			if srv.ID == "phone-x" && srv.Online {
				return true
			}
		}
		return false
	})

	var created stackProjectResp
	code := h.do(t, http.MethodPost, "/api/projects", map[string]any{
		"name": "Restart Stack", "slug": "restart-stack", "repo": "https://example.com/demo.git",
		"branch": "main", "stack": true, "pinnedServer": "phone-x",
		"services": []map[string]any{
			{"name": "Web", "slug": "restart-web", "startCmd": "node web.js", "healthPath": "/"},
		},
	}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create stack = %d", code)
	}
	if len(created.Project.Services) != 1 {
		t.Fatalf("stack services = %d, want 1", len(created.Project.Services))
	}
	child := created.Project.Services[0]
	if err := h.store.UpsertPlacement(model.Placement{
		ProjectID: child.ID, ServerID: "phone-x", Port: 4100, Status: "running", Desired: "running",
	}); err != nil {
		t.Fatal(err)
	}

	addonIDs := map[string]bool{"addon-running": true, "addon-stopped": true}
	for _, spec := range []struct {
		id, name, slug, status string
	}{
		{"addon-running", "Running DB", "db-running", "running"},
		{"addon-stopped", "Stopped DB", "db-stopped", "stopped"},
	} {
		if err := h.store.CreateAddon(model.Addon{
			ID: spec.id, Name: spec.name, Slug: spec.slug, Engine: "postgres",
			Kind: "database", OwnerProjectID: created.Project.ID, PinnedServer: "phone-x",
			Status: spec.status,
		}); err != nil {
			t.Fatal(err)
		}
		if err := h.store.UpsertAddonPlacement(model.AddonPlacement{
			AddonID: spec.id, ServerID: "phone-x", Role: "primary", Status: "running", Desired: "running",
		}); err != nil {
			t.Fatal(err)
		}
	}

	actions := make(chan stackAgentAction, 16)
	go recordStackAgentActions(ws, addonIDs, child.Slug, actions)

	var result struct {
		OK      bool             `json:"ok"`
		Results []map[string]any `json:"results"`
	}
	if code := h.do(t, http.MethodPost, "/api/projects/"+created.Project.ID+"/restart", nil, &result); code != http.StatusOK {
		t.Fatalf("restart stack = %d", code)
	}
	if !result.OK {
		t.Fatalf("restart result = %+v", result)
	}

	got := map[string]string{}
	order := []string{}
	for i := 0; i < 3; i++ {
		select {
		case a := <-actions:
			order = append(order, a.kind)
			got[a.target] = a.action
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for action %d; got %+v", i+1, got)
		}
	}
	if got["addon-running"] != "restart" {
		t.Fatalf("running addon action = %q, want restart", got["addon-running"])
	}
	if got["addon-stopped"] != "start" {
		t.Fatalf("stopped addon action = %q, want start", got["addon-stopped"])
	}
	if got[child.Slug] != "restart" {
		t.Fatalf("service action = %q, want restart", got[child.Slug])
	}
	if len(order) != 3 || order[0] != "addon" || order[1] != "addon" || order[2] != "app" {
		t.Fatalf("action order = %v, want addons before service", order)
	}

	addonResults := 0
	for _, r := range result.Results {
		if _, ok := r["addon"]; ok {
			addonResults++
			if r["ok"] != true {
				t.Fatalf("addon result = %+v, want ok", r)
			}
		}
	}
	if addonResults != 2 {
		t.Fatalf("addon results = %d, want 2: %+v", addonResults, result.Results)
	}
}
