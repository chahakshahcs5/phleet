package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"phleet/internal/secrets"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func (s *Server) handleListProjectSecrets(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	list, err := s.st.ListProjectSecrets(p.ID)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{"name": x.Name, "updatedAt": x.UpdatedAt})
	}
	writeJSON(w, 200, map[string]any{"secrets": out})
}

func (s *Server) handlePutProjectSecret(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if !secretNamePattern.MatchString(name) {
		apiErr(w, 400, errors.New("secret name must use letters, numbers, dot, dash or underscore"))
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	enc, err := secrets.Encrypt(s.cfg.SessionKey, []byte(body.Content))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if err := s.st.SetProjectSecret(p.ID, name, enc); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "project.secret", p.ID, name)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleDeleteProjectSecret(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := s.st.DeleteProjectSecret(p.ID, name); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "project.secret_delete", p.ID, name)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) projectSecrets(projectID string) (map[string]string, error) {
	list, err := s.st.ListProjectSecrets(projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, x := range list {
		raw, err := secrets.Decrypt(s.cfg.SessionKey, x.ContentEnc)
		if err != nil {
			return nil, err
		}
		out[x.Name] = string(raw)
	}
	return out, nil
}
