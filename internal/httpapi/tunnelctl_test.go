package httpapi

import (
	"errors"
	"testing"
)

func TestServiceOutputParsers(t *testing.T) {
	query := []byte("SERVICE_NAME: cloudflared\n        TYPE               : 20  WIN32_SHARE_PROCESS\n        STATE              : 4  RUNNING\n                                (NOT_STOPPABLE, NOT_PAUSABLE)\n")
	if got := parseServiceState(query); got != 4 {
		t.Fatalf("state: got %d, want 4", got)
	}
	if got := serviceStateLabel(4); got != "Running" {
		t.Fatalf("state label: %q", got)
	}

	config := []byte("SERVICE_NAME: cloudflared\n        TYPE               : 20  WIN32_SHARE_PROCESS\n        START_TYPE         : 2   AUTO_START\n")
	if got := parseServiceStartType(config); got != 2 {
		t.Fatalf("start type: got %d, want 2", got)
	}
	if got := serviceStartTypeLabel(2); got != "Automatic" {
		t.Fatalf("start type label: %q", got)
	}

	if !isServiceNotInstalled(errors.New("exit status 1060: The specified service does not exist as an installed service.")) {
		t.Fatal("1060 should be treated as not installed")
	}
	if isServiceNotInstalled(errors.New("exit status 5: Access is denied.")) {
		t.Fatal("access denied must not look like a missing service")
	}
}
