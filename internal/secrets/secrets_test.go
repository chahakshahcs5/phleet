package secrets

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key := []byte("test-key-0123456789")
	plain := []byte(`{"DATABASE_URL":"postgres://x","TOKEN":"s3cret"}`)
	enc, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc == string(plain) {
		t.Fatal("plaintext leaked")
	}
	dec, err := Decrypt(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("roundtrip mismatch: %q", dec)
	}
	if _, err := Decrypt([]byte("other-key"), enc); err == nil {
		t.Fatal("expected failure with the wrong key")
	}
	if empty, err := Decrypt(key, ""); err != nil || len(empty) != 0 {
		t.Fatalf("empty ciphertext should decrypt to empty: %v %q", err, empty)
	}
}
