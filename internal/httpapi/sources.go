package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const maxSourceBytes = 64 << 20

func (s *Server) sourceBundlePath(projectID string) string {
	return filepath.Join(s.cfg.DataDir, "bundles", projectID+".zip")
}

func (s *Server) hasSourceBundle(projectID string) bool {
	st, err := os.Stat(s.sourceBundlePath(projectID))
	return err == nil && !st.IsDir() && st.Size() > 0
}

// handleUploadSource stores a zip uploaded from the browser. Agents download it
// at deploy time instead of cloning git.
func (s *Server) handleUploadSource(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if s.hasInflight(p.ID) {
		apiErr(w, 409, errors.New("a deployment is in progress; wait for it to finish"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSourceBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		apiErr(w, 413, fmt.Errorf("upload rejected: %w", err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		apiErr(w, 400, errors.New("missing file field"))
		return
	}
	defer file.Close()

	dest := s.sourceBundlePath(p.ID)
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		apiErr(w, 500, err)
		return
	}
	tmp, err := os.CreateTemp(dir, p.ID+"-*.zip")
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		_ = os.Remove(tmpName)
	}
	n, err := io.Copy(tmp, io.LimitReader(file, maxSourceBytes+1))
	if err != nil {
		cleanup()
		apiErr(w, 500, err)
		return
	}
	if n > maxSourceBytes {
		cleanup()
		apiErr(w, 413, fmt.Errorf("source archive exceeds %d MB", maxSourceBytes>>20))
		return
	}
	if n == 0 {
		cleanup()
		apiErr(w, 400, errors.New("empty archive"))
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		apiErr(w, 500, err)
		return
	}
	_ = os.Remove(dest)
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		apiErr(w, 500, err)
		return
	}
	now := time.Now().Unix()
	if err := s.st.UpdateProjectSource(p.ID, "upload", n, now); err != nil {
		apiErr(w, 500, err)
		return
	}
	p.SourceType = "upload"
	p.SourceSize = n
	p.SourceUpdatedAt = now
	_ = s.st.AddEvent("ui", "project.source_upload", p.ID, fmt.Sprintf("%d bytes", n))
	writeJSON(w, 200, map[string]any{"project": s.buildProjectView(p, true), "bytes": n})
}

// handleDownloadSource is used by agents during deploy. The agent token is
// accepted by the API middleware, so no unsigned URL is needed.
func (s *Server) handleDownloadSource(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	path := s.sourceBundlePath(p.ID)
	f, err := os.Open(path)
	if err != nil {
		apiErr(w, 404, errors.New("source bundle is missing"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=source.zip")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "source.zip", st.ModTime(), f)
}
