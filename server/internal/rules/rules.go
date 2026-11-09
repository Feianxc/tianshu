// Package rules decides what deserves a person's attention. Each rule is a plain
// function over a Snapshot and produces findings keyed by rule and entity, so a
// lasting condition stays one row and disappears by itself once it clears.
//
// The set absorbs two ideas from developer portals: operational alarms (a disk
// is filling, a site is down) and scorecards (a production service has no probe,
// a credential has no rotation date). Scorecard rules are informational and never
// feed a completion percentage.
//
// A finding is shown to anyone who can read its entity, so its text must be safe
// for all of them: it only ever names objects in the same workspace. Whatever sits
// in another workspace appears as a count.
package rules

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/status"
)

// Inputs are the facts a rule may need beyond the snapshot.
type Inputs struct {
	Snapshot  *status.Snapshot
	HasCheck  map[string]bool
	DiskTrend func(entityID string) (perDay float64, span time.Duration, ok bool)
	holders   map[string][]*model.Entity // key fingerprint -> devices it opens
}

// Evaluate runs every rule.
func Evaluate(in Inputs) []model.Finding {
	out := []model.Finding{}
	s := in.Snapshot
	in.holders = keyHolders(s)
	for _, e := range s.Entities {
		if e.Lifecycle == model.LifeRetired {
			continue
		}
		switch e.Kind {
		case model.KindDevice:
			out = append(out, deviceRules(in, e)...)
		case model.KindService:
			out = append(out, serviceRules(in, e)...)
		case model.KindProject:
			out = append(out, projectRules(in, e)...)
		case model.KindRepository:
			out = append(out, repoRules(in, e)...)
		case model.KindCredential:
			out = append(out, credentialRules(in, e)...)
		}
	}
	return out
}

func finding(rule string, e *model.Entity, sev, title, detail, action string, ev map[string]any) model.Finding {
	if ev == nil {
		ev = map[string]any{}
	}
	return model.Finding{Rule: rule, EntityID: e.ID, EntityName: e.Name, EntityKind: e.Kind, WorkspaceID: e.WorkspaceID,
		Severity: sev, Title: title, Detail: detail, Action: action, Evidence: ev}
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func str(e *model.Entity, key string) string {
	v, _ := e.Attrs[key].(string)
	return v
}

// impacted names the services riding on a device, so an alarm says what is at
// stake rather than only what broke. Services of other workspaces are counted.
func impacted(s *status.Snapshot, device *model.Entity) string {
	var names []string
	others := 0
	for _, svc := range s.Dependents(device.ID, model.RelRunsOn) {
		if svc.Lifecycle != model.LifeActive {
			continue
		}
		if svc.WorkspaceID == device.WorkspaceID {
			names = append(names, svc.Name)
		} else {
			others++
		}
	}
	var b strings.Builder
	if len(names) > 0 {
		shown := names
		if len(shown) > 4 {
			shown = shown[:4]
		}
		fmt.Fprintf(&b, "影响 %d 个运行中服务：%s", len(names), strings.Join(shown, "、"))
	}
	if others > 0 {
		if b.Len() > 0 {
			b.WriteString("，")
		}
		fmt.Fprintf(&b, "另有 %d 个其他空间的服务", others)
	}
	return b.String()
}

// answering says whether something on the device still answers an outside probe,
// and names it when it shares the workspace of the device.
func answering(s *status.Snapshot, device *model.Entity) (name string, found bool) {
	for _, svc := range s.Dependents(device.ID, model.RelRunsOn) {
		for _, g := range s.Signals[svc.ID] {
			if strings.HasPrefix(g.Name, "check.") && !g.Stale && g.State == model.StateOK {
				if svc.WorkspaceID == device.WorkspaceID {
					return svc.Name, true
				}
				found = true
			}
		}
	}
	return "", found
}

func deviceRules(in Inputs, e *model.Entity) []model.Finding {
	s := in.Snapshot
	out := []model.Finding{}
	if e.Lifecycle != model.LifeActive {
		return out
	}
	avail := str(e, "availability")
	hb := s.Signal(e.ID, "host.heartbeat")
	if hb != nil && hb.Stale && (avail == "" || avail == "always_on") {
		ago := status.Ago(s.Now.Sub(hb.ReceivedAt))
		ev := map[string]any{"last_seen": hb.ReceivedAt}
		// A collector going quiet is not a host going down. If something on this
		// host still answers an outside probe, the feed broke, not the machine.
		if witness, ok := answering(s, e); ok {
			if witness == "" {
				witness = "这台主机上的服务"
			}
			out = append(out, finding("host.feed_lost", e, model.SevWarning, e.Name+" 采集中断 "+ago,
				witness+" 的外部探测仍然正常，主机多半在线，是采集通道断了。指标已经过期。", "检查采集器或 SSH 通道", ev))
			return out
		}
		out = append(out, finding("host.lost", e, model.SevCritical, e.Name+" 失联 "+ago,
			"采集已停止，也没有外部探测能证明它还在线。"+impacted(s, e), "先用独立通道确认主机是否在线", ev))
		return out // stale host signals say nothing more
	}
	var disk *model.Finding
	worstPct := -1.0
	for _, g := range s.Signals[e.ID] {
		if g.Stale {
			continue
		}
		switch {
		case strings.HasPrefix(g.Name, "host.disk:"):
			// One finding per host, so it has to be the worst mount. Taking the
			// last one seen would let a calm disk hide a full one.
			if f, ok := diskRule(in, e, g); ok {
				p, _ := num(g.Value["pct"])
				if disk == nil || rank(f.Severity) > rank(disk.Severity) || (rank(f.Severity) == rank(disk.Severity) && p > worstPct) {
					disk, worstPct = &f, p
				}
			}
		case g.Name == "host.memory" && g.State != model.StateOK && g.State != model.StateUnknown:
			out = append(out, finding("host.memory", e, model.SevWarning, e.Name+" 内存紧张", g.Summary, "查看占用最高的进程", nil))
		case g.Name == "host.systemd" && g.State == model.StateFail:
			out = append(out, finding("host.failed_units", e, model.SevWarning, e.Name+" 有失败的系统服务", g.Summary,
				"逐个确认是遗留单元还是真实故障；遗留的应清理，否则会掩盖新故障", nil))
		case g.Name == "sec.ssh":
			out = append(out, sshRules(in, e, g)...)
		}
	}
	if disk != nil {
		out = append(out, *disk)
	}
	return out
}

func keyHolders(s *status.Snapshot) map[string][]*model.Entity {
	out := map[string][]*model.Entity{}
	for id, sigs := range s.Signals {
		e := s.Entities[id]
		if e == nil || e.Kind != model.KindDevice {
			continue
		}
		for _, g := range sigs {
			if keys, ok := g.Value["keys"].(map[string]any); ok && g.Name == "sec.ssh" && !g.Stale {
				for k := range keys {
					out[k] = append(out[k], e)
				}
			}
		}
	}
	return out
}

// A key that appeared after watching began stays on the list this long.
const newKeyWindow = 7 * 24 * time.Hour

// sshRules looks at how a host lets people in. The new key is the one to treat as
// urgent: on a root account it is what an intruder leaves behind.
func sshRules(in Inputs, e *model.Entity, g model.Signal) []model.Finding {
	s := in.Snapshot
	out := []model.Finding{}
	if g.Value["password_auth"] == "yes" {
		var d strings.Builder
		if root, _ := g.Value["root_login"].(string); root == "yes" {
			d.WriteString("root 也能用密码登录。")
		}
		ev := map[string]any{"root_login": g.Value["root_login"], "fail2ban": g.Value["fail2ban"]}
		if n, ok := num(g.Value["failed_last_hour"]); ok && n > 0 {
			fmt.Fprintf(&d, "过去 1 小时有 %.0f 次失败登录。", n)
			ev["failed_last_hour"] = n
		}
		if g.Value["fail2ban"] != "active" {
			d.WriteString("fail2ban 没有运行。")
		}
		out = append(out, finding("host.ssh_password", e, model.SevWarning, e.Name+" 允许密码登录", d.String(),
			"确认钥匙可用、控制台能进之后改成只用密钥", ev))
	}
	keys, _ := g.Value["keys"].(map[string]any)
	var fresh int
	var since time.Time
	var cross int
	var same []string
	seen := map[string]bool{e.ID: true}
	for k, t := range keys {
		if ms, ok := num(t); ok && ms > 0 {
			at := time.UnixMilli(int64(ms))
			if s.Now.Sub(at) < newKeyWindow {
				fresh++
				if since.IsZero() || at.Before(since) {
					since = at
				}
			}
		}
		for _, h := range in.holders[k] {
			if seen[h.ID] {
				continue
			}
			seen[h.ID] = true
			if h.WorkspaceID == e.WorkspaceID {
				same = append(same, h.Name)
			} else {
				cross++
			}
		}
	}
	if fresh > 0 {
		out = append(out, finding("host.ssh_new_key", e, model.SevCritical, e.Name+" 新增了登录钥匙",
			fmt.Sprintf("%d 把新钥匙可以登录这台主机，%s前出现。", fresh, status.Ago(s.Now.Sub(since))),
			"如果不是你加的，按入侵处理：断开可疑会话，移除钥匙，再查它做过什么", map[string]any{"new_keys": fresh}))
	}
	if cross > 0 {
		sort.Strings(same)
		detail := fmt.Sprintf("能登录这台主机的钥匙还能登录另外 %d 台其他空间的主机", cross)
		if len(same) > 0 {
			detail += "和 " + strings.Join(same, "、")
		}
		out = append(out, finding("host.ssh_shared_key", e, model.SevWarning, e.Name+" 的钥匙跨空间共用",
			detail+"。一把钥匙泄露，这些主机一起暴露。", "给每个空间换独立的钥匙，先加后删", map[string]any{"other_hosts": cross}))
	}
	return out
}

func rank(sev string) int {
	switch sev {
	case model.SevCritical:
		return 2
	case model.SevWarning:
		return 1
	}
	return 0
}

func diskRule(in Inputs, e *model.Entity, g model.Signal) (model.Finding, bool) {
	pct, ok := num(g.Value["pct"])
	if !ok {
		return model.Finding{}, false
	}
	mount := strings.TrimPrefix(g.Name, "host.disk:")
	free, _ := num(g.Value["free_gb"])
	sev := ""
	switch g.State {
	case model.StateFail:
		sev = model.SevCritical
	case model.StateWarn:
		sev = model.SevWarning
	}
	var detail strings.Builder
	fmt.Fprintf(&detail, "%s 已用 %.0f%%，剩余 %.1f GB。", mount, pct, free)
	ev := map[string]any{"pct": pct, "free_gb": free, "mount": mount}
	if in.DiskTrend != nil && (mount == "/" || strings.EqualFold(mount, `C:\`)) {
		if perDay, span, ok := in.DiskTrend(e.ID); ok && perDay > 0.05 {
			days := (100 - pct) / perDay
			fmt.Fprintf(&detail, "按近 %s的增速（每天 %.1f 个百分点）约 %.0f 天后写满。", status.Ago(span), perDay, math.Floor(days))
			ev["pct_per_day"], ev["days_left"], ev["window"] = perDay, days, span.String()
			if days < 14 {
				sev = model.SevCritical
			}
		}
	}
	if sev == "" {
		return model.Finding{}, false
	}
	detail.WriteString(impacted(in.Snapshot, e))
	return finding("host.disk", e, sev, e.Name+" 磁盘 "+strconv.Itoa(int(pct))+"%", detail.String(),
		"找出增长最快的目录并清理，写满会导致数据库和服务异常", ev), true
}

func serviceRules(in Inputs, e *model.Entity) []model.Finding {
	s := in.Snapshot
	out := []model.Finding{}
	if e.Lifecycle != model.LifeActive {
		return out
	}
	h := s.Health(e.ID)
	sigs := s.Signals[e.ID]
	if h.State == model.HealthDown {
		var hosts []string
		for _, d := range s.Related(e.ID, model.RelRunsOn) {
			if d.WorkspaceID == e.WorkspaceID {
				hosts = append(hosts, d.Name)
			}
		}
		detail := h.Reason
		if len(hosts) > 0 {
			detail += "。运行于 " + strings.Join(hosts, "、")
		}
		for _, p := range s.Related(e.ID, model.RelPartOf) {
			if p.WorkspaceID == e.WorkspaceID {
				detail += "。属于项目 " + p.Name
			}
		}
		out = append(out, finding("service.down", e, model.SevCritical, e.Name+" 故障", detail, "查看服务日志和最近发布", nil))
	}
	// A service can have several certificates; the one finding it gets must be
	// about the one that runs out first.
	soonest := math.Inf(1)
	for _, g := range sigs {
		if g.Stale || !strings.HasPrefix(g.Name, "check.tls") {
			continue
		}
		if days, ok := num(g.Value["days_left"]); ok && days < soonest {
			soonest = days
		}
	}
	sev := ""
	switch {
	case soonest < 14:
		sev = model.SevCritical
	case soonest < 30:
		sev = model.SevWarning
	}
	if sev != "" {
		out = append(out, finding("tls.expiring", e, sev, fmt.Sprintf("%s 证书 %.0f 天后到期", e.Name, soonest),
			"确认自动续期确实能成功，不能只看定时器存在", "运行一次续期模拟", map[string]any{"days_left": soonest}))
	}
	if !in.HasCheck[e.ID] && str(e, "url") != "" {
		out = append(out, finding("coverage.no_check", e, model.SevInfo, e.Name+" 没有外部探测",
			"已有访问地址但没有配置探测，故障时只能靠用户发现", "在目录中加一条 http 或 tls 探测", nil))
	}
	if len(sigs) == 0 && str(e, "unit") == "" && str(e, "url") == "" {
		out = append(out, finding("coverage.unmonitored", e, model.SevInfo, e.Name+" 未接入监控",
			"没有任何信号来源", "补充运行单元名或访问地址", nil))
	}
	return out
}

func projectRules(in Inputs, e *model.Entity) []model.Finding {
	out := []model.Finding{}
	stage := str(e, "stage")
	if stage == "archived" || stage == "paused" || stage == "idea" || e.Lifecycle != model.LifeActive {
		return out
	}
	if bl, ok := e.Attrs["blockers"].([]any); ok && len(bl) > 0 {
		out = append(out, finding("project.blocked", e, model.SevWarning, e.Name+" 被阻塞", firstBlocker(bl), "处理阻塞或调整计划", nil))
	}
	if str(e, "next_step") == "" && (stage == "building" || stage == "verifying" || stage == "planned") {
		out = append(out, finding("project.no_next_step", e, model.SevInfo, e.Name+" 没有下一步",
			"进行中的项目没有写明下一步，下次回来时要重新回忆", "写一句下一步要做什么", nil))
	}
	if due := str(e, "due"); due != "" {
		if t, err := time.Parse("2006-01-02", due); err == nil && in.Snapshot.Now.After(t.Add(24*time.Hour)) {
			out = append(out, finding("project.overdue", e, model.SevWarning, e.Name+" 已逾期",
				"计划日期 "+due, "调整日期或推进", nil))
		}
	}
	return out
}

func firstBlocker(bl []any) string {
	switch b := bl[0].(type) {
	case string:
		return b
	case map[string]any:
		if t, ok := b["text"].(string); ok {
			return t
		}
	}
	return "存在未解决的阻塞"
}

// holder names the device holding a working copy, when the reader of this
// repository may know about it.
func holder(s *status.Snapshot, e *model.Entity, g model.Signal) string {
	slug, _ := g.Value["device"].(string)
	for _, d := range s.Entities {
		if d.Kind == model.KindDevice && d.Slug == slug && d.WorkspaceID == e.WorkspaceID {
			return d.Name
		}
	}
	return "某台设备"
}

func repoRules(in Inputs, e *model.Entity) []model.Finding {
	s := in.Snapshot
	out := []model.Finding{}
	if ci := s.Signal(e.ID, "repo.ci"); ci != nil && !ci.Stale && ci.State == model.StateFail {
		out = append(out, finding("repo.ci_failed", e, model.SevWarning, e.Name+" 默认分支构建失败", ci.Summary, "打开最近一次运行查看原因", nil))
	}
	for _, g := range s.Signals[e.ID] {
		if g.Stale || !strings.HasPrefix(g.Name, "repo.local:") {
			continue
		}
		dirty, _ := num(g.Value["dirty"])
		ahead, _ := num(g.Value["ahead"])
		ev := map[string]any{"dirty": dirty, "ahead": ahead}
		if ahead > 0 {
			out = append(out, finding("repo.unpushed", e, model.SevWarning, fmt.Sprintf("%s 有 %.0f 个提交没推送", e.Name, ahead),
				fmt.Sprintf("工作只存在于%s，设备丢失就没了。另有 %.0f 处未提交。", holder(s, e, g), dirty), "推送到远端", ev))
		} else if dirty > 0 {
			out = append(out, finding("repo.dirty", e, model.SevInfo, fmt.Sprintf("%s 有 %.0f 处未提交改动", e.Name, dirty),
				"改动在"+holder(s, e, g)+"上", "提交或清理", ev))
		}
	}
	return out
}

func credentialRules(in Inputs, e *model.Entity) []model.Finding {
	s := in.Snapshot
	out := []model.Finding{}
	if due := str(e, "rotation_due"); due != "" {
		if t, err := time.Parse("2006-01-02", due); err == nil && s.Now.After(t) {
			out = append(out, finding("credential.rotation_due", e, model.SevWarning, e.Name+" 已到轮换日期", "计划 "+due+" 轮换", "轮换后更新日期", nil))
		}
	}
	// One key that opens several workspaces is a real blast-radius problem:
	// losing it crosses the company boundary the catalog is built around.
	spaces := map[string]bool{}
	for _, d := range s.Dependents(e.ID, model.RelUses) {
		spaces[d.WorkspaceID] = true
	}
	if len(spaces) > 1 {
		out = append(out, finding("credential.cross_workspace", e, model.SevWarning, e.Name+" 跨空间共用",
			fmt.Sprintf("同一凭据被 %d 个空间的资源使用，一处泄露会同时影响全部", len(spaces)), "拆成每个空间独立的凭据", nil))
	}
	if str(e, "storage") == "plaintext_file" {
		out = append(out, finding("credential.plaintext", e, model.SevWarning, e.Name+" 以明文文件保存",
			"没有加密保护，任何能读到这个目录的程序都能读到内容", "迁入密匣，并轮换已经以明文保存过的密码", nil))
	}
	return out
}
