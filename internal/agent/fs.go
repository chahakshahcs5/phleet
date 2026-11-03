//go:build linux

package agent

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"phleet/internal/agentproto"
)

const maxListEntries = 2000

// resolve maps an absolute or home-relative path inside the allowed roots,
// following symlinks for the part of the path that exists.
func (a *Agent) resolve(p string) (string, error) {
	if p == "" || p == "." {
		p = a.home
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(a.home, p))
	}
	resolved, err := evalExisting(abs)
	if err != nil {
		return "", err
	}
	for _, root := range a.roots {
		if resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path %q is outside the allowed roots", p)
}

func evalExisting(p string) (string, error) {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p, nil
	}
	rp, err := evalExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(p)), nil
}

func (a *Agent) list(req agentproto.FSListRequest) (agentproto.FSListResponse, error) {
	abs, err := a.resolve(req.Path)
	if err != nil {
		return agentproto.FSListResponse{}, err
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return agentproto.FSListResponse{}, err
	}
	out := agentproto.FSListResponse{
		Path:    abs,
		Parent:  parentWithin(abs, a.roots),
		Roots:   a.roots,
		Entries: make([]agentproto.FSEntry, 0, len(ents)),
	}
	for _, e := range ents {
		if len(out.Entries) >= maxListEntries {
			break
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, agentproto.FSEntry{
			Name:    e.Name(),
			Path:    filepath.Join(abs, e.Name()),
			Dir:     info.IsDir(),
			Symlink: info.Mode()&os.ModeSymlink != 0,
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().Unix(),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Dir != out.Entries[j].Dir {
			return out.Entries[i].Dir
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

func parentWithin(abs string, roots []string) string {
	p := filepath.Dir(abs)
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return p
		}
	}
	return abs
}

func (a *Agent) readFile(req agentproto.FSReadRequest) (agentproto.FSReadResponse, error) {
	abs, err := a.resolve(req.Path)
	if err != nil {
		return agentproto.FSReadResponse{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return agentproto.FSReadResponse{}, err
	}
	if info.IsDir() {
		return agentproto.FSReadResponse{}, fmt.Errorf("%s is a directory", abs)
	}
	max := req.MaxBytes
	if max <= 0 || max > 128<<20 {
		max = 128 << 20
	}
	f, err := os.Open(abs)
	if err != nil {
		return agentproto.FSReadResponse{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return agentproto.FSReadResponse{}, err
	}
	res := agentproto.FSReadResponse{
		Path:    abs,
		Data:    data,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().Unix(),
	}
	res.Truncated = info.Size() > max
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	res.Binary = bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(head)
	return res, nil
}

func (a *Agent) writeFile(req agentproto.FSWriteRequest) error {
	abs, err := a.resolve(req.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if req.Mode != 0 {
		mode = os.FileMode(req.Mode).Perm()
	}
	return os.WriteFile(abs, req.Data, mode)
}

func (a *Agent) mkdir(req agentproto.FSMkdirRequest) error {
	abs, err := a.resolve(req.Path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o755)
	if req.Mode != 0 {
		mode = os.FileMode(req.Mode).Perm()
	}
	return os.MkdirAll(abs, mode)
}

func (a *Agent) rename(req agentproto.FSRenameRequest) error {
	from, err := a.resolve(req.From)
	if err != nil {
		return err
	}
	to, err := a.resolve(req.To)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

func (a *Agent) remove(req agentproto.FSDeleteRequest) error {
	abs, err := a.resolve(req.Path)
	if err != nil {
		return err
	}
	for _, root := range a.roots {
		if abs == root {
			return fmt.Errorf("refusing to delete a root directory")
		}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if req.Recursive {
			return os.RemoveAll(abs)
		}
		return os.Remove(abs)
	}
	return os.Remove(abs)
}

func (a *Agent) stat(p string) (agentproto.FSStatResponse, error) {
	abs, err := a.resolve(p)
	if err != nil {
		return agentproto.FSStatResponse{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return agentproto.FSStatResponse{}, err
	}
	return agentproto.FSStatResponse{
		Path:    abs,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().Unix(),
		Dir:     info.IsDir(),
	}, nil
}

// writeChunk appends bytes at a given offset. The control plane splits large
// uploads into sub-2 MB chunks so they stay under the WebSocket read limit.
func (a *Agent) writeChunk(req agentproto.FSWriteChunkRequest) error {
	abs, err := a.resolve(req.Path)
	if err != nil {
		return err
	}
	if req.Offset < 0 {
		return fmt.Errorf("invalid offset")
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if req.Offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(abs, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteAt(req.Data, req.Offset)
	return err
}

// extractZip safely unpacks an archive inside the allowed roots. It rejects
// path traversal, skips symlinks and enforces an uncompressed size budget.
func (a *Agent) extractZip(req agentproto.FSExtractRequest) (agentproto.FSExtractResponse, error) {
	res := agentproto.FSExtractResponse{}
	src, err := a.resolve(req.Path)
	if err != nil {
		return res, err
	}
	target, err := a.resolve(req.Target)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return res, err
	}
	res.Target = target
	max := req.MaxBytes
	if max <= 0 || max > 512<<20 {
		max = 512 << 20
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return res, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		// Windows-created zips often use backslashes; normalize before the
		// traversal checks so they extract as directories on Android.
		raw := strings.ReplaceAll(f.Name, "\\", "/")
		name := filepath.Clean(strings.TrimPrefix(raw, "/"))
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return res, fmt.Errorf("archive contains an unsafe path: %s", f.Name)
		}
		dest := filepath.Join(target, name)
		rel, err := filepath.Rel(target, dest)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return res, fmt.Errorf("archive entry escapes the target: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return res, err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if f.UncompressedSize64 > uint64(max) || res.Bytes+int64(f.UncompressedSize64) > max {
			return res, fmt.Errorf("archive expands beyond %d MB", max>>20)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return res, err
		}
		rc, err := f.Open()
		if err != nil {
			return res, err
		}
		out, err := os.Create(dest)
		if err != nil {
			rc.Close()
			return res, err
		}
		n, copyErr := io.Copy(out, io.LimitReader(rc, max-res.Bytes))
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return res, copyErr
		}
		if closeErr != nil {
			return res, closeErr
		}
		res.Files++
		res.Bytes += n
	}
	return res, nil
}
