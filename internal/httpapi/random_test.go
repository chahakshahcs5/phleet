package httpapi

import (
	"errors"
	"testing"
)

func TestRandomHex(t *testing.T) {
	tok, err := randomHex(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 32 {
		t.Fatalf("token length = %d, want 32", len(tok))
	}
}

func TestRandomHexFailsClosed(t *testing.T) {
	old := randomRead
	randomRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randomRead = old })

	if _, err := randomHex(8); err == nil {
		t.Fatal("randomHex must return an error when crypto/rand fails")
	}
}
