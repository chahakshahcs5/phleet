//go:build linux

package agent

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"phleet/internal/agentproto"
)

func (a *Agent) processes() (agentproto.ProcListResponse, error) {
	dir, err := os.ReadDir("/proc")
	if err != nil {
		return agentproto.ProcListResponse{}, err
	}
	pageSize := int64(os.Getpagesize())
	out := make([]agentproto.Proc, 0, 64)
	for _, e := range dir {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		base := "/proc/" + e.Name()
		cmdline, err := os.ReadFile(base + "/cmdline")
		if err != nil || len(cmdline) == 0 {
			continue
		}
		cmd := strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " ")
		if len(cmd) > 240 {
			cmd = cmd[:240]
		}
		var rss int64
		if b, err := os.ReadFile(base + "/statm"); err == nil {
			f := strings.Fields(string(b))
			if len(f) > 1 {
				pages, _ := strconv.ParseInt(f[1], 10, 64)
				rss = pages * pageSize
			}
		}
		user := ""
		if b, err := os.ReadFile(base + "/status"); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "Uid:") {
					f := strings.Fields(line)
					if len(f) > 1 {
						user = uidName(f[1])
					}
					break
				}
			}
		}
		out = append(out, agentproto.Proc{PID: pid, User: user, Command: cmd, RSS: rss})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RSS > out[j].RSS })
	if len(out) > 80 {
		out = out[:80]
	}
	return agentproto.ProcListResponse{Processes: out}, nil
}

func uidName(uid string) string {
	if uid == "0" {
		return "root"
	}
	if b, err := os.ReadFile("/etc/passwd"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, ":")
			if len(f) > 2 && f[2] == uid {
				return f[0]
			}
		}
	}
	return "uid:" + uid
}
