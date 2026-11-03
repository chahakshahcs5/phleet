package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

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

func (s *Server) handleGetTOTP(w http.ResponseWriter, r *http.Request) {
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
	pending, err := s.st.GetSetting(settingTOTPPending)
	if err != nil {
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
	if err := s.st.SetSetting(settingTOTPSecret, enc); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.DeleteSetting(settingTOTPPending)
	_ = s.st.AddEvent("ui", "totp.enable", "", "")
	writeJSON(w, 200, map[string]any{"enabled": true})
}

func (s *Server) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	_ = s.st.DeleteSetting(settingTOTPSecret)
	_ = s.st.DeleteSetting(settingTOTPPending)
	_ = s.st.AddEvent("ui", "totp.disable", "", "")
	writeJSON(w, 200, map[string]any{"enabled": false})
}
