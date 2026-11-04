package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/engines"
	"phleet/internal/model"
)

const (
	settingBackupRemote = "backup_remote"
	settingBackupDrill  = "backup_drill"
)

func (s *Server) loadBackupRemote() model.BackupRemoteConfig {
	cfg := model.BackupRemoteConfig{RclonePath: "rclone"}
	if raw, err := s.st.GetSetting(settingBackupRemote); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	cfg.Target = strings.TrimSpace(cfg.Target)
	cfg.RclonePath = strings.TrimSpace(cfg.RclonePath)
	if cfg.RclonePath == "" {
		cfg.RclonePath = "rclone"
	}
	return cfg
}

func (s *Server) saveBackupRemote(cfg model.BackupRemoteConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.st.SetSetting(settingBackupRemote, string(raw))
}

func (s *Server) loadBackupDrill() model.BackupDrillConfig {
	cfg := model.BackupDrillConfig{IntervalHours: 168}
	if raw, err := s.st.GetSetting(settingBackupDrill); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = 168
	}
	return cfg
}

func (s *Server) saveBackupDrill(cfg model.BackupDrillConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.st.SetSetting(settingBackupDrill, string(raw))
}

func joinRemote(target string, parts ...string) string {
	out := strings.TrimRight(strings.TrimSpace(target), "/")
	for _, p := range parts {
		out += "/" + strings.Trim(p, "/")
	}
	return out
}

func (s *Server) handleGetBackupRemote(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"config": s.loadBackupRemote()})
}

func (s *Server) handlePutBackupRemote(w http.ResponseWriter, r *http.Request) {
	var cfg model.BackupRemoteConfig
	if err := readJSON(r, &cfg, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	cfg.Target = strings.TrimSpace(cfg.Target)
	cfg.RclonePath = strings.TrimSpace(cfg.RclonePath)
	if cfg.RclonePath == "" {
		cfg.RclonePath = "rclone"
	}
	if cfg.Enabled && cfg.Target == "" {
		apiErr(w, 400, errors.New("an rclone target is required when off-site backups are enabled"))
		return
	}
	if err := s.saveBackupRemote(cfg); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "backup.remote", "", fmt.Sprintf("enabled=%v target=%s", cfg.Enabled, cfg.Target))
	writeJSON(w, 200, map[string]any{"config": cfg})
}

func (s *Server) handleSyncBackupRemote(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadBackupRemote()
	if !cfg.Enabled || cfg.Target == "" {
		apiErr(w, 400, errors.New("enable off-site backups first"))
		return
	}
	addons, err := s.st.ListAddons()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	queued := 0
	type item struct {
		addon model.Addon
		b     model.Backup
		path  string
	}
	var items []item
	for _, a := range addons {
		backups, err := s.st.ListBackups(a.ID, 10)
		if err != nil {
			continue
		}
		for _, b := range backups {
			if b.Status != "complete" || b.RemoteStatus == "uploaded" || b.Path == "" {
				continue
			}
			if _, err := os.Stat(b.Path); err != nil {
				continue
			}
			items = append(items, item{addon: a, b: b, path: b.Path})
			queued++
		}
	}
	if len(items) > 0 {
		go func() {
			for _, it := range items {
				s.uploadBackupToRemote(it.addon, it.b, it.path, it.b.Checksum)
			}
		}()
	}
	writeJSON(w, 202, map[string]any{"queued": queued})
}

func (s *Server) handleTestBackupRemote(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadBackupRemote()
	if cfg.Target == "" {
		apiErr(w, 400, errors.New("configure an rclone target first"))
		return
	}
	tmp, err := os.CreateTemp("", "phleet-rclone-test-*")
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.WriteString("phleet backup remote test\n"); err != nil {
		tmp.Close()
		apiErr(w, 500, err)
		return
	}
	_ = tmp.Close()
	key := joinRemote(cfg.Target, ".phleet-test-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := runRclone(ctx, cfg, "copyto", name, key)
	if err != nil {
		apiErr(w, 502, fmt.Errorf("rclone copyto: %w: %s", err, truncateOutput(out, 2000)))
		return
	}
	_, _ = runRclone(ctx, cfg, "deletefile", key)
	writeJSON(w, 200, map[string]any{"ok": true, "output": truncateOutput(out, 2000)})
}

func runRclone(ctx context.Context, cfg model.BackupRemoteConfig, args ...string) (string, error) {
	path := cfg.RclonePath
	if path == "" {
		path = "rclone"
	}
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 8192 {
		out = out[len(out)-8192:]
	}
	return string(out), err
}

func (s *Server) uploadBackupToRemote(a model.Addon, b model.Backup, localPath, checksum string) {
	cfg := s.loadBackupRemote()
	if !cfg.Enabled || cfg.Target == "" {
		return
	}
	remoteKey := joinRemote(cfg.Target, a.ID, b.ID+engines.BackupExt(a.Engine))
	_ = s.st.SetBackupRemote(b.ID, "uploading", remoteKey, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if out, err := runRclone(ctx, cfg, "copyto", localPath, remoteKey); err != nil {
		msg := fmt.Sprintf("rclone copyto: %v: %s", err, truncateOutput(out, 2000))
		_ = s.st.SetBackupRemote(b.ID, "failed", remoteKey, msg)
		_ = s.st.AddEvent("system", "backup.remote_failed", a.ID, msg)
		s.log.Warn("backup remote upload", "addon", a.Slug, "backup", b.ID, "err", err)
		return
	}
	meta := map[string]any{
		"addonId": a.ID, "slug": a.Slug, "engine": a.Engine, "backupId": b.ID,
		"size": b.Size, "checksum": checksum, "encrypted": true, "createdAt": b.CreatedAt,
	}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	tmp, err := os.CreateTemp(s.cfg.DataDir, "backup-meta-*")
	if err == nil {
		name := tmp.Name()
		_, _ = tmp.Write(raw)
		_ = tmp.Close()
		_, metaErr := runRclone(ctx, cfg, "copyto", name, remoteKey+".json")
		_ = os.Remove(name)
		if metaErr != nil {
			s.log.Warn("backup remote metadata", "addon", a.Slug, "backup", b.ID, "err", metaErr)
		}
	}
	_ = s.st.SetBackupRemote(b.ID, "uploaded", remoteKey, "")
	_ = s.st.AddEvent("system", "backup.remote", a.ID, "uploaded "+remoteKey)
}

func (s *Server) deleteBackupRemote(b model.Backup) {
	if b.RemoteKey == "" {
		return
	}
	cfg := s.loadBackupRemote()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, _ = runRclone(ctx, cfg, "deletefile", b.RemoteKey)
	_, _ = runRclone(ctx, cfg, "deletefile", b.RemoteKey+".json")
}

// ensureBackupLocal materialises a backup file from the off-site copy when the
// local one is missing (a fresh control plane, or local cleanup).
func (s *Server) ensureBackupLocal(ctx context.Context, a model.Addon, b model.Backup) (string, error) {
	if b.Path != "" {
		if st, err := os.Stat(b.Path); err == nil && st.Size() > 0 {
			return b.Path, nil
		}
	}
	if b.RemoteKey == "" {
		return "", errors.New("backup file is missing locally and has no off-site copy")
	}
	cfg := s.loadBackupRemote()
	local := s.backupPath(a, b.ID)
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return "", err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if out, err := runRclone(fetchCtx, cfg, "copyto", b.RemoteKey, local); err != nil {
		return "", fmt.Errorf("fetch off-site backup: %w: %s", err, truncateOutput(out, 2000))
	}
	if err := s.st.SetBackupPath(b.ID, local); err != nil {
		s.log.Warn("record fetched backup", "backup", b.ID, "err", err)
	}
	return local, nil
}

func (s *Server) verifyBackup(ctx context.Context, a model.Addon, b model.Backup) error {
	if b.Status != "complete" {
		return errors.New("this backup is not complete")
	}
	if _, err := s.ensureBackupLocal(ctx, a, b); err != nil {
		return err
	}
	if !s.hub.Online(a.PinnedServer) {
		return errors.New("the phone hosting this database is offline")
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	_, err = s.hub.Call(callCtx, a.PinnedServer, agentproto.TypeAddonVerify, agentproto.AddonVerifyRequest{
		AddonID: a.ID, Slug: a.Slug, BackupID: b.ID, Engine: a.Engine,
		DownloadPath: "/api/addons/" + a.ID + "/backups/" + b.ID + "/download",
		Checksum:     b.Checksum,
		Port:         a.Port, User: creds.User, Password: creds.Password, Database: creds.Database,
	})
	return err
}

func (s *Server) handleGetBackupDrill(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"config": s.loadBackupDrill()})
}

func (s *Server) handlePutBackupDrill(w http.ResponseWriter, r *http.Request) {
	var cfg model.BackupDrillConfig
	if err := readJSON(r, &cfg, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	current := s.loadBackupDrill()
	cfg.LastRun = current.LastRun
	cfg.LastStatus = current.LastStatus
	cfg.LastDetail = current.LastDetail
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = 168
	}
	if cfg.IntervalHours > 8760 {
		cfg.IntervalHours = 8760
	}
	if err := s.saveBackupDrill(cfg); err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"config": cfg})
}

func (s *Server) handleListDrills(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	list, err := s.st.ListDrills(a.ID, 50)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	latest, err := s.st.LatestDrill(a.ID)
	var latestPtr *model.RestoreDrill
	if err == nil {
		latestPtr = &latest
	}
	writeJSON(w, 200, map[string]any{"drills": list, "latest": latestPtr})
}

func (s *Server) handleRunDrill(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	backups, err := s.st.ListBackups(a.ID, 1)
	if err != nil || len(backups) == 0 || backups[0].Status != "complete" {
		apiErr(w, 409, errors.New("this addon has no complete backup to verify"))
		return
	}
	drill, ok := s.startDrill(a, backups[0], "manual")
	if !ok {
		apiErr(w, 409, errors.New("a drill is already running for this addon"))
		return
	}
	writeJSON(w, 202, map[string]any{"drill": drill})
}

func (s *Server) claimDrill(addonID string) bool {
	s.drillMu.Lock()
	defer s.drillMu.Unlock()
	if s.drillRunning[addonID] {
		return false
	}
	s.drillRunning[addonID] = true
	return true
}

func (s *Server) releaseDrill(addonID string) {
	s.drillMu.Lock()
	delete(s.drillRunning, addonID)
	s.drillMu.Unlock()
}

// startDrill creates a drill row and runs it in the background. It returns the
// row as "running"; the UI polls or refreshes the list.
func (s *Server) startDrill(a model.Addon, b model.Backup, trigger string) (model.RestoreDrill, bool) {
	if !s.claimDrill(a.ID) {
		return model.RestoreDrill{}, false
	}
	d := model.RestoreDrill{
		ID: uuid.NewString(), AddonID: a.ID, BackupID: b.ID,
		Status: "running", StartedAt: time.Now().Unix(),
	}
	if err := s.st.CreateDrill(d); err != nil {
		s.releaseDrill(a.ID)
		return model.RestoreDrill{}, false
	}
	go func() {
		defer s.releaseDrill(a.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		err := s.verifyBackup(ctx, a, b)
		status, detail := "passed", trigger+" drill"
		if err != nil {
			status = "failed"
			detail = err.Error()
		}
		_ = s.st.FinishDrill(d.ID, status, detail)
		_ = s.st.AddEvent("system", "backup.drill."+status, a.ID, detail)
		cfg := s.loadBackupDrill()
		if status == "failed" || cfg.LastStatus != "failed" {
			cfg.LastStatus = status
			cfg.LastDetail = detail
			_ = s.saveBackupDrill(cfg)
		}
	}()
	return d, true
}

func (s *Server) drillLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Minute):
	}
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runScheduledDrills()
		}
	}
}

func (s *Server) runScheduledDrills() {
	cfg := s.loadBackupDrill()
	if !cfg.Enabled {
		return
	}
	interval := time.Duration(cfg.IntervalHours) * time.Hour
	if interval <= 0 {
		interval = 7 * 24 * time.Hour
	}
	if cfg.LastRun > 0 && time.Since(time.Unix(cfg.LastRun, 0)) < interval {
		return
	}
	addons, err := s.st.ListAddons()
	if err != nil {
		return
	}
	// Mark the batch before launching drills: a fast drill may finish and write
	// its own result while the loop is still starting the others.
	prevLastRun := cfg.LastRun
	cfg.LastRun = time.Now().Unix()
	cfg.LastStatus = "started"
	cfg.LastDetail = "drills started"
	_ = s.saveBackupDrill(cfg)
	started := 0
	for _, a := range addons {
		if !s.hub.Online(a.PinnedServer) {
			continue
		}
		backups, err := s.st.ListBackups(a.ID, 1)
		if err != nil || len(backups) == 0 || backups[0].Status != "complete" {
			continue
		}
		if _, ok := s.startDrill(a, backups[0], "scheduled"); !ok {
			continue
		}
		started++
	}
	if started == 0 {
		cfg = s.loadBackupDrill()
		cfg.LastRun = prevLastRun
		cfg.LastStatus = "skipped"
		cfg.LastDetail = "no online addon with a complete backup; will retry"
		_ = s.saveBackupDrill(cfg)
		return
	}
	cfg = s.loadBackupDrill()
	cfg.LastDetail = fmt.Sprintf("%d drill(s) started", started)
	_ = s.saveBackupDrill(cfg)
}

func truncateOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
