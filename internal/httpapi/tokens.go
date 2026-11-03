package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"phleet/internal/model"
)

func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListAPITokens()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": list})
}

func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		apiErr(w, 400, errors.New("name is required"))
		return
	}
	role := strings.ToLower(strings.TrimSpace(body.Role))
	if role != "admin" && role != "viewer" {
		apiErr(w, 400, errors.New("role must be admin or viewer"))
		return
	}
	tok := randomHex(32)
	t := model.APIToken{
		ID: uuid.NewString(), Name: name, Role: role,
	}
	if err := s.st.CreateAPIToken(t, tokenHash(tok)); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "api_token.create", t.ID, name+" ("+role+")")
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "apiToken": t})
}

func (s *Server) handleDeleteAPIToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.st.DeleteAPIToken(id); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "api_token.delete", id, "")
	writeJSON(w, 200, map[string]any{"ok": true})
}
