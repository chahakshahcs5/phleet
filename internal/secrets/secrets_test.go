package secrets

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestEncryptedFileRoundTrip(t *testing.T) {
	key := []byte("test-key-0123456789")
	dir := t.TempDir()
	src := filepath.Join(dir, "dump.bin")
	plain := bytes.Repeat([]byte("phone backup data\n"), 200000)
	if err := os.WriteFile(src, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "dump.enc")
	sum, err := EncryptFile(key, src, enc)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(enc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("phone backup data")) {
		t.Fatal("plaintext leaked into encrypted backup")
	}
	f, err := os.Open(enc)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out bytes.Buffer
	if err := DecryptTo(key, f, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Fatalf("roundtrip mismatch: %d bytes", out.Len())
	}
	if sum == "" {
		t.Fatal("missing plaintext checksum")
	}
}
