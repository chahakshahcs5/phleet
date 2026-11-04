package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestRotateTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin_token")
	first, err := rotateTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != first+"\n" {
		t.Fatalf("file content = %q, want token + newline", b)
	}
	if len(first) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}

	second, err := rotateTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("rotation produced the same token twice")
	}
}
