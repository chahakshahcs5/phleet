package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"phleet/internal/auth"
	"phleet/internal/model"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	username := strings.TrimSpace(body.Username)
	if !usernamePattern.MatchString(username) {
		apiErr(w, 400, errors.New("username must be 3-32 letters, numbers, dots, dashes or underscores"))
		return
	}
	role := strings.ToLower(strings.TrimSpace(body.Role))
	if role == "" {
		role = "viewer"
	}
	if role != "admin" && role != "viewer" && role != "deployer" {
		apiErr(w, 400, errors.New("role must be admin, deployer or viewer"))
		return
	}
	if _, err := s.st.GetUserByUsername(username); err == nil {
		apiErr(w, 409, errors.New("username is already in use"))
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	u := model.User{ID: uuid.NewString(), Username: username, PasswordHash: hash, Role: role}
	if err := s.st.CreateUser(u); err != nil {
		apiErr(w, 409, err)
		return
	}
	_ = s.st.AddEvent("ui", "user.create", u.ID, username+" ("+role+")")
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.st.GetUser(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	if body.Role != nil {
		role := strings.ToLower(strings.TrimSpace(*body.Role))
		if role != "admin" && role != "viewer" && role != "deployer" {
			apiErr(w, 400, errors.New("role must be admin, deployer or viewer"))
			return
		}
		u.Role = role
	}
	if body.Disabled != nil {
		if *body.Disabled != u.Disabled {
			u.SessionVersion++
		}
		u.Disabled = *body.Disabled
	}
	if err := s.st.UpdateUser(u); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "user.update", u.ID, u.Username)
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	u, err := s.st.GetUser(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	u.PasswordHash = hash
	u.SessionVersion++
	if err := s.st.UpdateUser(u); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "user.password", u.ID, u.Username)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if u, err := s.st.GetUser(id); err == nil {
		_ = s.st.AddEvent("ui", "user.delete", id, u.Username)
	}
	if err := s.st.DeleteUser(id); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
