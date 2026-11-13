package view

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/status"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// A summary-only workspace hands an agent a whitelist. Anything not named here is
// withheld, so a new attribute is private until someone decides otherwise; a
// blacklist fails the other way, by leaking whatever was forgotten.
var summaryAttrs = map[string][]string{
	model.KindDevice:     {"device_type", "availability", "os", "provider", "duties"},
	model.KindService:    {"service_kind", "environment"},
	model.KindProject:    {"stage", "priority", "role", "goal", "next_step", "blockers", "due"},
	model.KindRepository: {"provider", "full_name", "visibility", "default_branch", "language", "pushed_at", "fork", "open_prs", "open_issues"},
	model.KindCredential: {"provider", "usage"},
}

var summaryValues = []string{"pct", "free_gb", "total_gb", "days_left", "status", "latency_ms", "dirty", "ahead", "behind", "stash", "charging", "fail_streak"}

// foreignValues are the signal fields that identify the device or path behind a
// reading.
var foreignValues = []string{"device", "path", "user_email", "remote", "vantage", "hostname"}

// limited reports whether a viewer receives only the summary of a workspace. A
// person who can read two companies is not the same as a cloud model being allowed
// to, so this applies to agents alone.
func (s *Service) limited(ctx context.Context, v model.Viewer, workspaceID string) bool {
	if v.Kind != "agent" {
		return false
	}
	policy, err := s.Store.CloudPolicy(ctx, workspaceID)
	return err != nil || policy != "allow"
}

// redact prepares a card for a viewer.
func (s *Service) redact(ctx context.Context, v model.Viewer, c Card) Card {
	// Contributors are signal names, and signal names carry targets and device
	// slugs. They are for people who can already see all of it.
	if !v.All {
		c.Health.Contributors = []string{}
	}
	if !s.limited(ctx, v, c.WorkspaceID) {
		return c
	}
	keep := summaryAttrs[c.Kind]
	attrs := make(map[string]any, len(keep))
	for _, k := range keep {
		if val, ok := c.Attrs[k]; ok {
			attrs[k] = val
		}
	}
	c.Attrs = attrs
	c.Health.Reason = genericReason(c.Health.State)
	if c.Service != nil {
		brief := *c.Service
		brief.URL = ""
		c.Service = &brief
	}
	return c
}

func deviceIndex(snap *status.Snapshot) map[string]*model.Entity {
	out := map[string]*model.Entity{}
	for _, e := range snap.Entities {
		if e.Kind == model.KindDevice {
			out[e.Slug] = e
		}
	}
	return out
}

// behind finds the device a signal was read from.
func behind(g model.Signal, devices map[string]*model.Entity) *model.Entity {
	if slug, ok := g.Value["device"].(string); ok {
		if d := devices[slug]; d != nil {
			return d
		}
	}
	if _, slug, ok := strings.Cut(g.Source, ":"); ok {
		return devices[slug]
	}
	return nil
}

// projectSignals prepares signals for a viewer. A reading taken on a device the
// viewer cannot see keeps its result and loses its origin. Signals are copied,
// never edited in place: the snapshot behind them is shared.
func (s *Service) projectSignals(ctx context.Context, v model.Viewer, workspaceID string, snap *status.Snapshot, in []model.Signal) []model.Signal {
	out := make([]model.Signal, 0, len(in))
	devices := deviceIndex(snap)
	limited := s.limited(ctx, v, workspaceID)
	for _, g := range in {
		if d := behind(g, devices); d != nil && !v.CanSee(d.WorkspaceID) {
			val := make(map[string]any, len(g.Value))
			for k, x := range g.Value {
				if !slices.Contains(foreignValues, k) {
					val[k] = x
				}
			}
			g.Value = val
			g.Source = "采集器"
			g.Name = strings.Replace(g.Name, ":"+d.Slug, ":受限设备", 1)
		}
		// A host's failed units and open ports can belong to other tenants of that
		// host. The person who owns the host may see them; a cloud model may not.
		if v.Kind == "agent" {
			val := make(map[string]any, len(g.Value))
			for k, x := range g.Value {
				if k != "failed" && k != "ports" && k != "keys" {
					val[k] = x
				}
			}
			g.Value = val
		}
		if limited {
			val := make(map[string]any, len(summaryValues))
			for _, k := range summaryValues {
				if x, ok := g.Value[k]; ok {
					val[k] = x
				}
			}
			g.Value = val
			g.Source = ""
			g.Error = ""
			// Names and summaries carry unit names, mount points and probe targets.
			if i := strings.IndexByte(g.Name, ':'); i > 0 {
				g.Name = g.Name[:i]
			}
			g.Summary = genericSignal(g.State)
		}
		out = append(out, g)
	}
	return out
}

func (s *Service) redactEvidence(ctx context.Context, v model.Viewer, workspaceID string, in []model.Evidence) []model.Evidence {
	if !s.limited(ctx, v, workspaceID) {
		return in
	}
	out := make([]model.Evidence, 0, len(in))
	for _, x := range in {
		x.Environment, x.Method, x.SourceURL, x.Note = "", "", "", ""
		out = append(out, x)
	}
	return out
}

// otherSpaces matches the clause rules add for the owner of a shared host. For an
// agent another workspace does not exist, so even the count has to go.
var otherSpaces = regexp.MustCompile(`，?另有 \d+ 个其他空间的服务`)

func (s *Service) redactFindings(ctx context.Context, v model.Viewer, in []model.Finding) []model.Finding {
	out := make([]model.Finding, 0, len(in))
	for _, f := range in {
		if v.Kind == "agent" {
			f.Detail = otherSpaces.ReplaceAllString(f.Detail, "")
		}
		if s.limited(ctx, v, f.WorkspaceID) {
			// The detail and the action quote units, paths, dates and addresses; in a
			// summary workspace an agent gets what the rule means instead.
			f.Evidence = map[string]any{}
			f.Detail, f.Action = genericFinding(f.Rule), ""
		}
		out = append(out, f)
	}
	return out
}

// Findings lists findings as this viewer may see them.
func (s *Service) Findings(ctx context.Context, v model.Viewer, f store.FindingFilter) ([]model.Finding, error) {
	list, err := s.Store.ListFindings(ctx, v, f)
	if err != nil {
		return nil, err
	}
	return s.redactFindings(ctx, v, list), nil
}
