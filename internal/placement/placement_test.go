package placement

import (
	"reflect"
	"testing"
)

func TestParseSelector(t *testing.T) {
	m, err := ParseSelector("tier=web, region=home")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, map[string]string{"tier": "web", "region": "home"}) {
		t.Fatalf("unexpected selector: %#v", m)
	}
	if got := FormatSelector(m); got != "region=home,tier=web" {
		t.Fatalf("unexpected format: %q", got)
	}
	if _, err := ParseSelector("tier:web"); err == nil {
		t.Fatal("expected error for malformed selector")
	}
	if empty, _ := ParseSelector(" , "); len(empty) != 0 {
		t.Fatalf("expected empty selector, got %#v", empty)
	}
}

func TestMatch(t *testing.T) {
	labels := map[string]string{"tier": "web", "region": "home"}
	if !Match(map[string]string{"tier": "web"}, labels) {
		t.Fatal("expected match")
	}
	if !Match(nil, labels) {
		t.Fatal("empty selector must match everything")
	}
	if Match(map[string]string{"tier": "db"}, labels) {
		t.Fatal("expected mismatch")
	}
}

func TestPlanPrefersLeastLoadedThenCoolest(t *testing.T) {
	servers := []Server{
		{ID: "a", Online: true, Labels: map[string]string{"tier": "web"}, Score: 10, Placements: 1},
		{ID: "b", Online: true, Labels: map[string]string{"tier": "web"}, Score: 50, Placements: 0},
		{ID: "c", Online: true, Labels: map[string]string{"tier": "web"}, Score: 5, Placements: 1},
		{ID: "d", Online: false, Labels: map[string]string{"tier": "web"}},
		{ID: "e", Online: true, Drained: true, Labels: map[string]string{"tier": "web"}},
		{ID: "f", Online: true, Labels: map[string]string{"tier": "db"}},
	}
	got := Plan(servers, Need{Selector: map[string]string{"tier": "web"}, Replicas: 3})
	want := []string{"b", "c", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
}

func TestPlanRespectsExistingAndPinned(t *testing.T) {
	servers := []Server{
		{ID: "a", Online: true, Score: 1},
		{ID: "b", Online: true, Score: 2},
		{ID: "c", Online: true, Score: 3},
	}
	if got := Plan(servers, Need{Replicas: 2, Existing: map[string]bool{"a": true}}); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("existing plan = %v", got)
	}
	if got := Plan(servers, Need{Replicas: 2, Pinned: "c"}); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("pinned plan = %v", got)
	}
	if got := Plan(servers, Need{Replicas: 2, Existing: map[string]bool{"a": true, "b": true}}); got != nil {
		t.Fatalf("expected no additions, got %v", got)
	}
}

func TestEligible(t *testing.T) {
	s := Server{ID: "a", Online: true, Labels: map[string]string{"tier": "web"}}
	sel := map[string]string{"tier": "web"}
	if !Eligible(s, sel, "") {
		t.Fatal("expected eligible")
	}
	s.Drained = true
	if Eligible(s, sel, "") {
		t.Fatal("drained server must not be eligible")
	}
	s.Drained = false
	if Eligible(s, sel, "b") {
		t.Fatal("pinned mismatch must not be eligible")
	}
	if Eligible(s, map[string]string{"tier": "db"}, "") {
		t.Fatal("selector mismatch must not be eligible")
	}
}

// A rooted phone without Termux reports userland=false. It is a valid node for
// metrics and shell, but it must never be scheduled a project.
func TestMonitoringOnlyPhoneIsNotScheduled(t *testing.T) {
	servers := []Server{
		{ID: "monitor", Online: true, NoUserland: true, Score: 1},
		{ID: "phone", Online: true, Score: 100},
	}
	if got := Plan(servers, Need{Replicas: 1}); !reflect.DeepEqual(got, []string{"phone"}) {
		t.Fatalf("plan = %v, want [phone]", got)
	}
	if got := Plan(servers, Need{Replicas: 1, Pinned: "monitor"}); len(got) != 0 {
		t.Fatalf("pinned monitoring phone must not be planned, got %v", got)
	}
	if Eligible(servers[0], nil, "") {
		t.Fatal("monitoring-only phone must not be eligible")
	}
}
