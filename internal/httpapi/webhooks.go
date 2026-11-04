package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"phleet/internal/model"
	"phleet/internal/secrets"
)

type githubPush struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Forced     bool   `json:"forced"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type gitlabPush struct {
	ObjectKind string `json:"object_kind"`
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"repository"`
}

func (s *Server) webhookBase(r *http.Request) string {
	if publicURL, _ := s.Edge(); publicURL != "" {
		return strings.TrimRight(publicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) webhookURLs(r *http.Request, projectID string) (github, gitlab string) {
	base := s.webhookBase(r)
	return base + "/hooks/github/" + projectID, base + "/hooks/gitlab/" + projectID
}

func (s *Server) projectWebhookSecret(p model.Project) (string, error) {
	if p.WebhookSecretEnc == "" {
		return "", errors.New("webhook is not configured")
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, p.WebhookSecretEnc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s *Server) handleGetProjectWebhook(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	github, gitlab := s.webhookURLs(r, p.ID)
	writeJSON(w, 200, map[string]any{
		"configured": p.WebhookSecretEnc != "",
		"githubUrl":  github,
		"gitlabUrl":  gitlab,
	})
}

func (s *Server) handleRotateProjectWebhook(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if p.ParentID != "" {
		apiErr(w, 400, errors.New("webhooks are configured on the stack parent, not on a service"))
		return
	}
	if p.SourceType != "git" {
		apiErr(w, 400, errors.New("webhooks need a git source"))
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		apiErr(w, 500, err)
		return
	}
	secret := hex.EncodeToString(buf)
	enc, err := secrets.Encrypt(s.cfg.SessionKey, []byte(secret))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	p.WebhookSecretEnc = enc
	if err := s.st.UpdateProject(p); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "project.webhook", p.ID, "secret rotated")
	github, gitlab := s.webhookURLs(r, p.ID)
	writeJSON(w, 200, map[string]any{
		"configured": true, "secret": secret,
		"githubUrl": github, "gitlabUrl": gitlab,
	})
}

func (s *Server) handleGithubWebhook(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	secret, err := s.projectWebhookSecret(p)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	got := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(got), []byte(want)) {
		apiErr(w, 401, errors.New("invalid webhook signature"))
		return
	}
	if event := strings.TrimSpace(r.Header.Get("X-GitHub-Event")); event != "" && event != "push" {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "not a push event"})
		return
	}
	var payload githubPush
	if err := json.Unmarshal(body, &payload); err != nil {
		apiErr(w, 400, err)
		return
	}
	if payload.Deleted || payload.After == "" {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "branch deleted"})
		return
	}
	if !webhookRefMatches(p, payload.Ref) {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "branch does not match"})
		return
	}
	n, err := s.deployAtRef(p, payload.After)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	_ = s.st.AddEvent("webhook", "deploy.start", p.ID, fmt.Sprintf("github %s (%s)", payload.After, payload.Repository.FullName))
	writeJSON(w, 202, map[string]any{"ok": true, "deployments": n})
}

func (s *Server) handleGitlabWebhook(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	secret, err := s.projectWebhookSecret(p)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	if !secureEq(strings.TrimSpace(r.Header.Get("X-Gitlab-Token")), secret) {
		apiErr(w, 401, errors.New("invalid webhook token"))
		return
	}
	if event := strings.TrimSpace(r.Header.Get("X-Gitlab-Event")); event != "" && !strings.EqualFold(event, "push hook") {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "not a push event"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	var payload gitlabPush
	if err := json.Unmarshal(body, &payload); err != nil {
		apiErr(w, 400, err)
		return
	}
	if payload.ObjectKind != "" && payload.ObjectKind != "push" {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "not a push event"})
		return
	}
	if payload.After == "" || strings.Trim(payload.After, "0") == "" {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "branch deleted"})
		return
	}
	if !webhookRefMatches(p, payload.Ref) {
		writeJSON(w, 202, map[string]any{"ok": true, "ignored": "branch does not match"})
		return
	}
	n, err := s.deployAtRef(p, payload.After)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	_ = s.st.AddEvent("webhook", "deploy.start", p.ID, fmt.Sprintf("gitlab %s (%s)", payload.After, payload.Repository.Name))
	writeJSON(w, 202, map[string]any{"ok": true, "deployments": n})
}

func webhookRefMatches(p model.Project, ref string) bool {
	branch := strings.TrimSpace(p.Branch)
	if branch == "" {
		branch = "main"
	}
	return ref == "refs/heads/"+branch
}

// deployAtRef triggers one deployment per online replica. A stack deploys all
// of its child services at the same commit.
func (s *Server) deployAtRef(p model.Project, ref string) (int, error) {
	targets := []model.Project{p}
	if p.Kind == "stack" {
		children, err := s.st.ListChildProjects(p.ID)
		if err != nil {
			return 0, err
		}
		if len(children) == 0 {
			return 0, errors.New("this stack has no services")
		}
		targets = children
	}
	total := 0
	for _, target := range targets {
		if target.SourceType != "git" {
			return total, fmt.Errorf("%s needs a git source for webhook deploys", target.Name)
		}
		if !projectDeployable(target) {
			return total, fmt.Errorf("%s has no deployable source", target.Name)
		}
		if s.hasInflight(target.ID) {
			return total, fmt.Errorf("%s already has a deployment in progress", target.Name)
		}
		placements, err := s.ensurePlacements(target)
		if err != nil {
			return total, err
		}
		online := 0
		for _, pl := range placements {
			if !s.hub.Online(pl.ServerID) {
				continue
			}
			if _, err := s.startDeployment(target, pl, ref); err != nil {
				return total, fmt.Errorf("%s: %w", target.Name, err)
			}
			online++
		}
		if online == 0 {
			return total, fmt.Errorf("%s has no online replica", target.Name)
		}
		total += online
	}
	return total, nil
}
