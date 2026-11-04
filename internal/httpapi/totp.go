package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"phleet/internal/model"
	"phleet/internal/secrets"
	"phleet/internal/totp"
)

const (
	settingTOTPSecret   = "admin_totp_secret"
	settingTOTPPending  = "admin_totp_pending"
	settingTOTPLastStep = "admin_totp_last_step"
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
	step, ok := totp.ValidateStep(secret, code, time.Now())
	if !ok {
		return false, nil
	}
	// Serialize the compare-and-set so two concurrent logins cannot both
	// accept the same one-time code.
	s.totpMu.Lock()
	defer s.totpMu.Unlock()
	lastRaw, _ := s.st.GetSetting(settingTOTPLastStep)
	last, _ := strconv.ParseInt(strings.TrimSpace(lastRaw), 10, 64)
	if int64(step) <= last {
		return false, nil
	}
	if err := s.st.SetSetting(settingTOTPLastStep, strconv.FormatUint(step, 10)); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) validateUserTOTP(u model.User, code string) (bool, error) {
	if u.TOTPSecret == "" {
		return true, nil
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, u.TOTPSecret)
	if err != nil {
		return false, err
	}
	step, ok := totp.ValidateStep(string(raw), code, time.Now())
	if !ok {
		return false, nil
	}
	s.totpMu.Lock()
	defer s.totpMu.Unlock()
	fresh, err := s.st.GetUser(u.ID)
	if err != nil || fresh.Disabled || fresh.TOTPSecret == "" {
		return false, nil
	}
	if int64(step) <= fresh.TOTPLastStep {
		return false, nil
	}
	fresh.TOTPLastStep = int64(step)
	if err := s.st.UpdateUser(fresh); err != nil {
		return false, err
	}
	return true, nil
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
	step, ok := totp.ValidateStep(string(raw), body.Code, time.Now())
	if !ok {
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
		u.TOTPLastStep = int64(step)
		if err := s.st.UpdateUser(u); err != nil {
			apiErr(w, 500, err)
			return
		}
	} else {
		if err := s.st.SetSetting(settingTOTPSecret, enc); err != nil {
			apiErr(w, 500, err)
			return
		}
		if err := s.st.SetSetting(settingTOTPLastStep, strconv.FormatUint(step, 10)); err != nil {
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
		u.TOTPLastStep = 0
		_ = s.st.UpdateUser(u)
	} else {
		_ = s.st.DeleteSetting(settingTOTPSecret)
		_ = s.st.DeleteSetting(settingTOTPPending)
		_ = s.st.DeleteSetting(settingTOTPLastStep)
	}
	_ = s.st.AddEvent("ui", "totp.disable", "", "")
	writeJSON(w, 200, map[string]any{"enabled": false})
}
