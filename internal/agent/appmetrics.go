//go:build linux

package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// procUsage is the resource use of one process group. Apps are started with
// Setpgid, so the group id is the main process id and includes any children the
// runtime spawns (node workers, shell wrappers, and so on).
type procUsage struct {
	pgrp  int
	ticks uint64
	rss   int64
}

type appCPUSample struct {
	ticks uint64
	at    time.Time
}

// clockTicks is USER_HZ on Linux, including Android. It is 100 on every
// Android/Linux target phleet supports, and the value only affects the scale of
// the CPU percentage, not scheduling.
const clockTicks = 100.0

func scanProcessGroups() map[int]procUsage {
	out := map[int]procUsage{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		u, err := readProcUsage(pid)
		if err != nil || u.pgrp <= 0 {
			continue
		}
		agg := out[u.pgrp]
		agg.pgrp = u.pgrp
		agg.ticks += u.ticks
		agg.rss += u.rss
		out[u.pgrp] = agg
	}
	return out
}

func readProcUsage(pid int) (procUsage, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procUsage{}, err
	}
	line := string(b)
	// comm may contain spaces and parentheses; the fields we need start after
	// the final ')'. Field numbering: pgrp=5, utime=14, stime=15, rss=24.
	i := strings.LastIndexByte(line, ')')
	if i < 0 || i+2 >= len(line) {
		return procUsage{}, fmt.Errorf("bad stat line for %d", pid)
	}
	f := strings.Fields(line[i+2:])
	if len(f) < 22 {
		return procUsage{}, fmt.Errorf("short stat line for %d", pid)
	}
	pgrp, err := strconv.Atoi(f[2])
	if err != nil {
		return procUsage{}, err
	}
	utime, err := strconv.ParseUint(f[11], 10, 64)
	if err != nil {
		return procUsage{}, err
	}
	stime, err := strconv.ParseUint(f[12], 10, 64)
	if err != nil {
		return procUsage{}, err
	}
	rssPages, err := strconv.ParseInt(f[21], 10, 64)
	if err != nil {
		return procUsage{}, err
	}
	if rssPages < 0 {
		rssPages = 0
	}
	return procUsage{
		pgrp:  pgrp,
		ticks: utime + stime,
		rss:   rssPages * int64(os.Getpagesize()),
	}, nil
}
