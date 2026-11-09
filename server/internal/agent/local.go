package agent

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// local collects this machine. Every metric is attempted independently and a
// failure leaves that field empty, because a partial report is still true.
type local struct {
	lastRx, lastTx float64
	lastAt         time.Time
}

func newLocal() *local { return &local{} }

func f(v float64) *float64 { return &v }

var skipFS = map[string]bool{"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "proc": true,
	"sysfs": true, "cgroup": true, "cgroup2": true, "devfs": true, "autofs": true, "nsfs": true, "ramfs": true, "fuse.snapfuse": true}

// Collect takes one sample.
func (l *local) Collect(ctx context.Context) model.Report {
	r := model.Report{ObservedAt: time.Now().UTC()}
	m := &model.ReportMetric{}
	note := func(what string, err error) {
		if err != nil {
			r.Errors = append(r.Errors, what+": "+err.Error())
		}
	}

	if h, err := host.InfoWithContext(ctx); err == nil {
		r.Host = &model.ReportHost{Hostname: h.Hostname, OS: strings.TrimSpace(h.Platform + " " + h.PlatformVersion),
			Kernel: h.KernelVersion, Arch: h.KernelArch, UptimeS: int64(h.Uptime), CPUCount: runtime.NumCPU()}
	} else {
		note("host", err)
	}
	if v, err := cpu.PercentWithContext(ctx, time.Second, false); err == nil && len(v) > 0 {
		m.CPUPct = f(v[0])
	} else {
		note("cpu", err)
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemUsed, m.MemTotal = f(float64(v.Total-v.Available)), f(float64(v.Total))
	} else {
		note("memory", err)
	}
	if v, err := mem.SwapMemoryWithContext(ctx); err == nil && v.Total > 0 {
		m.SwapUsed, m.SwapTotal = f(float64(v.Used)), f(float64(v.Total))
	}
	if runtime.GOOS != "windows" {
		if v, err := load.AvgWithContext(ctx); err == nil {
			m.Load1, m.Load5, m.Load15 = f(v.Load1), f(v.Load5), f(v.Load15)
		}
	}
	if v, err := net.IOCountersWithContext(ctx, false); err == nil && len(v) > 0 {
		now := time.Now()
		rx, tx := float64(v[0].BytesRecv), float64(v[0].BytesSent)
		// The first sample has no baseline, and a counter that went backwards
		// was reset; in both cases the rate is unknown, not zero.
		if !l.lastAt.IsZero() && rx >= l.lastRx && tx >= l.lastTx {
			d := now.Sub(l.lastAt).Seconds()
			m.NetRxBps, m.NetTxBps = f((rx-l.lastRx)/d), f((tx-l.lastTx)/d)
		}
		l.lastRx, l.lastTx, l.lastAt = rx, tx, now
	}
	r.Metrics = m

	parts, err := disk.PartitionsWithContext(ctx, false)
	note("disk", err)
	seen := map[string]bool{}
	for _, p := range parts {
		if skipFS[p.Fstype] || seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true
		if u, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil && u.Total > 0 {
			r.Disks = append(r.Disks, model.ReportDisk{Mount: p.Mountpoint, Used: float64(u.Used), Total: float64(u.Total)})
		}
	}
	if runtime.GOOS == "linux" {
		collectSystemd(ctx, &r)
	}
	return r
}

func collectSystemd(ctx context.Context, r *model.Report) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(c, "systemctl", "is-system-running").Output()
	r.SystemdState = strings.TrimSpace(string(out))
	units, err := exec.CommandContext(c, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager").Output()
	if err != nil {
		r.Errors = append(r.Errors, "systemd: "+err.Error())
		return
	}
	r.Units = parseUnits(string(units))
	if ports, err := exec.CommandContext(c, "ss", "-lntH").Output(); err == nil {
		r.Ports = parsePorts(string(ports))
	}
}

// parseUnits reads `systemctl list-units --plain` lines.
func parseUnits(s string) []model.ReportUnit {
	out := []model.ReportUnit{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		if fields[1] == "not-found" {
			continue
		}
		out = append(out, model.ReportUnit{Name: fields[0], Active: fields[2], Sub: fields[3]})
	}
	return out
}

// parsePorts reads `ss -lntH` lines into unique ports.
func parsePorts(s string) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		addr := fields[3]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		if p, err := strconv.Atoi(addr[i+1:]); err == nil && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
