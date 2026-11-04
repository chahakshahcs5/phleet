package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"phleet/internal/model"
)

func TestGitAuthCreateAndDecrypt(t *testing.T) {
	s := testServer(t)
	kind, user, enc, err := s.setNewGitAuth("https", "", "token-value")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "https" || user != "git" || enc == "" {
		t.Fatalf("git auth = %q %q %q", kind, user, enc)
	}
	auth, err := s.deployGitAuth(model.Project{GitAuthType: kind, GitAuthUsername: user, GitAuthEnc: enc})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Type != "https" || auth.Username != "git" || auth.Secret != "token-value" {
		t.Fatalf("decrypted auth = %+v", auth)
	}
}

func TestGitAuthUpdateKeepsExistingSecret(t *testing.T) {
	s := testServer(t)
	kind, user, enc, err := s.setNewGitAuth("https", "deploy", "token-value")
	if err != nil {
		t.Fatal(err)
	}
	p := model.Project{GitAuthType: kind, GitAuthUsername: user, GitAuthEnc: enc}
	nextType := "https"
	newUser := "other"
	if err := s.applyGitAuthUpdate(&p, &nextType, &newUser, nil); err != nil {
		t.Fatal(err)
	}
	if p.GitAuthUsername != "other" || p.GitAuthEnc != enc {
		t.Fatalf("update changed the secret or username incorrectly: %+v", p)
	}
}

func TestGitAuthClear(t *testing.T) {
	s := testServer(t)
	p := model.Project{GitAuthType: "https", GitAuthUsername: "git", GitAuthEnc: "ciphertext"}
	none := "none"
	if err := s.applyGitAuthUpdate(&p, &none, nil, nil); err != nil {
		t.Fatal(err)
	}
	if p.GitAuthType != "" || p.GitAuthUsername != "" || p.GitAuthEnc != "" {
		t.Fatalf("git auth was not cleared: %+v", p)
	}
}

func TestGitAuthSecretIsNotSerialized(t *testing.T) {
	s := testServer(t)
	kind, user, enc, err := s.setNewGitAuth("https", "deploy", "super-secret-token")
	if err != nil {
		t.Fatal(err)
	}
	p := model.Project{ID: "p1", Name: "demo", Slug: "demo", GitAuthType: kind, GitAuthUsername: user, GitAuthEnc: enc}
	raw, err := json.Marshal(s.buildProjectView(p, false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-token") || strings.Contains(string(raw), "gitAuthEnc") {
		t.Fatalf("project view leaked git credentials: %s", raw)
	}
	if !strings.Contains(string(raw), `"gitAuth":true`) {
		t.Fatalf("project view does not advertise configured auth: %s", raw)
	}
}
