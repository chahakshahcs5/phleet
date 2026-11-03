package totp

import (
	"testing"
	"time"
)

func TestRFC6238Vector(t *testing.T) {
	// RFC 6238 test vector for 6 digits at T=59.
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := Code(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("code = %q, want 287082", code)
	}
	if !Validate(secret, code, time.Unix(59, 0)) {
		t.Fatal("valid code rejected")
	}
	if Validate(secret, "000000", time.Unix(59, 0)) {
		t.Fatal("wrong code accepted")
	}
}

func TestGeneratedSecretIsUsable(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !Validate(secret, code, now) {
		t.Fatal("generated secret did not validate")
	}
	if ProvisioningURL("phleet", "admin", secret) == "" {
		t.Fatal("empty provisioning URL")
	}
}
