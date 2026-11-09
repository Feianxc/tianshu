// Package status turns raw signals into the health a person reads. Its rules are
// the product: a paused site is not an outage, a sleeping laptop is not a failed
// server, and a number that was not measured is not zero. Every result names the
// rule version and the signals that produced it.
package status

import (
	"fmt"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// RuleVersion is stamped on every rollup so a later change of rules can be told
// apart from a change of state.
const RuleVersion = 1

// Health is one rollup.
type Health struct {
	State        string     `json:"state"`
	Freshness    string     `json:"freshness"`
	Reason       string     `json:"reason"`
	CheckedAt    *time.Time `json:"checked_at"`
	Contributors []string   `json:"contributors"`
	RuleVersion  int        `json:"rule_version"`
}

// FreshPartial means some signals are current and some are not. It is what keeps
// a half-visible system from being described as healthy.
const FreshPartial = "partial"

// Snapshot is a consistent view of the catalog for evaluation.
type Snapshot struct {
	Now      time.Time
	Entities map[string]*model.Entity
	Signals  map[string][]model.Signal
	Out      map[string][]model.Relation
	In       map[string][]model.Relation
}

// NewSnapshot indexes catalog data.
func NewSnapshot(now time.Time, ents []model.Entity, sigs map[string][]model.Signal, rels []model.Relation) *Snapshot {
	s := &Snapshot{Now: now, Entities: make(map[string]*model.Entity, len(ents)), Signals: sigs,
		Out: map[string][]model.Relation{}, In: map[string][]model.Relation{}}
	for i := range ents {
		s.Entities[ents[i].ID] = &ents[i]
	}
	for _, r := range rels {
		s.Out[r.FromID] = append(s.Out[r.FromID], r)
		s.In[r.ToID] = append(s.In[r.ToID], r)
	}
	return s
}

// Related returns entities this one points at through a relation type.
func (s *Snapshot) Related(id, typ string) []*model.Entity {
	out := []*model.Entity{}
	for _, r := range s.Out[id] {
		if r.Type == typ {
			if e := s.Entities[r.ToID]; e != nil {
				out = append(out, e)
			}
		}
	}
	return out
}

// Dependents returns entities that point at this one through a relation type.
func (s *Snapshot) Dependents(id, typ string) []*model.Entity {
	out := []*model.Entity{}
	for _, r := range s.In[id] {
		if r.Type == typ {
			if e := s.Entities[r.FromID]; e != nil {
				out = append(out, e)
			}
		}
	}
	return out
}

// Signal returns one signal of an entity.
func (s *Snapshot) Signal(id, name string) *model.Signal {
	for i := range s.Signals[id] {
		if s.Signals[id][i].Name == name {
			return &s.Signals[id][i]
		}
	}
	return nil
}

// Health computes the rollup for one entity.
func (s *Snapshot) Health(id string) Health {
	e := s.Entities[id]
	if e == nil {
		return Health{State: model.HealthUnknown, Freshness: model.FreshMissing, Contributors: []string{}, RuleVersion: RuleVersion}
	}
	var h Health
	switch e.Kind {
	case model.KindDevice:
		h = s.deviceHealth(e)
	case model.KindService:
		h = s.serviceHealth(e)
	case model.KindProject:
		h = s.projectHealth(e)
	case model.KindRepository:
		h = s.repoHealth(e)
	default:
		h = Health{State: model.HealthUnknown, Freshness: model.FreshMissing, Reason: "此类对象没有运行状态"}
	}
	if h.Contributors == nil {
		h.Contributors = []string{}
	}
	h.RuleVersion = RuleVersion
	return h
}

func declared(e *model.Entity) (Health, bool) {
	switch e.Lifecycle {
	case model.LifeRetired:
		return Health{State: model.HealthRetired, Freshness: model.FreshMissing, Reason: "已退役"}, true
	case model.LifePlanned:
		return Health{State: model.HealthPlanned, Freshness: model.FreshMissing, Reason: "尚未上线"}, true
	case model.LifePaused:
		return Health{State: model.HealthPaused, Freshness: model.FreshMissing, Reason: "已暂停，停止运行属于预期"}, true
	}
	return Health{}, false
}

func newest(sigs []model.Signal) *time.Time {
	var t *time.Time
	for i := range sigs {
		if t == nil || sigs[i].ReceivedAt.After(*t) {
			r := sigs[i].ReceivedAt
			t = &r
		}
	}
	return t
}

// badMemory bounds how long an old failure keeps a thing from being called healthy.
// After that it is a stale fact like any other, so a retired check cannot pin a
// service to unknown forever.
const badMemory = 3 * 24 * time.Hour

// fold reduces signals to their worst current state and the freshness of the
// whole set. An old failure is no longer evidence of a failure and an old pass is no
// longer evidence of health, but an old failure does stop anyone calling the thing
// healthy: the last word on it was bad, so staleBad makes the answer "unknown".
func fold(now time.Time, sigs []model.Signal) (worst string, fresh string, reasons []string, contributors []string, staleBad bool) {
	worst = model.StateOK
	var nFresh, nStale int
	for _, g := range sigs {
		// Exposure is not health: a host that allows passwords is still running fine,
		// and painting it amber would hide the next real fault.
		if strings.HasPrefix(g.Name, "sec.") {
			continue
		}
		contributors = append(contributors, g.Name)
		if g.Stale {
			nStale++
			if (g.State == model.StateFail || g.State == model.StateWarn) && now.Sub(g.ReceivedAt) < badMemory {
				staleBad = true
			}
			continue
		}
		nFresh++
		switch g.State {
		case model.StateFail:
			worst = model.StateFail
			reasons = append(reasons, g.Summary)
		case model.StateWarn:
			if worst != model.StateFail {
				worst = model.StateWarn
			}
			reasons = append(reasons, g.Summary)
		case model.StateUnknown:
			if worst == model.StateOK {
				worst = model.StateUnknown
			}
		}
	}
	switch {
	case nFresh == 0 && nStale == 0:
		fresh = model.FreshMissing
	case nFresh == 0:
		fresh = model.FreshStale
	case nStale > 0:
		fresh = FreshPartial
	default:
		fresh = model.FreshFresh
	}
	return worst, fresh, reasons, contributors, staleBad
}

func availability(e *model.Entity) string {
	if a, ok := e.Attrs["availability"].(string); ok && a != "" {
		return a
	}
	return "always_on"
}

func (s *Snapshot) deviceHealth(e *model.Entity) Health {
	if h, ok := declared(e); ok {
		return h
	}
	sigs := s.Signals[e.ID]
	hb := s.Signal(e.ID, "host.heartbeat")
	avail := availability(e)
	if hb == nil {
		reason := "未接入采集"
		if avail == "manual" {
			reason = "手工登记，无自动状态"
		}
		return Health{State: model.HealthUnknown, Freshness: model.FreshMissing, Reason: reason}
	}
	checked := hb.ReceivedAt
	if hb.Stale {
		ago := Ago(s.Now.Sub(hb.ReceivedAt))
		if avail == "always_on" {
			return Health{State: model.HealthUnknown, Freshness: model.FreshStale, CheckedAt: &checked,
				Reason: "失联待确认：最后上报在 " + ago + "前", Contributors: []string{hb.Name}}
		}
		return Health{State: model.HealthOffline, Freshness: model.FreshStale, CheckedAt: &checked,
			Reason: "未上线（符合预期）：最后上报在 " + ago + "前", Contributors: []string{hb.Name}}
	}
	worst, fresh, reasons, contrib, staleBad := fold(s.Now, sigs)
	h := Health{Freshness: fresh, CheckedAt: newest(sigs), Contributors: contrib}
	switch {
	case worst == model.StateFail || worst == model.StateWarn:
		h.State = model.HealthDegraded
		h.Reason = strings.Join(reasons, "；")
	case staleBad:
		h.State = model.HealthUnknown
		h.Reason = "上次有问题的信号已过期，当前状态不可知"
	case fresh == FreshPartial:
		h.State = model.HealthOK
		h.Reason = "可见部分正常，部分信号已过期"
	default:
		h.State = model.HealthOK
		h.Reason = "运行正常"
	}
	return h
}

func (s *Snapshot) serviceHealth(e *model.Entity) Health {
	sigs := s.Signals[e.ID]
	if h, ok := declared(e); ok {
		if e.Lifecycle == model.LifePaused {
			// A paused service that still answers is worth saying out loud.
			if worst, fresh, _, _, _ := fold(s.Now, sigs); fresh != model.FreshMissing && fresh != model.FreshStale && worst == model.StateOK {
				h.Reason = "已暂停，但仍在响应"
			}
		}
		return h
	}
	if len(sigs) == 0 {
		return Health{State: model.HealthUnknown, Freshness: model.FreshMissing, Reason: "未接入监控"}
	}
	worst, fresh, reasons, contrib, staleBad := fold(s.Now, sigs)
	h := Health{Freshness: fresh, CheckedAt: newest(sigs), Contributors: contrib}
	if fresh == model.FreshStale {
		h.State = model.HealthUnknown
		h.Reason = "所有信号已过期，当前状态不可知"
		return h
	}
	switch worst {
	case model.StateFail:
		h.State = model.HealthDown
		h.Reason = strings.Join(reasons, "；")
	case model.StateWarn:
		h.State = model.HealthDegraded
		h.Reason = strings.Join(reasons, "；")
	case model.StateUnknown:
		h.State = model.HealthUnknown
		h.Reason = "探测结果不确定"
	default:
		h.State = model.HealthOK
		h.Reason = "运行正常"
		if fresh == FreshPartial {
			h.Reason = "可见部分正常，部分信号已过期"
		}
		if staleBad {
			h.State = model.HealthUnknown
			h.Reason = "上次有问题的信号已过期，当前状态不可知"
		}
	}
	return h
}

func (s *Snapshot) projectHealth(e *model.Entity) Health {
	if e.Lifecycle == model.LifeRetired {
		return Health{State: model.HealthRetired, Freshness: model.FreshMissing, Reason: "已归档"}
	}
	var down, degraded, unknown, ok, paused int
	var seen, blind, gone int // live services; those partly and those wholly out of sight
	var names []string
	var checked *time.Time
	for _, svc := range s.Dependents(e.ID, model.RelPartOf) {
		// A project speaks for its own workspace. Anything else linked to it would
		// show through the counts below to readers who cannot see it.
		if svc.Kind != model.KindService || svc.WorkspaceID != e.WorkspaceID {
			continue
		}
		h := s.Health(svc.ID)
		names = append(names, svc.Slug)
		if h.CheckedAt != nil && (checked == nil || h.CheckedAt.After(*checked)) {
			checked = h.CheckedAt
		}
		if svc.Lifecycle == model.LifeActive {
			seen++
			switch h.Freshness {
			case model.FreshFresh:
			case FreshPartial:
				blind++
			default:
				blind++
				gone++
			}
		}
		switch h.State {
		case model.HealthDown:
			down++
		case model.HealthDegraded:
			degraded++
		case model.HealthUnknown:
			unknown++
		case model.HealthOK:
			ok++
		default:
			paused++
		}
	}
	h := Health{Freshness: model.FreshFresh, CheckedAt: checked, Contributors: names}
	switch {
	case seen == 0:
		h.Freshness = model.FreshMissing
	case gone == seen:
		h.Freshness = model.FreshStale
	case blind > 0:
		h.Freshness = FreshPartial
	}
	total := down + degraded + unknown + ok + paused
	switch {
	case total == 0:
		h.State = model.HealthUnknown
		h.Freshness = model.FreshMissing
		h.Reason = "没有关联的运行服务"
	case down > 0:
		h.State = model.HealthDown
		h.Reason = fmt.Sprintf("%d 个服务故障", down)
	case degraded > 0:
		h.State = model.HealthDegraded
		h.Reason = fmt.Sprintf("%d 个服务降级", degraded)
	case unknown > 0:
		h.State = model.HealthUnknown
		h.Reason = fmt.Sprintf("%d 个服务状态未知", unknown)
	case ok == 0:
		h.State = model.HealthPaused
		h.Reason = "全部服务已暂停"
	default:
		h.State = model.HealthOK
		h.Reason = fmt.Sprintf("%d 个服务正常", ok)
	}
	return h
}

func (s *Snapshot) repoHealth(e *model.Entity) Health {
	if e.Lifecycle == model.LifeRetired {
		return Health{State: model.HealthRetired, Freshness: model.FreshMissing, Reason: "已归档"}
	}
	meta := s.Signal(e.ID, "repo.meta")
	ci := s.Signal(e.ID, "repo.ci")
	if meta == nil {
		return Health{State: model.HealthUnknown, Freshness: model.FreshMissing, Reason: "尚未同步"}
	}
	checked := meta.ReceivedAt
	h := Health{State: model.HealthOK, Freshness: model.FreshFresh, CheckedAt: &checked, Reason: "最近同步正常", Contributors: []string{meta.Name}}
	if meta.Stale {
		h.State = model.HealthUnknown
		h.Freshness = model.FreshStale
		h.Reason = "同步已过期"
		return h
	}
	if meta.State != model.StateOK {
		h.State = model.HealthDegraded
		h.Reason = meta.Summary
		return h
	}
	if ci != nil && !ci.Stale {
		h.Contributors = append(h.Contributors, ci.Name)
		if ci.State == model.StateFail {
			h.State = model.HealthDegraded
			h.Reason = ci.Summary
		} else if ci.State == model.StateOK {
			h.Reason = ci.Summary
		}
	}
	return h
}

// Ago renders a duration for people.
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	}
}
