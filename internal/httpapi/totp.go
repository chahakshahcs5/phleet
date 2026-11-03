package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"phleet/internal/model"
	"phleet/internal/secrets"
	"phleet/internal/totp"
)

const (
	settingTOTPSecret  = "admin_totp_secret"
	settingTOTPPending = "admin_totp_pending"
)

func (s *Server) totpEnabled() bool {
	v, err := s.st.GetSetting(settingTOTPSecret)
	return err == nil && strings.TrimSpace(v) != ""
}

func (s *Server) totpSecret() (string, error) {
	v, err := s.st.GetSetting(settingTOTPSecret)
	if err != nil {
		return "", err
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s *Server) validateTOTP(code string) (bool, error) {
	secret, err := s.totpSecret()
	if err != nil {
		return false, err
	}
	return totp.Validate(secret, code, time.Now()), nil
}

func (s *Server) validateUserTOTP(u model.User, code string) (bool, error) {
	if u.TOTPSecret == "" {
		return true, nil
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, u.TOTPSecret)
	if err != nil {
		return false, err
	}
	return totp.Validate(string(raw), code, time.Now()), nil
}

func (s *Server) handleGetTOTP(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.currentUser(r); ok {
		writeJSON(w, 200, map[string]any{"enabled": u.TOTPSecret != ""})
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": s.totpEnabled()})
}

func (s *Server) handleSetupTOTP(w http.ResponseWriter, r *http.Request) {
	secret, err := totp.GenerateSecret()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	enc, err := secrets.Encrypt(s.cfg.SessionKey, []byte(secret))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if u, ok := s.currentUser(r); ok {
		u.TOTPPending = enc
		if err := s.st.UpdateUser(u); err != nil {
			apiErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"secret": secret,
			"url":    totp.ProvisioningURL("phleet", u.Username, secret),
		})
		return
	}
	if err := s.st.SetSetting(settingTOTPPending, enc); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"secret": secret,
		"url":    totp.ProvisioningURL("phleet", "admin", secret),
	})
}

func (s *Server) handleEnableTOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &body, 16<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	var pending string
	if u, ok := s.currentUser(r); ok {
		pending = u.TOTPPending
	} else {
		pending, _ = s.st.GetSetting(settingTOTPPending)
	}
	if pending == "" {
		apiErr(w, 400, errors.New("run setup first"))
		return
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, pending)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if !totp.Validate(string(raw), body.Code, time.Now()) {
		apiErr(w, 401, errors.New("invalid code"))
		return
	}
	enc, err := secrets.Encrypt(s.cfg.SessionKey, raw)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if u, ok := s.currentUser(r); ok {
		u.TOTPSecret = enc
		u.TOTPPending = ""
		if err := s.st.UpdateUser(u); err != nil {
			apiErr(w, 500, err)
			return
		}
	} else {
		if err := s.st.SetSetting(settingTOTPSecret, enc); err != nil {
			apiErr(w, 500, err)
			return
		}
		_ = s.st.DeleteSetting(settingTOTPPending)
	}
	_ = s.st.AddEvent("ui", "totp.enable", "", "")
	writeJSON(w, 200, map[string]any{"enabled": true})
}

func (s *Server) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.currentUser(r); ok {
		u.TOTPSecret = ""
		u.TOTPPending = ""
		_ = s.st.UpdateUser(u)
	} else {
		_ = s.st.DeleteSetting(settingTOTPSecret)
		_ = s.st.DeleteSetting(settingTOTPPending)
	}
	_ = s.st.AddEvent("ui", "totp.disable", "", "")
	writeJSON(w, 200, map[string]any{"enabled": false})
}
