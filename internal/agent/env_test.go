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
		"PATH=/usr/bin",
	}
	got := sanitizeEnv(in)
	want := []string{"HOME=/home/phleet", "PATH=/usr/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitizeEnv = %#v, want %#v", got, want)
	}
}
