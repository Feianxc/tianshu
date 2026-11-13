package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

const (
	minIntervalS = 10
	maxIntervalS = 86400
	maxStaleS    = 7 * 86400
	maxPastRuns  = 32
)

// NormalizeRemote reduces a git remote to host/path with no credentials, so that
// https and ssh forms of one repository compare equal and a token embedded in a
// remote URL can never reach the catalog.
func NormalizeRemote(raw string) string {
	host, path, ok := model.ParseRemote(raw)
	if !ok {
		return ""
	}
	return strings.ToLower(host + "/" + strings.TrimSuffix(strings.Trim(path, "/"), ".git"))
}

func pct(used, total *float64) *float64 {
	if used == nil || total == nil || *total <= 0 {
		return nil
	}
	v := *used / *total * 100
	return &v
}

func threshold(v, warn, fail float64) string {
	switch {
	case v >= fail:
		return model.StateFail
	case v >= warn:
		return model.StateWarn
	}
	return model.StateOK
}

// DiskState judges a filesystem on what is left as well as on the percentage. 87%
// of a 1.4 TB drive still has 177 GB to give; 87% of a 50 GB system disk does not.
func DiskState(pct, freeGB float64) string {
	switch {
	case pct >= 92 && freeGB < 30:
		return model.StateFail
	case pct >= 85 && freeGB < 60:
		return model.StateWarn
	}
	return model.StateOK
}

var (
	collectorName = regexp.MustCompile(`^[a-z0-9_-]{1,20}$`)
	runName       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	fingerprint   = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

func oneOf(v string, allowed ...string) string {
	if slices.Contains(allowed, v) {
		return v
	}
	return ""
}

// sanitizeSSH keeps only values from a closed list, so these fields cannot carry
// text of any other kind into the database.
func sanitizeSSH(x *model.ReportSSH) {
	x.PasswordAuth = oneOf(x.PasswordAuth, "yes", "no")
	x.RootLogin = oneOf(x.RootLogin, "yes", "no", "prohibit-password", "without-password", "forced-commands-only")
	x.Fail2ban = oneOf(x.Fail2ban, "active", "inactive", "failed", "activating", "unknown")
	keys := make([]string, 0, len(x.Keys))
	for _, k := range x.Keys {
		if fingerprint.MatchString(k) && len(keys) < 64 && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	x.Keys = keys
	ports := make([]int, 0, len(x.Ports))
	for _, p := range x.Ports {
		if p > 0 && p < 65536 && len(ports) < 16 {
			ports = append(ports, p)
		}
	}
	x.Ports = ports
	if x.FailedLastHour != nil && *x.FailedLastHour < 0 {
		x.FailedLastHour = nil
	}
}

// sanitize treats a report as untrusted text before any of it is stored.
func sanitize(r *model.Report) {
	if !collectorName.MatchString(r.Collector) {
		r.Collector = "unknown"
	}
	// Only equality matters, so only a hash is kept. A value shaped like a token
	// passes the format check, and this way it still cannot land in the database.
	if runName.MatchString(r.RunID) {
		h := sha256.Sum256([]byte(r.RunID))
		r.RunID = hex.EncodeToString(h[:12])
	} else {
		r.RunID = ""
	}
	// One report may not create rows without bound; real hosts stay far below these.
	if len(r.Units) > 3000 {
		r.Units = r.Units[:3000]
	}
	if len(r.Ports) > 1000 {
		r.Ports = r.Ports[:1000]
	}
	if len(r.Repos) > 300 {
		r.Repos = r.Repos[:300]
	}
	if len(r.Disks) > 64 {
		r.Disks = r.Disks[:64]
	}
	r.Version = model.Scrub(r.Version, 40)
	r.SystemdState = model.Scrub(r.SystemdState, 40)
	if len(r.Errors) > 20 {
		r.Errors = r.Errors[:20]
	}
	for i := range r.Errors {
		r.Errors[i] = model.Scrub(r.Errors[i], 200)
	}
	if h := r.Host; h != nil {
		h.Hostname, h.OS = model.Scrub(h.Hostname, 120), model.Scrub(h.OS, 120)
		h.Kernel, h.Arch = model.Scrub(h.Kernel, 120), model.Scrub(h.Arch, 40)
	}
	if m := r.Metrics; m != nil {
		m.Network = model.Scrub(m.Network, 40)
	}
	if r.SSH != nil {
		sanitizeSSH(r.SSH)
	}
	for i := range r.Disks {
		r.Disks[i].Mount = model.Scrub(r.Disks[i].Mount, 200)
	}
	for i := range r.Units {
		u := &r.Units[i]
		u.Name, u.Active, u.Sub = model.Scrub(u.Name, 200), model.Scrub(u.Active, 40), model.Scrub(u.Sub, 40)
	}
	for i := range r.Repos {
		x := &r.Repos[i]
		x.Path, x.Branch = model.Scrub(x.Path, 400), model.Scrub(x.Branch, 200)
		x.UserEmail, x.Error = model.Scrub(x.UserEmail, 200), model.Scrub(x.Error, 200)
	}
}

// admit decides whether a report is new. Ordering comes from the collector run and
// its sequence number, not from its clock: the same counter works when a clock is
// wrong, and a replayed report carries a number that has already been used. A
// report without a run id falls back to strictly increasing timestamps.
func (s *Store) admit(ctx context.Context, src string, r model.Report, obs time.Time) (bool, error) {
	var run, past string
	var seq, last int64
	err := s.w.QueryRowContext(ctx, "SELECT run_id,seq,past_runs,last_obs FROM report_cursors WHERE source=?", src).Scan(&run, &seq, &past, &last)
	first := errors.Is(err, sql.ErrNoRows)
	if err != nil && !first {
		return false, err
	}
	runs := parseStrings(past)
	switch {
	case first:
	case r.RunID == "":
		if ms(obs) <= last {
			return false, nil
		}
	case r.RunID == run:
		if r.Seq <= seq {
			return false, nil
		}
	case slices.Contains(runs, r.RunID):
		return false, nil
	default:
		if run != "" {
			runs = append(runs, run)
		}
		if len(runs) > maxPastRuns {
			runs = runs[len(runs)-maxPastRuns:]
		}
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO report_cursors(source,run_id,seq,past_runs,last_obs) VALUES(?,?,?,?,?)
		ON CONFLICT(source) DO UPDATE SET run_id=excluded.run_id,seq=excluded.seq,past_runs=excluded.past_runs,last_obs=excluded.last_obs`,
		src, r.RunID, r.Seq, toJSON(runs), ms(obs))
	return err == nil, err
}

// Ingest applies one collector report. The only time taken from a collector is how
// old the sample was when sent, measured on its own clock, so an offset cancels out.
// A report that is not new has no effect at all, including on signal cleanup.
func (s *Store) Ingest(ctx context.Context, device model.Entity, r model.Report) (map[string]int, error) {
	sanitize(&r)
	now := s.Now()
	r.Logs = sanitizeLogs(r.Logs)
	interval := min(max(r.IntervalS, minIntervalS), maxIntervalS)
	if r.IntervalS <= 0 {
		interval = 60
	}
	var obs time.Time
	skewed := false
	if r.RunID != "" {
		age := r.SentAt.Sub(r.ObservedAt)
		age = min(max(age, 0), time.Duration(interval)*time.Second)
		obs = now.Add(-age)
		if !r.SentAt.IsZero() {
			off := r.SentAt.Sub(now)
			skewed = off > 2*time.Minute || off < -2*time.Minute
		}
	} else {
		obs = r.ObservedAt.UTC()
		if obs.IsZero() || obs.After(now.Add(2*time.Minute)) {
			skewed = !obs.IsZero()
			obs = now
		}
	}
	src := r.Collector + ":" + device.Slug
	ok, err := s.admit(ctx, src, r, obs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]int{"dropped": 1}, nil
	}

	staleAfter := max(interval*3, 120)
	if v, ok := device.Attrs["stale_after_s"].(float64); ok && v > 0 {
		staleAfter = min(int64(v), maxStaleS)
	}
	put := func(name, state, summary string, value map[string]any) error {
		return s.PutSignal(ctx, model.Signal{EntityID: device.ID, Name: name, State: state, Value: value, Summary: summary,
			Source: src, ObservedAt: obs, ReceivedAt: now, StaleAfterS: staleAfter})
	}

	hb := map[string]any{"collector": r.Collector, "version": r.Version, "interval_s": interval}
	if skewed {
		hb["clock_skew"] = true
	}
	if r.Host != nil {
		hb["hostname"], hb["os"], hb["kernel"], hb["arch"] = r.Host.Hostname, r.Host.OS, r.Host.Kernel, r.Host.Arch
		hb["cpu_count"], hb["uptime_s"] = r.Host.CPUCount, r.Host.UptimeS
	}
	if err := put("host.heartbeat", model.StateOK, "在线", hb); err != nil {
		return nil, err
	}

	sample := model.Sample{TS: obs}
	if m := r.Metrics; m != nil {
		sample.CPUPct, sample.Load1, sample.NetRxBps, sample.NetTxBps, sample.BatteryPct = m.CPUPct, m.Load1, m.NetRxBps, m.NetTxBps, m.BatteryPct
		sample.MemPct, sample.SwapPct = pct(m.MemUsed, m.MemTotal), pct(m.SwapUsed, m.SwapTotal)
		if sample.CPUPct != nil {
			if err := put("host.cpu", threshold(*sample.CPUPct, 90, 101), fmt.Sprintf("CPU %.0f%%", *sample.CPUPct),
				map[string]any{"pct": *sample.CPUPct, "load1": m.Load1, "load5": m.Load5, "load15": m.Load15}); err != nil {
				return nil, err
			}
		}
		if sample.MemPct != nil {
			if err := put("host.memory", threshold(*sample.MemPct, 90, 97), fmt.Sprintf("内存 %.0f%%", *sample.MemPct),
				map[string]any{"pct": *sample.MemPct, "used": *m.MemUsed, "total": *m.MemTotal, "swap_pct": sample.SwapPct}); err != nil {
				return nil, err
			}
		}
		if m.BatteryPct != nil {
			state := threshold(100-*m.BatteryPct, 85, 95)
			if m.Charging != nil && *m.Charging {
				state = model.StateOK
			}
			if err := put("host.battery", state, fmt.Sprintf("电量 %.0f%%", *m.BatteryPct),
				map[string]any{"pct": *m.BatteryPct, "charging": m.Charging, "network": m.Network}); err != nil {
				return nil, err
			}
		}
	}
	keep := map[string]bool{}
	for _, d := range r.Disks {
		if d.Total <= 0 {
			continue
		}
		p := d.Used / d.Total * 100
		free := (d.Total - d.Used) / (1 << 30)
		name := "host.disk:" + d.Mount
		keep[name] = true
		if err := put(name, DiskState(p, free), fmt.Sprintf("%s 已用 %.0f%%，剩余 %.1f GB", d.Mount, p, free),
			map[string]any{"pct": p, "free_gb": free, "total_gb": d.Total / (1 << 30), "mount": d.Mount}); err != nil {
			return nil, err
		}
		if sample.DiskPct == nil || d.Mount == "/" || strings.EqualFold(d.Mount, `C:\`) || strings.EqualFold(d.Mount, "C:") {
			sample.DiskPct = &p
		}
	}
	if len(r.Disks) > 0 {
		if err := s.DeleteSignals(ctx, device.ID, "host.disk:", keep); err != nil {
			return nil, err
		}
	}
	if r.Metrics != nil || len(r.Disks) > 0 {
		if err := s.PutSample(ctx, device.ID, sample); err != nil {
			return nil, err
		}
	}

	if r.SystemdState != "" {
		failed := []string{}
		for _, u := range r.Units {
			if u.Active == "failed" {
				failed = append(failed, u.Name)
			}
		}
		state, summary := model.StateOK, "系统服务正常（"+r.SystemdState+"）"
		if len(failed) > 0 {
			// The names stay in the value: a shared host runs other tenants' units,
			// and this summary travels further than the signal does.
			state = model.StateFail
			summary = fmt.Sprintf("%d 个系统服务失败", len(failed))
		}
		if err := put("host.systemd", state, summary, map[string]any{"state": r.SystemdState, "failed": failed, "units": len(r.Units), "ports": r.Ports}); err != nil {
			return nil, err
		}
	}

	if x := r.SSH; x != nil && (x.PasswordAuth != "" || len(x.Keys) > 0) {
		if err := s.applySSH(ctx, device, x, now, put); err != nil {
			return nil, err
		}
	}

	stats := map[string]int{}
	if len(r.Units) > 0 {
		n, err := s.applyUnits(ctx, device, r.Units, src, obs, now, staleAfter)
		if err != nil {
			return nil, err
		}
		stats["units_mapped"] = n
	}
	if len(r.Repos) > 0 {
		n, err := s.applyRepos(ctx, device, r.Repos, src, obs, now, staleAfter)
		if err != nil {
			return nil, err
		}
		stats["repos_mapped"] = n
	}
	if n, err := s.applyLogs(ctx, device.ID, r.Logs); err != nil {
		return nil, err
	} else if n > 0 {
		stats["logs"] = n
	}
	detail := map[string]any{"device": device.Slug, "version": r.Version, "errors": r.Errors}
	if err := s.SourceOK(ctx, src, r.Collector, interval, detail); err != nil {
		return nil, err
	}
	return stats, nil
}

// applySSH records how a host lets people in. Each key keeps the time it was first
// seen, with 0 for the keys already there when watching began: those are the baseline
// and were not added by anyone just now. A report that could not read the key file
// keeps the old list, or the next successful read would make every key look new.
func (s *Store) applySSH(ctx context.Context, device model.Entity, x *model.ReportSSH, now time.Time, put func(name, state, summary string, value map[string]any) error) error {
	var raw string
	err := s.r.QueryRowContext(ctx, "SELECT value FROM signals WHERE entity_id=? AND signal='sec.ssh'", device.ID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	known := map[string]any{}
	if err == nil {
		if m, ok := parseMap(raw)["keys"].(map[string]any); ok {
			known = m
		}
	}
	keys := make(map[string]any, len(x.Keys))
	for _, k := range x.Keys {
		switch t, ok := known[k]; {
		case ok:
			keys[k] = t
		case len(known) == 0:
			keys[k] = 0
		default:
			keys[k] = now.UnixMilli()
		}
	}
	if len(x.Keys) == 0 {
		keys = known
	}
	state, summary := model.StateUnknown, "读不到 SSH 配置"
	switch x.PasswordAuth {
	case "yes":
		state, summary = model.StateWarn, "允许密码登录"
	case "no":
		state, summary = model.StateOK, "只允许密钥登录"
	}
	value := map[string]any{"password_auth": x.PasswordAuth, "root_login": x.RootLogin, "ports": x.Ports, "fail2ban": x.Fail2ban, "keys": keys}
	if x.FailedLastHour != nil {
		value["failed_last_hour"] = *x.FailedLastHour
	}
	return put("sec.ssh", state, summary, value)
}

// noiseUnit filters units every Linux host has, so the inbox only shows what a
// person actually installed.
var noiseUnit = regexp.MustCompile(`^(systemd-|dbus|getty@|serial-getty|polkit|cron|crond|rsyslog|unattended|networkd-dispatcher|multipathd|ModemManager|irqbalance|accounts-daemon|udisks2|user@|user-runtime|chrony|ntp|snapd|snap\.|packagekit|atd|qemu-guest|uuidd|cloud-|fwupd|upower|thermald|lvm2|blk-availability|rpcbind|gssproxy|ssh|sshd|apt-|dpkg|e2scrub|fstrim|logrotate|man-db|motd|ua-|update-notifier|sysstat|plymouth|kmod|ifup|apport|console|finalrd|grub|initrd|keyboard|lxd|modprobe|networking|open-iscsi|iscsid|pollinate|rc-local|secureboot|setvtrgb|ubuntu|ufw|vgauth|open-vm|dm-event|mdmonitor|nis-domainname|NetworkManager|tuned|rngd|acpid|mcelog|wpa_supplicant|firewalld|auditd|sssd|kdump|dracut|selinux|tat_agent|ctcm-agent|ctcss|raid-check|systemd)`)

// applyUnits maps host units onto services. Signals are named per device, so a
// service on two hosts keeps both results instead of the last writer winning.
func (s *Store) applyUnits(ctx context.Context, device model.Entity, units []model.ReportUnit, src string, obs, now time.Time, staleAfter int64) (int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT e.id, e.attrs FROM entities e JOIN relations r ON r.from_id=e.id
		WHERE r.type='runs_on' AND r.to_id=? AND e.kind='service'`, device.ID)
	if err != nil {
		return 0, err
	}
	prefix := "unit:" + device.Slug + ":"
	byUnit := map[string][]string{}
	declared := map[string]map[string]bool{} // service id -> signal names it should have
	note := func(id, unit string) {
		byUnit[unit] = append(byUnit[unit], id)
		if declared[id] == nil {
			declared[id] = map[string]bool{}
		}
		declared[id][prefix+unit] = true
	}
	for rows.Next() {
		var id, attrs string
		if err := rows.Scan(&id, &attrs); err != nil {
			rows.Close()
			return 0, err
		}
		if declared[id] == nil {
			declared[id] = map[string]bool{}
		}
		for _, u := range DeclaredUnits(parseMap(attrs)) {
			note(id, u)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	// A unit dropped from the catalog must stop speaking for the service, or a
	// corrected entry keeps reporting the fault it just fixed.
	for id, keep := range declared {
		if err := s.DeleteSignals(ctx, id, prefix, keep); err != nil {
			return 0, err
		}
	}
	seen := map[string]bool{}
	mapped := 0
	for _, u := range units {
		seen[u.Name] = true
		ids, ok := byUnit[u.Name]
		if !ok {
			// Oneshot units sit at active/exited forever and are not services.
			if u.Active == "active" && u.Sub == "running" && !noiseUnit.MatchString(u.Name) {
				if err := s.SeeDiscovery(ctx, "unit", device.ID, u.Name, map[string]any{"active": u.Active, "sub": u.Sub}); err != nil {
					return mapped, err
				}
			}
			continue
		}
		if err := s.closeDiscovery(ctx, "unit", device.ID, u.Name); err != nil {
			return mapped, err
		}
		state, summary := model.StateOK, u.Name+" 运行中"
		switch u.Active {
		case "active":
		case "failed":
			state, summary = model.StateFail, u.Name+" 已失败"
		case "inactive":
			state, summary = model.StateFail, u.Name+" 未运行"
		default:
			state, summary = model.StateWarn, u.Name+" 状态 "+u.Active
		}
		for _, id := range ids {
			mapped++
			if err := s.PutSignal(ctx, model.Signal{EntityID: id, Name: prefix + u.Name, State: state, Summary: summary, Source: src,
				Value: map[string]any{"active": u.Active, "sub": u.Sub, "device": device.Slug}, ObservedAt: obs, ReceivedAt: now, StaleAfterS: staleAfter}); err != nil {
				return mapped, err
			}
		}
	}
	// A unit declared in the catalog but absent from the host is a fact too.
	for name, ids := range byUnit {
		if seen[name] {
			continue
		}
		for _, id := range ids {
			if err := s.PutSignal(ctx, model.Signal{EntityID: id, Name: prefix + name, State: model.StateFail, Summary: name + " 未加载，可能已改名或停用",
				Value: map[string]any{"device": device.Slug}, Source: src, ObservedAt: obs, ReceivedAt: now, StaleAfterS: staleAfter}); err != nil {
				return mapped, err
			}
		}
	}
	return mapped, nil
}

// applyRepos maps working copies onto repositories. A device may only speak for a
// repository in its own workspace, except that devices in the personal workspace
// are the owner's own machines and hold checkouts of everything. A company's
// collector therefore cannot write onto another company's repository, however its
// remote URL is spelled.
func (s *Store) applyRepos(ctx context.Context, device model.Entity, repos []model.ReportRepo, src string, obs, now time.Time, staleAfter int64) (int, error) {
	var kind string
	if err := s.r.QueryRowContext(ctx, "SELECT kind FROM workspaces WHERE id=?", device.WorkspaceID).Scan(&kind); err != nil {
		return 0, err
	}
	rows, err := s.r.QueryContext(ctx, "SELECT id, workspace_id, attrs FROM entities WHERE kind='repository'")
	if err != nil {
		return 0, err
	}
	type target struct{ id, workspace string }
	byRemote := map[string]target{}
	for rows.Next() {
		var id, ws, attrs string
		if err := rows.Scan(&id, &ws, &attrs); err != nil {
			rows.Close()
			return 0, err
		}
		if u, ok := parseMap(attrs)["url"].(string); ok {
			byRemote[NormalizeRemote(u)] = target{id, ws}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	mapped := 0
	for _, r := range repos {
		remote := NormalizeRemote(r.Remote)
		value := map[string]any{"path": r.Path, "branch": r.Branch, "dirty": r.Dirty, "ahead": r.Ahead, "behind": r.Behind,
			"stash": r.Stash, "has_upstream": r.HasUpstream, "user_email": r.UserEmail, "device": device.Slug, "remote": remote}
		if r.LastCommitAt != nil {
			value["last_commit_at"] = r.LastCommitAt.UTC()
		}
		t, ok := byRemote[remote]
		if !ok || remote == "" || (t.workspace != device.WorkspaceID && kind != "personal") {
			if err := s.SeeDiscovery(ctx, "repo", device.ID, r.Path, value); err != nil {
				return mapped, err
			}
			continue
		}
		if err := s.closeDiscovery(ctx, "repo", device.ID, r.Path); err != nil {
			return mapped, err
		}
		state := model.StateOK
		var parts []string
		if r.Dirty > 0 {
			parts = append(parts, fmt.Sprintf("%d 处未提交", r.Dirty))
		}
		if r.Ahead > 0 {
			state = model.StateWarn
			parts = append(parts, fmt.Sprintf("%d 个提交未推送", r.Ahead))
		}
		if r.Behind > 0 {
			parts = append(parts, fmt.Sprintf("落后 %d", r.Behind))
		}
		// No device name here: a repository can be read by people who may not know
		// which machine holds this copy. The name lives in the value.
		summary := "本地副本与远端一致"
		if len(parts) > 0 {
			summary = "本地副本：" + strings.Join(parts, "，")
		}
		mapped++
		if err := s.PutSignal(ctx, model.Signal{EntityID: t.id, Name: "repo.local:" + device.Slug, State: state, Value: value, Summary: summary,
			Source: src, ObservedAt: obs, ReceivedAt: now, StaleAfterS: max(staleAfter, 6*3600)}); err != nil {
			return mapped, err
		}
	}
	return mapped, nil
}
