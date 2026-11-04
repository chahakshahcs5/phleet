package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"phleet/internal/model"
)

const (
	settingAlertRules = "alert_rules"
	settingNotify     = "notify"
)

func defaultRules() model.AlertRules {
	return model.AlertRules{TempC: 60, BatteryPct: 20, DiskPct: 90, OfflineSec: 120}
}

func (s *Server) loadRules() model.AlertRules {
	rules := defaultRules()
	raw, err := s.st.GetSetting(settingAlertRules)
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &rules)
	}
	if rules.TempC <= 0 {
		rules.TempC = 60
	}
	if rules.BatteryPct <= 0 {
		rules.BatteryPct = 20
	}
	if rules.DiskPct <= 0 {
		rules.DiskPct = 90
	}
	if rules.OfflineSec <= 0 {
		rules.OfflineSec = 120
	}
	return rules
}

func (s *Server) loadNotify() model.NotifyConfig {
	var cfg model.NotifyConfig
	if raw, err := s.st.GetSetting(settingNotify); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg
}

func (s *Server) alertLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-time.After(8 * time.Second):
	}
	s.evaluateAlerts()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.evaluateAlerts()
		}
	}
}

func (s *Server) evaluateAlerts() {
	rules := s.loadRules()
	servers, err := s.st.ListServers()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, srv := range servers {
		online := s.hub.Online(srv.ID)
		offlineKey := "server:" + srv.ID + ":offline"
		if !online && now-srv.LastSeen >= int64(rules.OfflineSec) {
			s.fireAlert(offlineKey, "server_offline", "critical", srv.ID,
				fmt.Sprintf("%s has been offline for %s", srv.Name, humanSeconds(now-srv.LastSeen)))
		} else {
			s.resolveAlert(offlineKey)
		}
		latest, err := s.st.LatestMetrics(srv.ID)
		if err != nil {
			continue
		}
		tempKey := "server:" + srv.ID + ":temp"
		if latest.TempC > 0 && latest.TempC >= rules.TempC {
			sev := "warning"
			if latest.TempC >= rules.TempC+10 {
				sev = "critical"
			}
			s.fireAlert(tempKey, "high_temp", sev, srv.ID,
				fmt.Sprintf("%s is at %.1f°C (limit %.0f°C)", srv.Name, latest.TempC, rules.TempC))
		} else {
			s.resolveAlert(tempKey)
		}
		battKey := "server:" + srv.ID + ":battery"
		if latest.BatteryPct >= 0 && latest.BatteryPct < rules.BatteryPct && !latest.Charging {
			s.fireAlert(battKey, "low_battery", "warning", srv.ID,
				fmt.Sprintf("%s battery below %d%% (%d%%), not charging", srv.Name, rules.BatteryPct, latest.BatteryPct))
		} else {
			s.resolveAlert(battKey)
		}
		diskKey := "server:" + srv.ID + ":disk"
		if latest.DiskTotal > 0 {
			usedPct := float64(latest.DiskUsed) / float64(latest.DiskTotal) * 100
			if usedPct >= float64(rules.DiskPct) {
				sev := "warning"
				if usedPct >= 95 {
					sev = "critical"
				}
				s.fireAlert(diskKey, "disk_full", sev, srv.ID,
					fmt.Sprintf("%s disk is %.0f%% full (%s free)", srv.Name, usedPct, bytesShort(latest.DiskTotal-latest.DiskUsed)))
			} else {
				s.resolveAlert(diskKey)
			}
		}
	}

	placements, _ := s.st.ListPlacements()
	projects, _ := s.st.ListProjects()
	names := map[string]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	crashed := map[string]bool{}
	for _, pl := range placements {
		key := "app:" + pl.ProjectID + ":crashed"
		if pl.Status == "crashed" {
			crashed[pl.ProjectID] = true
			name := names[pl.ProjectID]
			s.fireAlert(key, "app_crashed", "warning", pl.ProjectID,
				fmt.Sprintf("%s is crash-looping on %s", name, pl.ServerID))
		} else if pl.Status == "running" || pl.Status == "stopped" {
			s.resolveAlert(key)
		}
	}
	for _, p := range projects {
		if !crashed[p.ID] {
			s.resolveAlert("app:" + p.ID + ":crashed")
		}
		key := "deploy:" + p.ID
		dep, err := s.st.LatestDeployment(p.ID)
		if err != nil {
			s.resolveAlert(key)
			continue
		}
		if dep.Status == "failed" {
			msg := p.Name + " failed to deploy"
			if dep.Detail != "" {
				msg += ": " + dep.Detail
			}
			s.fireAlert(key, "deploy_failed", "critical", p.ID, msg)
		} else {
			s.resolveAlert(key)
		}
	}

	// Data services fail differently from apps: a crash-looping engine and a
	// phone that is reachable but not serving the addon both matter.
	if addons, err := s.st.ListAddons(); err == nil {
		for _, a := range addons {
			key := "addon:" + a.ID
			switch a.Status {
			case "failed", "crashed":
				msg := fmt.Sprintf("%s (%s) is not healthy", a.Name, a.Engine)
				if a.LastError != "" {
					msg += ": " + a.LastError
				}
				s.fireAlert(key, "addon_failed", "critical", a.ID, msg)
			case "unreachable":
				s.fireAlert(key, "addon_unreachable", "warning", a.ID,
					fmt.Sprintf("%s is unreachable on %s", a.Name, a.PinnedServer))
			default:
				s.resolveAlert(key)
			}
		}
	}
}

func (s *Server) fireAlert(key, kind, severity, target, message string) {
	created, err := s.st.UpsertAlert(key, kind, severity, target, message)
	if err != nil || !created {
		return
	}
	_ = s.st.AddEvent("alerts", "alert.open", target, message)
	if a, err := s.st.GetAlertByKey(key); err == nil {
		s.notify(a, "open")
	}
}

func (s *Server) resolveAlert(key string) {
	changed, err := s.st.ResolveAlert(key)
	if err != nil || !changed {
		return
	}
	a, err := s.st.GetAlertByKey(key)
	if err != nil {
		return
	}
	_ = s.st.AddEvent("alerts", "alert.resolved", a.Target, a.Message)
	s.notify(a, "resolved")
}

// startDeployAlertResolve clears the deploy failure alert after a successful
// deploy or rollback.
func (s *Server) startDeployAlertResolve(projectID string) {
	s.resolveAlert("deploy:" + projectID)
}

func (s *Server) notify(a model.Alert, event string) {
	cfg := s.loadNotify()
	if !hasNotifyChannel(cfg) {
		return
	}
	now := time.Now()
	if cfg.MaintenanceUntil > 0 && now.Unix() < cfg.MaintenanceUntil {
		return
	}
	if inQuietHours(cfg, now) {
		return
	}
	text := fmt.Sprintf("[%s] %s — %s", strings.ToUpper(event), a.Severity, a.Message)
	s.sendAll(cfg, a, event, text)
}

func hasNotifyChannel(cfg model.NotifyConfig) bool {
	return (cfg.TelegramToken != "" && cfg.TelegramChat != "") ||
		cfg.WebhookURL != "" || cfg.NtfyURL != "" ||
		(cfg.GotifyURL != "" && cfg.GotifyToken != "") ||
		cfg.DiscordURL != "" || cfg.SlackURL != "" ||
		(cfg.SMTPHost != "" && cfg.EmailFrom != "" && cfg.EmailTo != "")
}

func inQuietHours(cfg model.NotifyConfig, now time.Time) bool {
	if !cfg.QuietHours || cfg.QuietStart == cfg.QuietEnd {
		return false
	}
	h := now.Hour()
	start, end := cfg.QuietStart, cfg.QuietEnd
	if start < end {
		return h >= start && h < end
	}
	return h >= start || h < end
}

func (s *Server) sendAll(cfg model.NotifyConfig, a model.Alert, event, text string) {
	if cfg.TelegramToken != "" && cfg.TelegramChat != "" {
		go s.sendTelegram(cfg, text)
	}
	if cfg.WebhookURL != "" {
		go s.sendWebhook(cfg, a, event, text)
	}
	if cfg.NtfyURL != "" {
		go s.sendNtfy(cfg, text)
	}
	if cfg.GotifyURL != "" && cfg.GotifyToken != "" {
		go s.sendGotify(cfg, text)
	}
	if cfg.DiscordURL != "" {
		go s.sendJSONWebhook(cfg.DiscordURL, map[string]any{"content": text})
	}
	if cfg.SlackURL != "" {
		go s.sendJSONWebhook(cfg.SlackURL, map[string]any{"text": text})
	}
	if cfg.SMTPHost != "" && cfg.EmailFrom != "" && cfg.EmailTo != "" {
		go s.sendEmail(cfg, text)
	}
}

func (s *Server) sendNtfy(cfg model.NotifyConfig, text string) {
	req, err := http.NewRequest(http.MethodPost, cfg.NtfyURL, strings.NewReader(text))
	if err != nil {
		s.log.Warn("ntfy notify", "err", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", "phleet alert")
	resp, err := notifyClient().Do(req)
	if err != nil {
		s.log.Warn("ntfy notify", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("ntfy notify", "status", resp.StatusCode)
	}
}

func (s *Server) sendGotify(cfg model.NotifyConfig, text string) {
	endpoint := strings.TrimRight(cfg.GotifyURL, "/") + "/message?token=" + url.QueryEscape(cfg.GotifyToken)
	body, _ := json.Marshal(map[string]any{"title": "phleet", "message": text, "priority": 5})
	resp, err := notifyClient().Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		s.log.Warn("gotify notify", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("gotify notify", "status", resp.StatusCode)
	}
}

func (s *Server) sendJSONWebhook(endpoint string, payload map[string]any) {
	body, _ := json.Marshal(payload)
	resp, err := notifyClient().Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		s.log.Warn("chat webhook notify", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("chat webhook notify", "status", resp.StatusCode)
	}
}

func (s *Server) sendEmail(cfg model.NotifyConfig, text string) {
	port := cfg.SMTPPort
	if port <= 0 {
		port = 587
	}
	host := cfg.SMTPHost
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, host)
	}
	recipients := []string{}
	for _, to := range strings.Split(cfg.EmailTo, ",") {
		if to = strings.TrimSpace(to); to != "" {
			recipients = append(recipients, to)
		}
	}
	if len(recipients) == 0 {
		return
	}
	subject := "phleet alert"
	msg := []byte("From: " + cfg.EmailFrom + "\r\n" +
		"To: " + strings.Join(recipients, ", ") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		text + "\r\n")
	if err := smtp.SendMail(addr, auth, cfg.EmailFrom, recipients, msg); err != nil {
		s.log.Warn("email notify", "err", err)
	}
}

func (s *Server) sendTelegram(cfg model.NotifyConfig, text string) {
	form := url.Values{}
	form.Set("chat_id", cfg.TelegramChat)
	form.Set("text", text)
	form.Set("disable_web_page_preview", "true")
	endpoint := "https://api.telegram.org/bot" + cfg.TelegramToken + "/sendMessage"
	resp, err := notifyClient().PostForm(endpoint, form)
	if err != nil {
		s.log.Warn("telegram notify", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("telegram notify", "status", resp.StatusCode)
	}
}

func (s *Server) sendWebhook(cfg model.NotifyConfig, a model.Alert, event, text string) {
	body, _ := json.Marshal(map[string]any{"event": event, "text": text, "alert": a})
	resp, err := notifyClient().Post(cfg.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		s.log.Warn("webhook notify", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("webhook notify", "status", resp.StatusCode)
	}
}

func notifyClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

// ---- API ----

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	active := r.URL.Query().Get("active") == "1"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	alerts, err := s.st.ListAlerts(active, limit)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	count, _ := s.st.CountActiveAlerts()
	writeJSON(w, 200, map[string]any{"alerts": alerts, "active": count})
}

func (s *Server) handleAlertCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountActiveAlerts()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"count": n})
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiErr(w, 400, errors.New("invalid alert id"))
		return
	}
	if err := s.st.AckAlert(id); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "alert.ack", r.PathValue("id"), "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleGetRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.loadRules())
}

func (s *Server) handlePutRules(w http.ResponseWriter, r *http.Request) {
	rules := defaultRules()
	if err := readJSON(r, &rules, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	if rules.TempC <= 0 || rules.BatteryPct <= 0 || rules.DiskPct <= 0 || rules.OfflineSec <= 0 {
		apiErr(w, 400, errors.New("all thresholds must be positive"))
		return
	}
	b, _ := json.Marshal(rules)
	if err := s.st.SetSetting(settingAlertRules, string(b)); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "alert.rules_update", "", string(b))
	writeJSON(w, 200, rules)
}

func (s *Server) handleGetNotify(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.loadNotify())
}

func (s *Server) handlePutNotify(w http.ResponseWriter, r *http.Request) {
	var cfg model.NotifyConfig
	if err := readJSON(r, &cfg, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	cfg.TelegramToken = strings.TrimSpace(cfg.TelegramToken)
	cfg.TelegramChat = strings.TrimSpace(cfg.TelegramChat)
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	cfg.NtfyURL = strings.TrimSpace(cfg.NtfyURL)
	cfg.GotifyURL = strings.TrimSpace(cfg.GotifyURL)
	cfg.GotifyToken = strings.TrimSpace(cfg.GotifyToken)
	cfg.DiscordURL = strings.TrimSpace(cfg.DiscordURL)
	cfg.SlackURL = strings.TrimSpace(cfg.SlackURL)
	cfg.SMTPHost = strings.TrimSpace(cfg.SMTPHost)
	cfg.SMTPUser = strings.TrimSpace(cfg.SMTPUser)
	cfg.EmailFrom = strings.TrimSpace(cfg.EmailFrom)
	cfg.EmailTo = strings.TrimSpace(cfg.EmailTo)
	if cfg.SMTPPort <= 0 || cfg.SMTPPort > 65535 {
		cfg.SMTPPort = 587
	}
	if cfg.QuietStart < 0 || cfg.QuietStart > 23 {
		cfg.QuietStart = 22
	}
	if cfg.QuietEnd < 0 || cfg.QuietEnd > 23 {
		cfg.QuietEnd = 7
	}
	if cfg.MaintenanceUntil < 0 {
		cfg.MaintenanceUntil = 0
	}
	b, _ := json.Marshal(cfg)
	if err := s.st.SetSetting(settingNotify, string(b)); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadNotify()
	if !hasNotifyChannel(cfg) {
		apiErr(w, 400, errors.New("configure at least one notification channel first"))
		return
	}
	text := "phleet test notification — " + time.Now().Format(time.RFC3339)
	// A test bypasses quiet hours and maintenance mode on purpose.
	s.sendAll(cfg, model.Alert{Kind: "test", Severity: "info", Message: text, Status: "test"}, "test", text)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func humanSeconds(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm", sec/60)
	}
	if sec < 86400 {
		return fmt.Sprintf("%dh", sec/3600)
	}
	return fmt.Sprintf("%dd", sec/86400)
}

func bytesShort(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
