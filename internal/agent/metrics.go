//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"phleet/internal/agentproto"
)

// batteryRetry is how long the Termux:API battery helper is skipped after it
// blocks. Without the Termux:API app the helper waits on a broadcast that never
// arrives, which used to stall the whole heartbeat loop.
const batteryRetry = 10 * time.Minute

type cpuTimes struct {
	total uint64
	idle  uint64
	ok    bool
}

type netCounters struct{ rx, tx int64 }

type batteryReading struct {
	pct      int
	temp     float64
	charging bool
	at       time.Time
	ok       bool
}

func (a *Agent) collectMetrics() agentproto.Metrics {
	m := agentproto.Metrics{BatteryPct: -1, At: 0}

	// CPU
	cur := readCPU()
	a.cpuMu.Lock()
	prev := a.prevCPU
	a.prevCPU = cur
	a.cpuMu.Unlock()
	if prev.ok && cur.ok && cur.total > prev.total {
		dTotal := cur.total - prev.total
		dIdle := cur.idle - prev.idle
		m.CPUPercent = 100 * (1 - float64(dIdle)/float64(dTotal))
		if m.CPUPercent < 0 {
			m.CPUPercent = 0
		}
	}

	// Memory
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, avail int64
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			switch f[0] {
			case "MemTotal:":
				total, _ = strconv.ParseInt(f[1], 10, 64)
			case "MemAvailable:":
				avail, _ = strconv.ParseInt(f[1], 10, 64)
			}
		}
		if total > 0 {
			m.MemTotal = total * 1024
			m.MemUsed = (total - avail) * 1024
		}
	}

	// Disk
	var st syscall.Statfs_t
	if err := syscall.Statfs(a.home, &st); err == nil {
		m.DiskTotal = int64(st.Blocks) * int64(st.Bsize)
		m.DiskUsed = m.DiskTotal - int64(st.Bavail)*int64(st.Bsize)
	}

	// Load
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			m.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}

	// Uptime
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			v, _ := strconv.ParseFloat(f[0], 64)
			m.Uptime = int64(v)
		}
	}

	// Network (cumulative)
	if b, err := os.ReadFile("/proc/net/dev"); err == nil {
		var rx, tx int64
		for _, line := range strings.Split(string(b), "\n") {
			idx := strings.Index(line, ":")
			if idx < 0 {
				continue
			}
			name := strings.TrimSpace(line[:idx])
			if name == "lo" || name == "" {
				continue
			}
			f := strings.Fields(line[idx+1:])
			if len(f) < 9 {
				continue
			}
			r, _ := strconv.ParseInt(f[0], 10, 64)
			t, _ := strconv.ParseInt(f[8], 10, 64)
			rx += r
			tx += t
		}
		m.NetRx, m.NetTx = rx, tx
	}

	// Thermal
	m.TempC = readThermal()

	// Battery
	a.readBattery(&m)
	return m
}

func readCPU() cpuTimes {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return cpuTimes{}
	}
	var vals []uint64
	for _, x := range f[1:] {
		v, _ := strconv.ParseUint(x, 10, 64)
		vals = append(vals, v)
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4] // iowait counts as idle
	}
	return cpuTimes{total: total, idle: idle, ok: true}
}

// readThermal returns the hottest plausible zone. Android thermal zones do not
// agree on a unit: on a Qualcomm phone some report millidegrees (34000), some
// tenths of a degree (367) and some plain degrees (34). There are also raw,
// uncalibrated sensors such as pa_therm0 (the RF power amplifier), which
// reports a constant 99 on many Xiaomi devices. Treating that as 99 °C made
// every phone look like it was about to melt, so those zones are skipped and
// each remaining reading is normalised on its own.
func readThermal() float64 {
	dirs, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		return 0
	}
	var max float64
	for _, dir := range dirs {
		typ := ""
		if b, err := os.ReadFile(filepath.Join(dir, "type")); err == nil {
			typ = strings.ToLower(strings.TrimSpace(string(b)))
		}
		// PA thermistors report uncalibrated values on several Qualcomm/Xiaomi
		// boards (typically 99 or 100 regardless of the real temperature).
		if strings.Contains(typ, "pa_therm") || strings.Contains(typ, "pa-therm") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "temp"))
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			continue
		}
		v = normalizeTemp(v)
		if v > 0 && v < 125 && v > max {
			max = v
		}
	}
	return max
}

// normalizeTemp converts a raw thermal-zone value to degrees Celsius. The
// kernel documents millidegrees, but several Android drivers report tenths or
// whole degrees.
func normalizeTemp(v float64) float64 {
	switch {
	case v >= 1000:
		return v / 1000
	case v >= 200:
		return v / 10
	}
	return v
}

// readBattery fills the power fields. Android denies apps access to
// /sys/class/power_supply/battery, so Termux:API is the fallback. When that app
// is not installed the helper blocks inside a binder call that even SIGKILL
// cannot interrupt quickly, so it is sampled off the heartbeat path and only
// the last good reading is used.
func (a *Agent) readBattery(m *agentproto.Metrics) {
	base := "/sys/class/power_supply/battery"
	if b, err := os.ReadFile(filepath.Join(base, "capacity")); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			m.BatteryPct = v
		}
		b, err = os.ReadFile(filepath.Join(base, "temp"))
		if err == nil {
			if v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
				if v > 100 {
					v /= 10
				}
				m.BatteryTemp = v
			}
		}
		if b, err := os.ReadFile(filepath.Join(base, "status")); err == nil {
			s := strings.ToLower(strings.TrimSpace(string(b)))
			m.Charging = strings.Contains(s, "charging") || strings.Contains(s, "full")
		}
		return
	}
	tool := a.batteryTool
	if tool == "" {
		return
	}

	a.batteryMu.Lock()
	cached := a.battery
	stale := time.Since(cached.at) > 30*time.Second
	start := stale && !a.batteryBusy && time.Now().After(a.batteryOffUntil)
	if start {
		a.batteryBusy = true
	}
	a.batteryMu.Unlock()
	if start {
		go a.sampleBattery(tool)
	}
	if cached.ok {
		m.BatteryPct = cached.pct
		m.BatteryTemp = cached.temp
		m.Charging = cached.charging
	}
}

func (a *Agent) sampleBattery(tool string) {
	// This may outlive its deadline on a binder stall; the busy flag keeps
	// heartbeats from piling more calls on top.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool)
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()

	a.batteryMu.Lock()
	a.batteryBusy = false
	if err != nil {
		a.batteryOffUntil = time.Now().Add(batteryRetry)
	} else {
		a.batteryOffUntil = time.Time{}
	}
	a.batteryMu.Unlock()
	if err != nil {
		a.log.Warn("battery helper unavailable; battery metrics paused (install the Termux:API app)", "err", err, "retryIn", batteryRetry)
		return
	}
	var body struct {
		Percentage  int     `json:"percentage"`
		Temperature float64 `json:"temperature"`
		Plugged     string  `json:"plugged"`
		Status      string  `json:"status"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return
	}
	a.batteryMu.Lock()
	a.battery = batteryReading{
		pct:      body.Percentage,
		temp:     body.Temperature,
		charging: strings.EqualFold(body.Status, "CHARGING") || strings.Contains(strings.ToUpper(body.Plugged), "PLUGGED"),
		at:       time.Now(),
		ok:       true,
	}
	a.batteryMu.Unlock()
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
