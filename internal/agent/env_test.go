package agent

import (
	"reflect"
	"testing"
)

func TestSanitizeEnvDropsAgentSecrets(t *testing.T) {
	in := []string{
		"HOME=/home/phleet",
		"PHLEET_TOKEN=secret",
		"PHLEET_TOKEN_FILE=/home/phleet/.phleet/agent.token",
		"PHLEET_SECRET=also-secret",
		"PHLEET_STATE_DIR=/home/phleet/.phleet",
		"PATH=/usr/bin",
	}
	got := sanitizeEnv(in)
	want := []string{"HOME=/home/phleet", "PATH=/usr/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitizeEnv = %#v, want %#v", got, want)
	}
}

func TestEnvToMapKeepsLastValue(t *testing.T) {
	m := envToMap([]string{"HOST=127.0.0.1", "HOST=0.0.0.0", "A=1=2"})
	if m["HOST"] != "0.0.0.0" {
		t.Fatalf("HOST = %q, want last value", m["HOST"])
	}
	if m["A"] != "1=2" {
		t.Fatalf("value containing = was split: %q", m["A"])
	}
	got := envFromMap(m)
	want := []string{"A=1=2", "HOST=0.0.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envFromMap = %#v, want %#v", got, want)
	}
}
