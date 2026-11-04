package httpapi

import (
	"strings"
	"testing"
)

func TestRemoteBootstrapArgsOmitsToken(t *testing.T) {
	args := remoteBootstrapArgs("https://fleet.example.com", "pixel")
	if strings.Contains(args, "--token") {
		t.Fatalf("remote args still contain --token: %s", args)
	}
	if !strings.Contains(args, "--server") || !strings.Contains(args, "--name") {
		t.Fatalf("remote args missing fields: %s", args)
	}
}

func TestBootstrapPayloadCarriesTokenOnStdin(t *testing.T) {
	script := []byte("#!/bin/sh\nset -eu\necho ok\n")
	payload := string(bootstrapPayload(script, "secret-token"))
	if !strings.HasPrefix(payload, "PHLEET_BOOTSTRAP_TOKEN='secret-token'\n") {
		t.Fatalf("payload prefix = %q", payload[:40])
	}
	if !strings.HasSuffix(payload, string(script)) {
		t.Fatal("payload did not preserve the bootstrap script")
	}
}
