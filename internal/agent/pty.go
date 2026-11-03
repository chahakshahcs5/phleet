//go:build linux

package agent

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"phleet/internal/agentproto"
)

const maxPTYSessions = 4

type ptySession struct {
	id   string
	ptmx *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func (a *Agent) shellPath() string {
	candidates := []string{
		os.Getenv("SHELL"),
		"/data/data/com.termux/files/usr/bin/bash",
		"/data/data/com.termux/files/usr/bin/sh",
		"/bin/bash",
		"/bin/sh",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return "sh"
}

func (a *Agent) openPTY(req agentproto.PTYOpen) error {
	a.sessMu.Lock()
	if len(a.sessions) >= maxPTYSessions {
		a.sessMu.Unlock()
		return errors.New("too many terminal sessions")
	}
	a.sessMu.Unlock()

	cols, rows := req.Cols, req.Rows
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	cwd := a.home
	if req.Cwd != "" {
		if p, err := a.resolve(req.Cwd); err == nil {
			cwd = p
		}
	}
	shell := a.shellPath()
	if req.Shell != "" {
		shell = req.Shell
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=C.UTF-8",
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	s := &ptySession{id: req.Session, ptmx: ptmx, cmd: cmd}
	a.sessMu.Lock()
	a.sessions[req.Session] = s
	a.sessMu.Unlock()
	go a.pumpPTY(s)
	return nil
}

func (a *Agent) pumpPTY(s *ptySession) {
	buf := make([]byte, 32<<10)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			_ = a.send(agentproto.TypePTYData, "", agentproto.PTYData{Session: s.id, Data: chunk})
		}
		if err != nil {
			break
		}
	}
	code := 0
	if err := s.cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	a.sessMu.Lock()
	if cur, ok := a.sessions[s.id]; ok && cur == s {
		delete(a.sessions, s.id)
	}
	a.sessMu.Unlock()
	_ = a.send(agentproto.TypePTYExit, "", agentproto.PTYExit{Session: s.id, ExitCode: code})
}

func (a *Agent) writePTY(session string, data []byte) {
	a.sessMu.Lock()
	s := a.sessions[session]
	a.sessMu.Unlock()
	if s == nil {
		return
	}
	_, _ = s.ptmx.Write(data)
}

func (a *Agent) resizePTY(session string, cols, rows uint16) {
	a.sessMu.Lock()
	s := a.sessions[session]
	a.sessMu.Unlock()
	if s == nil || cols == 0 || rows == 0 {
		return
	}
	_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

func (a *Agent) closePTY(session string) {
	a.sessMu.Lock()
	s := a.sessions[session]
	delete(a.sessions, session)
	a.sessMu.Unlock()
	if s == nil {
		return
	}
	s.once.Do(func() {
		_ = s.ptmx.Close()
		if s.cmd.Process != nil {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
			_ = s.cmd.Process.Kill()
		}
	})
}

// closeAllPTYs is called when the control plane connection drops: nobody can
// watch those shells anymore, and leaving them around would leak processes.
func (a *Agent) closeAllPTYs() {
	a.sessMu.Lock()
	sessions := make([]*ptySession, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.sessions = map[string]*ptySession{}
	a.sessMu.Unlock()
	for _, s := range sessions {
		s.once.Do(func() {
			_ = s.ptmx.Close()
			if s.cmd.Process != nil {
				_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
				_ = s.cmd.Process.Kill()
			}
		})
	}
}
