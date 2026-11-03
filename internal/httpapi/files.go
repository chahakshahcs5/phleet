package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/tunnel"
)

const (
	maxEditBytes     = 8 << 20
	maxWriteBytes    = 32 << 20
	maxDownloadBytes = 96 << 20
	maxUploadBytes   = 64 << 20
	uploadChunk      = 1 << 20
)

func (s *Server) writeChunked(ctx context.Context, serverID, target string, data []byte) error {
	if len(data) == 0 {
		_, err := s.hub.Call(ctx, serverID, agentproto.TypeFSWrite, agentproto.FSWriteRequest{Path: target})
		return err
	}
	for offset := 0; offset < len(data); offset += uploadChunk {
		end := offset + uploadChunk
		if end > len(data) {
			end = len(data)
		}
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := s.hub.Call(cctx, serverID, agentproto.TypeFSWriteChunk, agentproto.FSWriteChunkRequest{
			Path: target, Data: data[offset:end], Offset: int64(offset),
		})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) fsCall(r *http.Request, typ string, req any, dst any) error {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, r.PathValue("id"), typ, req)
	if err != nil {
		return err
	}
	if dst != nil {
		return jsonUnmarshal(env.Data, dst)
	}
	return nil
}

func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	var res agentproto.FSListResponse
	if err := s.fsCall(r, agentproto.TypeFSList, agentproto.FSListRequest{Path: r.URL.Query().Get("path")}, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleFSRead(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		apiErr(w, 400, fmt.Errorf("path is required"))
		return
	}
	max := int64(maxEditBytes)
	if v := r.URL.Query().Get("max"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= maxDownloadBytes {
			max = n
		}
	}
	var res agentproto.FSReadResponse
	if err := s.fsCall(r, agentproto.TypeFSRead, agentproto.FSReadRequest{Path: p, MaxBytes: max}, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleFSWrite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Data []byte `json:"data"`
		Mode uint32 `json:"mode"`
	}
	if err := readJSON(r, &body, maxWriteBytes+64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	if body.Path == "" {
		apiErr(w, 400, fmt.Errorf("path is required"))
		return
	}
	if len(body.Data) > maxWriteBytes {
		apiErr(w, 413, fmt.Errorf("file too large for the editor, use upload"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := s.writeChunked(ctx, r.PathValue("id"), body.Path, body.Data); err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.AddEvent("ui", "file.write", r.PathValue("id"), body.Path)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleFSOp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Op        string `json:"op"`
		Path      string `json:"path"`
		To        string `json:"to"`
		Recursive bool   `json:"recursive"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	if body.Path == "" {
		apiErr(w, 400, fmt.Errorf("path is required"))
		return
	}
	var err error
	switch body.Op {
	case "mkdir":
		err = s.fsCall(r, agentproto.TypeFSMkdir, agentproto.FSMkdirRequest{Path: body.Path, Mode: 0o755}, nil)
	case "rename":
		if body.To == "" {
			apiErr(w, 400, fmt.Errorf("to is required"))
			return
		}
		err = s.fsCall(r, agentproto.TypeFSRename, agentproto.FSRenameRequest{From: body.Path, To: body.To}, nil)
	case "delete":
		err = s.fsCall(r, agentproto.TypeFSDelete, agentproto.FSDeleteRequest{Path: body.Path, Recursive: body.Recursive}, nil)
	case "stat":
		var st agentproto.FSStatResponse
		if err = s.fsCall(r, agentproto.TypeFSStat, agentproto.FSListRequest{Path: body.Path}, &st); err == nil {
			writeJSON(w, 200, st)
			return
		}
	default:
		apiErr(w, 400, fmt.Errorf("unknown op %q", body.Op))
		return
	}
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.AddEvent("ui", "fs."+body.Op, r.PathValue("id"), body.Path)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		apiErr(w, 413, fmt.Errorf("upload rejected: %w", err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		apiErr(w, 400, fmt.Errorf("missing file field"))
		return
	}
	defer file.Close()
	name := path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if name == "." || name == ".." || name == "/" || name == "" {
		apiErr(w, 400, fmt.Errorf("invalid file name"))
		return
	}
	dir := r.URL.Query().Get("path")
	target := path.Join(dir, name)
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if len(data) > maxUploadBytes {
		apiErr(w, 413, fmt.Errorf("file exceeds %d MB", maxUploadBytes>>20))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := s.writeChunked(ctx, r.PathValue("id"), target, data); err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.AddEvent("ui", "file.upload", r.PathValue("id"), target)
	writeJSON(w, 200, map[string]any{"ok": true, "path": target, "bytes": len(data)})
}

func (s *Server) handleUploadFolder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		apiErr(w, 413, fmt.Errorf("upload rejected: %w", err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		apiErr(w, 400, fmt.Errorf("missing file field"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	if len(data) > maxUploadBytes {
		apiErr(w, 413, fmt.Errorf("archive exceeds %d MB", maxUploadBytes>>20))
		return
	}
	if len(data) == 0 {
		apiErr(w, 400, fmt.Errorf("empty archive"))
		return
	}
	dir := r.URL.Query().Get("path")
	temp := fmt.Sprintf(".phleet/tmp/upload-%s.zip", uuid.NewString())
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	if err := s.writeChunked(ctx, r.PathValue("id"), temp, data); err != nil {
		apiErr(w, 502, err)
		return
	}
	env, err := s.hub.Call(ctx, r.PathValue("id"), agentproto.TypeFSExtract, agentproto.FSExtractRequest{
		Path: temp, Target: dir, MaxBytes: 512 << 20,
	})
	if err != nil {
		_, _ = s.hub.Call(ctx, r.PathValue("id"), agentproto.TypeFSDelete, agentproto.FSDeleteRequest{Path: temp})
		apiErr(w, 502, err)
		return
	}
	var res agentproto.FSExtractResponse
	_ = jsonUnmarshal(env.Data, &res)
	_, _ = s.hub.Call(ctx, r.PathValue("id"), agentproto.TypeFSDelete, agentproto.FSDeleteRequest{Path: temp})
	_ = s.st.AddEvent("ui", "folder.upload", r.PathValue("id"), dir)
	writeJSON(w, 200, map[string]any{
		"ok": true, "target": res.Target, "files": res.Files, "bytes": res.Bytes,
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		apiErr(w, 400, fmt.Errorf("path is required"))
		return
	}
	if s.streamDownload(w, r.PathValue("id"), p) {
		return
	}
	// Fallback for agents that predate "fs" streams. New agents stream any
	// size; the legacy path is capped at 96 MB and must not truncate silently.
	var res agentproto.FSReadResponse
	if err := s.fsCall(r, agentproto.TypeFSRead, agentproto.FSReadRequest{Path: p, MaxBytes: maxDownloadBytes}, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	if res.Truncated {
		apiErr(w, 413, fmt.Errorf("file is larger than the %d MB download limit; update the agent to stream larger files", maxDownloadBytes>>20))
		return
	}
	name := path.Base(res.Path)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(res.Data)))
	w.WriteHeader(200)
	_, _ = w.Write(res.Data)
}

// streamDownload returns true when it has handled the response. It returns
// false only before writing anything, so the caller can fall back to the old
// base64 protocol for older agents.
func (s *Server) streamDownload(w http.ResponseWriter, serverID, filePath string) bool {
	sess, ok := s.hub.Tunnel(serverID)
	if !ok {
		return false
	}
	stream, err := sess.OpenStream()
	if err != nil {
		return false
	}
	_ = stream.SetDeadline(time.Now().Add(5 * time.Minute))
	if err := tunnel.WriteFrame(stream, tunnel.RequestHeader{Kind: "fs", Path: filePath}); err != nil {
		_ = stream.Close()
		return false
	}
	var res tunnel.ResponseHeader
	if err := tunnel.ReadFrame(stream, &res); err != nil || res.Status != 200 {
		_ = stream.Close()
		return false
	}
	_ = stream.SetDeadline(time.Time{})
	name := path.Base(filePath)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(name)))
	if res.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", res.ContentLength))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, stream)
	_ = stream.Close()
	return true
}
