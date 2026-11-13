package view

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/rules"
	"github.com/Feianxc/tianshu/server/internal/status"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// LogQuery asks for the log lines behind one device or service.
type LogQuery struct {
	EntityID string
	Unit     string
	Level    string // error | warning | all
	Since    time.Duration
	Contains string
	Limit    int
}

// LogsOut is what a viewer gets. Text is withheld when the viewer may not read log
// text here; the counts per unit still say where the trouble is.
type LogsOut struct {
	Target     string          `json:"target"`
	Kind       string          `json:"kind"`
	TextShown  bool            `json:"text_shown"`
	Withheld   string          `json:"withheld,omitempty" jsonschema:"为什么没有返回日志原文"`
	Collecting bool            `json:"collecting" jsonschema:"false 表示这里还没有采集过日志，没有日志不代表没有问题"`
	Since      time.Time       `json:"since"`
	Units      []model.LogStat `json:"units"`
	Lines      []model.LogLine `json:"lines"`
}

func levelPriority(level string) int {
	switch level {
	case "all":
		return 7
	case "warning":
		return 4
	}
	return 3
}

// logScope works out which devices and units a query covers, and whether the viewer
// may read the text. A device query leaves out units of services in workspaces the
// viewer cannot see: a shared host runs other tenants' software.
func (s *Service) logScope(ctx context.Context, v model.Viewer, snap *status.Snapshot, e *model.Entity) (devices []*model.Entity, units, exclude []string, text bool, why string, err error) {
	text = v.CanReadLogs() && !s.limited(ctx, v, e.WorkspaceID)
	switch e.Kind {
	case model.KindDevice:
		devices = []*model.Entity{e}
		owners, err := s.Store.UnitOwners(ctx, e.ID)
		if err != nil {
			return nil, nil, nil, false, "", err
		}
		for u, wss := range owners {
			if !slices.ContainsFunc(wss, v.CanSee) {
				exclude = append(exclude, u)
			}
		}
	case model.KindService:
		devices = snap.Related(e.ID, model.RelRunsOn)
		units = store.DeclaredUnits(e.Attrs)
		for _, d := range devices {
			if s.limited(ctx, v, d.WorkspaceID) {
				text = false
			}
		}
	default:
		return nil, nil, nil, false, "只有设备和服务有日志", nil
	}
	if !text {
		switch {
		case !v.CanReadLogs():
			why = "当前身份没有日志权限，只返回各单元的错误和警告数量。需要原文时请主人在天枢授权时勾选“允许读取日志”。"
		default:
			why = "这个空间对云端 Agent 只提供摘要，日志原文不出天枢。只返回各单元的错误和警告数量。"
		}
	}
	return devices, units, exclude, text, why, nil
}

// Logs returns log lines for a device or service.
func (s *Service) Logs(ctx context.Context, v model.Viewer, q LogQuery) (*LogsOut, error) {
	e, err := s.Store.GetEntity(ctx, v, q.EntityID)
	if err != nil {
		return nil, err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	devices, units, exclude, text, why, err := s.logScope(ctx, v, snap, &e)
	if err != nil {
		return nil, err
	}
	since := q.Since
	if since <= 0 || since > 7*24*time.Hour {
		since = 24 * time.Hour
	}
	out := &LogsOut{Target: e.Slug, Kind: e.Kind, TextShown: text, Withheld: why, Since: s.Store.Now().Add(-since),
		Units: []model.LogStat{}, Lines: []model.LogLine{}}
	if len(devices) == 0 {
		if why == "" {
			out.Withheld = "这个服务没有登记运行在哪台主机上"
		}
		return out, nil
	}
	ids := make([]string, len(devices))
	slugOf := map[string]string{}
	for i, d := range devices {
		ids[i] = d.ID
		slugOf[d.ID] = d.Slug
		if !v.CanSee(d.WorkspaceID) {
			slugOf[d.ID] = "受限设备"
		}
	}
	if out.Collecting, err = s.Store.LogsEverSeen(ctx, ids); err != nil {
		return nil, err
	}
	// A quiet host has no lines at all; what says logs are collected is a collector
	// version that collects them.
	for _, d := range devices {
		if g := snap.Signal(d.ID, "host.heartbeat"); g != nil && !g.Stale && logCapable(g) {
			out.Collecting = true
		}
	}
	f := store.LogFilter{DeviceIDs: ids, Units: units, ExcludeUnits: exclude, MaxPriority: 7, Since: out.Since}
	if q.Unit != "" {
		if units != nil && !slices.Contains(units, q.Unit) {
			f.Units = []string{}
		} else {
			f.Units = []string{q.Unit}
		}
	}
	if out.Units, err = s.Store.LogStats(ctx, f); err != nil {
		return nil, err
	}
	if !text {
		return out, nil
	}
	f.MaxPriority, f.Contains, f.Limit = levelPriority(q.Level), q.Contains, q.Limit
	lines, err := s.Store.ListLogs(ctx, f)
	if err != nil {
		return nil, err
	}
	for i := range lines {
		lines[i].Device = slugOf[lines[i].DeviceID]
		lines[i].Level = model.LogLevel(lines[i].Priority)
	}
	out.Lines = lines
	return out, nil
}

// Diagnosis gathers what is needed to work on one object: its state, what is wrong,
// what each problem means and how to approach it, the evidence behind it, and the
// state of the hosts it depends on.
type Diagnosis struct {
	Target   string         `json:"target"`
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Health   string         `json:"health"`
	Reason   string         `json:"reason"`
	Problems []Problem      `json:"problems" jsonschema:"未解决的问题，每条带含义、处理步骤和只读排查命令"`
	Probes   []ProbeHistory `json:"probes"`
	Hosts    []HostState    `json:"hosts"`
	Logs     *LogsOut       `json:"logs"`
	Signals  []model.Signal `json:"signals"`
	Note     string         `json:"note"`
}

// Problem is a finding with its playbook.
type Problem struct {
	model.Finding
	Playbook *rules.Playbook `json:"playbook,omitempty"`
}

// ProbeHistory is the recent record of one probe.
type ProbeHistory struct {
	Kind   string           `json:"kind"`
	Target string           `json:"target"`
	Runs   []model.CheckRun `json:"runs"`
}

// HostState is a device a service runs on.
type HostState struct {
	Slug   string `json:"slug"`
	Health string `json:"health"`
	Reason string `json:"reason"`
}

// Diagnose builds a Diagnosis.
func (s *Service) Diagnose(ctx context.Context, v model.Viewer, id string) (*Diagnosis, error) {
	d, err := s.Get(ctx, v, id)
	if err != nil {
		return nil, err
	}
	out := &Diagnosis{Target: d.Slug, Name: d.Name, Kind: d.Kind, Health: d.Health.State, Reason: d.Health.Reason,
		Problems: []Problem{}, Probes: []ProbeHistory{}, Hosts: []HostState{}, Signals: []model.Signal{},
		Note: "天枢只读。排查命令需要在对应主机上由主人执行；修改配置、重启服务前请先征得主人同意。"}
	for _, f := range d.Open {
		p := Problem{Finding: f}
		if pb, ok := rules.PlaybookFor(f.Rule); ok {
			p.Playbook = &pb
		}
		out.Problems = append(out.Problems, p)
	}
	for _, g := range d.Signals {
		if g.State != model.StateOK || g.Stale {
			out.Signals = append(out.Signals, g)
		}
	}
	for _, c := range d.Checks {
		runs, err := s.Store.RecentCheckRuns(ctx, c.ID, 8)
		if err != nil {
			return nil, err
		}
		out.Probes = append(out.Probes, ProbeHistory{Kind: c.Kind, Target: c.Target, Runs: runs})
	}
	for _, r := range d.Relations {
		if r.Relation == model.RelRunsOn && r.Direction == "out" && !r.Restricted {
			hd, err := s.Get(ctx, v, r.ID)
			if err == nil {
				out.Hosts = append(out.Hosts, HostState{Slug: hd.Slug, Health: hd.Health.State, Reason: hd.Health.Reason})
			}
		}
	}
	if d.Kind == model.KindDevice || d.Kind == model.KindService {
		if out.Logs, err = s.Logs(ctx, v, LogQuery{EntityID: d.ID, Level: "warning", Since: 24 * time.Hour, Limit: 40}); err != nil {
			return nil, err
		}
	}
	if len(out.Problems) == 0 && out.Health == model.HealthOK {
		out.Note = "当前没有发现问题。" + out.Note
	} else if strings.TrimSpace(out.Reason) != "" {
		out.Note = "先看 problems 里每条的 playbook，再对照 logs 和 probes。" + out.Note
	}
	return out, nil
}

// logCapable reports whether the collector behind a heartbeat sends journal lines:
// the agent and SSH collectors from 0.2.0 on, on systems that have a journal.
func logCapable(g *model.Signal) bool {
	c, _ := g.Value["collector"].(string)
	v, _ := g.Value["version"].(string)
	osName, _ := g.Value["os"].(string)
	if strings.Contains(strings.ToLower(osName), "windows") {
		return false // no journal there
	}
	return (c == "agent" || c == "ssh") && v != "" && !strings.HasPrefix(v, "0.1.") && v != "0.1"
}
