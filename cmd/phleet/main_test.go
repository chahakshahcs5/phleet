package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"phleet/internal/config"
	"phleet/internal/store"
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

func TestEncryptedBackupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("probe", "value"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(dir, "test.db.enc")
	if code := backupDB([]string{"-data", dir, "-out", backup}); code != 0 {
		t.Fatalf("backup exit = %d", code)
	}
	restored := filepath.Join(dir, "restored.db")
	if code := restoreBackup([]string{"-data", dir, "-in", backup, "-out", restored}); code != 0 {
		t.Fatalf("restore exit = %d", code)
	}
	st2, err := store.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	v, err := st2.GetSetting("probe")
	if err != nil || v != "value" {
		t.Fatalf("restored setting = %q, err = %v", v, err)
	}
}

func TestBumpSessionEpoch(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := bumpSessionEpoch(cfg); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, err := st.GetSetting(config.SessionEpochSetting)
	if err != nil {
		t.Fatal(err)
	}
	if v == "" || v == "0" {
		t.Fatalf("session epoch = %q, want a non-zero value", v)
	}
}
