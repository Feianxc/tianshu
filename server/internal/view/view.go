// Package view is the single read model behind the web console, the REST API and
// the MCP tools. Business meaning and workspace filtering live here once, so an
// agent can never see a different truth, or more of it, than the console shows.
package view

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/status"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// SchemaVersion is stamped on every response.
const SchemaVersion = "1"

// Service builds views.
type Service struct {
	Store *store.Store
}

// Card is an entity with what a person needs at a glance.
type Card struct {
	model.Entity
	Workspace string        `json:"workspace"`
	Health    status.Health `json:"health"`
	Findings  int           `json:"findings"`
	Worst     string        `json:"worst_severity"`
	Device    *DeviceBrief  `json:"device,omitempty"`
	Project   *ProjectBrief `json:"project,omitempty"`
	Repo      *RepoBrief    `json:"repo,omitempty"`
	Service   *ServiceBrief `json:"service,omitempty"`
}

// ServiceBrief says where a service runs. A host outside the viewer's workspaces
// appears as a restricted stub, the same as on the detail page.
type ServiceBrief struct {
	Kind        string `json:"kind"`
	Environment string `json:"environment"`
	URL         string `json:"url"`
	Hosts       []Ref  `json:"hosts"`
	Projects    []Ref  `json:"projects"`
}

// DeviceBrief is the latest host picture. Nil numbers were not measured.
type DeviceBrief struct {
	Type       string     `json:"type"`
	OS         string     `json:"os"`
	CPUPct     *float64   `json:"cpu_pct"`
	MemPct     *float64   `json:"mem_pct"`
	DiskPct    *float64   `json:"disk_pct"`
	DiskFreeGB *float64   `json:"disk_free_gb"`
	BatteryPct *float64   `json:"battery_pct"`
	UptimeS    *float64   `json:"uptime_s"`
	Services   int        `json:"services"`
	LastSeen   *time.Time `json:"last_seen"`
	Collector  string     `json:"collector"`
}

// ProjectBrief answers "where is this stuck".
type ProjectBrief struct {
	Stage        string     `json:"stage"`
	Priority     string     `json:"priority"`
	NextStep     string     `json:"next_step"`
	Blockers     int        `json:"blockers"`
	Role         string     `json:"role"`
	Services     int        `json:"services"`
	ServicesUp   int        `json:"services_up"`
	Repos        int        `json:"repos"`
	LastVerdict  string     `json:"last_verdict"`
	LastActivity *time.Time `json:"last_activity"`
	UpdateStale  bool       `json:"update_stale"`
}

// RepoBrief is the code hosting picture.
type RepoBrief struct {
	FullName   string     `json:"full_name"`
	Visibility string     `json:"visibility"`
	Language   string     `json:"language"`
	PushedAt   *time.Time `json:"pushed_at"`
	OpenIssues *float64   `json:"open_issues"`
	OpenPRs    *float64   `json:"open_prs"`
	CI         string     `json:"ci"`
	LocalDirty int        `json:"local_dirty"`
	LocalAhead int        `json:"local_ahead"`
}

// Ref is one end of a relation. Restricted hides everything but the kind and
// state of something outside the viewer's workspaces, so a tenant learns its
// service sits on a sick host without learning which host or who else is on it.
type Ref struct {
	Relation   string `json:"relation"`
	Direction  string `json:"direction"` // out | in
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Health     string `json:"health"`
	Restricted bool   `json:"restricted"`
}

// Detail is the full page of one entity.
type Detail struct {
	Card
	Relations []Ref            `json:"relations"`
	Signals   []model.Signal   `json:"signals"`
	Checks    []model.Check    `json:"checks"`
	Open      []model.Finding  `json:"open_findings"`
	Evidence  []model.Evidence `json:"evidence"`
}

// Overview is the landing page.
type Overview struct {
	SchemaVersion string          `json:"schema_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Attention     []model.Finding `json:"attention"`
	Gaps          []model.Finding `json:"gaps"`
	Snoozed       int             `json:"snoozed"`
	Matrix        []MatrixRow     `json:"matrix"`
	Wall          []WallRow       `json:"wall"`
	Devices       []Card          `json:"devices"`
	Projects      []Card          `json:"projects"`
	Expiring      []Expiring      `json:"expiring"`
	Sources       []SourceRow     `json:"sources"`
	Totals        map[string]int  `json:"totals"`
}

// Lamp is one light on the wall: one device or service and what is known about it.
type Lamp struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Freshness string `json:"freshness"`
	Reason    string `json:"reason"`
}

// WallRow is one workspace's lamps, devices first.
type WallRow struct {
	Workspace model.Workspace `json:"workspace"`
	Devices   []Lamp          `json:"devices"`
	Services  []Lamp          `json:"services"`
}

// MatrixRow is one workspace across health states.
type MatrixRow struct {
	Workspace model.Workspace `json:"workspace"`
	Devices   map[string]int  `json:"devices"`
	Services  map[string]int  `json:"services"`
	Projects  map[string]int  `json:"projects"`
	Repos     int             `json:"repos"`
}

// Expiring is one date coming up.
type Expiring struct {
	EntityID string    `json:"entity_id"`
	Name     string    `json:"name"`
	What     string    `json:"what"`
	At       time.Time `json:"at"`
	DaysLeft int       `json:"days_left"`
}

// SourceRow is a feed with its computed condition.
type SourceRow struct {
	model.Source
	Condition string `json:"condition"` // ok | stale | error | never
}

func (s *Service) snapshot(ctx context.Context) (*status.Snapshot, error) {
	ents, err := s.Store.AllEntities(ctx)
	if err != nil {
		return nil, err
	}
	sigs, err := s.Store.AllSignals(ctx)
	if err != nil {
		return nil, err
	}
	rels, err := s.Store.AllRelations(ctx)
	if err != nil {
		return nil, err
	}
	return status.NewSnapshot(s.Store.Now(), ents, sigs, rels), nil
}

// Snapshot exposes an unfiltered view for background evaluation. Its result must
// never be returned to a caller.
func (s *Service) Snapshot(ctx context.Context) (*status.Snapshot, error) { return s.snapshot(ctx) }

func numOf(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}

func strOf(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func (s *Service) card(v model.Viewer, snap *status.Snapshot, e *model.Entity, slugs map[string]string, counts map[string][2]int) Card {
	c := Card{Entity: *e, Workspace: slugs[e.WorkspaceID], Health: snap.Health(e.ID)}
	if n, ok := counts[e.ID]; ok {
		c.Findings = n[0]
		if n[1] == 2 {
			c.Worst = model.SevCritical
		} else if n[1] == 1 {
			c.Worst = model.SevWarning
		} else {
			c.Worst = model.SevInfo
		}
	}
	switch e.Kind {
	case model.KindDevice:
		c.Device = deviceBrief(v, snap, e)
	case model.KindProject:
		c.Project = s.projectBrief(snap, e)
	case model.KindRepository:
		c.Repo = repoBrief(snap, e)
	case model.KindService:
		c.Service = serviceBrief(v, snap, e)
	}
	return c
}

func stub(v model.Viewer, snap *status.Snapshot, o *model.Entity, relation, dir string) Ref {
	ref := Ref{Relation: relation, Direction: dir, Kind: o.Kind, Health: snap.Health(o.ID).State}
	if v.CanSee(o.WorkspaceID) {
		ref.ID, ref.Slug, ref.Name = o.ID, o.Slug, o.Name
	} else {
		ref.Restricted, ref.Name = true, "受限资源"
	}
	return ref
}

// hidden reports whether a relation should be left out altogether. A person sees a
// restricted stub, which tells them something they depend on is unwell. For an agent
// a workspace outside its grant simply does not exist, not even as a kind and a state.
func hidden(v model.Viewer, o *model.Entity) bool {
	return v.Kind == "agent" && !v.CanSee(o.WorkspaceID)
}

func serviceBrief(v model.Viewer, snap *status.Snapshot, e *model.Entity) *ServiceBrief {
	b := &ServiceBrief{Kind: strOf(e.Attrs, "service_kind"), Environment: strOf(e.Attrs, "environment"), URL: strOf(e.Attrs, "url"),
		Hosts: []Ref{}, Projects: []Ref{}}
	for _, d := range snap.Related(e.ID, model.RelRunsOn) {
		if !hidden(v, d) {
			b.Hosts = append(b.Hosts, stub(v, snap, d, model.RelRunsOn, "out"))
		}
	}
	for _, p := range snap.Related(e.ID, model.RelPartOf) {
		if !hidden(v, p) {
			b.Projects = append(b.Projects, stub(v, snap, p, model.RelPartOf, "out"))
		}
	}
	return b
}

func deviceBrief(v model.Viewer, snap *status.Snapshot, e *model.Entity) *DeviceBrief {
	b := &DeviceBrief{Type: strOf(e.Attrs, "device_type"), OS: strOf(e.Attrs, "os")}
	for _, svc := range snap.Dependents(e.ID, model.RelRunsOn) {
		if !hidden(v, svc) {
			b.Services++
		}
	}
	hb := snap.Signal(e.ID, "host.heartbeat")
	if hb != nil {
		t := hb.ReceivedAt
		b.LastSeen = &t
		b.UptimeS = numOf(hb.Value["uptime_s"])
		b.Collector = strOf(hb.Value, "collector")
		if o := strOf(hb.Value, "os"); o != "" {
			b.OS = o
		}
		// Numbers from a silent host are history, not status.
		if hb.Stale {
			return b
		}
	}
	if g := snap.Signal(e.ID, "host.cpu"); g != nil && !g.Stale {
		b.CPUPct = numOf(g.Value["pct"])
	}
	if g := snap.Signal(e.ID, "host.memory"); g != nil && !g.Stale {
		b.MemPct = numOf(g.Value["pct"])
	}
	if g := snap.Signal(e.ID, "host.battery"); g != nil && !g.Stale {
		b.BatteryPct = numOf(g.Value["pct"])
	}
	// The fullest filesystem is the one worth showing on a card.
	for _, g := range snap.Signals[e.ID] {
		if !strings.HasPrefix(g.Name, "host.disk:") || g.Stale {
			continue
		}
		p := numOf(g.Value["pct"])
		if p != nil && (b.DiskPct == nil || *p > *b.DiskPct) {
			b.DiskPct = p
			b.DiskFreeGB = numOf(g.Value["free_gb"])
		}
	}
	return b
}

func (s *Service) projectBrief(snap *status.Snapshot, e *model.Entity) *ProjectBrief {
	b := &ProjectBrief{Stage: strOf(e.Attrs, "stage"), Priority: strOf(e.Attrs, "priority"),
		NextStep: strOf(e.Attrs, "next_step"), Role: strOf(e.Attrs, "role")}
	if bl, ok := e.Attrs["blockers"].([]any); ok {
		b.Blockers = len(bl)
	}
	var last *time.Time
	for _, d := range snap.Dependents(e.ID, model.RelPartOf) {
		if d.WorkspaceID != e.WorkspaceID {
			continue
		}
		switch d.Kind {
		case model.KindService:
			b.Services++
			if snap.Health(d.ID).State == model.HealthOK {
				b.ServicesUp++
			}
		case model.KindRepository:
			b.Repos++
			if p := pushedAt(d); p != nil && (last == nil || p.After(*last)) {
				last = p
			}
		}
	}
	// A catalog import touches every row, so an edit timestamp says nothing when a
	// repository can say when work actually happened.
	if last == nil {
		u := e.UpdatedAt
		last = &u
	}
	b.LastActivity = last
	// Borrowed from project trackers: an active project that has gone quiet fades
	// instead of keeping a confident status it no longer has evidence for.
	if b.Stage == "building" || b.Stage == "verifying" {
		b.UpdateStale = snap.Now.Sub(*last) > 14*24*time.Hour
	}
	return b
}

func pushedAt(e *model.Entity) *time.Time {
	s := strOf(e.Attrs, "pushed_at")
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	return nil
}

func repoBrief(snap *status.Snapshot, e *model.Entity) *RepoBrief {
	b := &RepoBrief{FullName: strOf(e.Attrs, "full_name"), Visibility: strOf(e.Attrs, "visibility"), Language: strOf(e.Attrs, "language"),
		PushedAt: pushedAt(e), OpenIssues: numOf(e.Attrs["open_issues"]), OpenPRs: numOf(e.Attrs["open_prs"])}
	if g := snap.Signal(e.ID, "repo.ci"); g != nil {
		b.CI = g.State
		if g.Stale {
			b.CI = model.StateUnknown
		}
	}
	for _, g := range snap.Signals[e.ID] {
		if !strings.HasPrefix(g.Name, "repo.local:") || g.Stale {
			continue
		}
		if v := numOf(g.Value["dirty"]); v != nil {
			b.LocalDirty += int(*v)
		}
		if v := numOf(g.Value["ahead"]); v != nil {
			b.LocalAhead += int(*v)
		}
	}
	return b
}

func (s *Service) slugs(ctx context.Context) (map[string]string, error) {
	m, err := s.Store.WorkspaceIDsBySlug(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(m))
	for slug, id := range m {
		out[id] = slug
	}
	return out, nil
}

func findingCounts(fs []model.Finding) map[string][2]int {
	out := map[string][2]int{}
	for _, f := range fs {
		n := out[f.EntityID]
		n[0]++
		rank := 0
		switch f.Severity {
		case model.SevCritical:
			rank = 2
		case model.SevWarning:
			rank = 1
		}
		if rank > n[1] {
			n[1] = rank
		}
		out[f.EntityID] = n
	}
	return out
}

// Cards lists entities with their rollups. It reuses the store's paging, so the
// page boundaries, filters and isolation are exactly those of ListEntities.
func (s *Service) Cards(ctx context.Context, v model.Viewer, f store.EntityFilter) ([]Card, store.Page, error) {
	page, err := s.Store.ListEntities(ctx, v, f)
	if err != nil {
		return nil, page, err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, page, err
	}
	slugs, err := s.slugs(ctx)
	if err != nil {
		return nil, page, err
	}
	fs, err := s.Store.ListFindings(ctx, v, store.FindingFilter{Open: true, Limit: 500})
	if err != nil {
		return nil, page, err
	}
	counts := findingCounts(fs)
	cards := make([]Card, 0, len(page.Items))
	for i := range page.Items {
		cards = append(cards, s.redact(ctx, v, s.card(v, snap, &page.Items[i], slugs, counts)))
	}
	return cards, page, nil
}

// Get returns the detail page of one entity, or store.ErrNotFound if it is
// outside the viewer's workspaces.
func (s *Service) Get(ctx context.Context, v model.Viewer, id string) (*Detail, error) {
	e, err := s.Store.GetEntity(ctx, v, id)
	if err != nil {
		return nil, err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	slugs, err := s.slugs(ctx)
	if err != nil {
		return nil, err
	}
	open, err := s.Store.ListFindings(ctx, v, store.FindingFilter{Open: true, EntityID: id})
	if err != nil {
		return nil, err
	}
	d := &Detail{Card: s.redact(ctx, v, s.card(v, snap, &e, slugs, findingCounts(open))), Open: s.redactFindings(ctx, v, open)}
	d.Relations = []Ref{}
	add := func(r model.Relation, otherID, dir string) {
		o := snap.Entities[otherID]
		if o == nil || hidden(v, o) {
			return
		}
		d.Relations = append(d.Relations, stub(v, snap, o, r.Type, dir))
	}
	for _, r := range snap.Out[id] {
		add(r, r.ToID, "out")
	}
	for _, r := range snap.In[id] {
		add(r, r.FromID, "in")
	}
	sort.SliceStable(d.Relations, func(i, j int) bool { return d.Relations[i].Kind < d.Relations[j].Kind })
	d.Signals = s.projectSignals(ctx, v, e.WorkspaceID, snap, snap.Signals[id])
	d.Checks = []model.Check{}
	if !s.limited(ctx, v, e.WorkspaceID) {
		if d.Checks, err = s.Store.ChecksFor(ctx, []string{id}); err != nil {
			return nil, err
		}
	}
	d.Evidence = []model.Evidence{}
	if e.Kind == model.KindProject {
		ev, err := s.Store.EvidenceFor(ctx, id, 50)
		if err != nil {
			return nil, err
		}
		d.Evidence = s.redactEvidence(ctx, v, e.WorkspaceID, ev)
	}
	return d, nil
}

func healthOrder(h string) int {
	return slices.Index([]string{model.HealthDown, model.HealthDegraded, model.HealthUnknown, model.HealthOK,
		model.HealthOffline, model.HealthPaused, model.HealthPlanned, model.HealthRetired}, h)
}

// Overview builds the landing page for a viewer, optionally narrowed to one
// workspace. A workspace outside the viewer's reach narrows to nothing.
func (s *Service) Overview(ctx context.Context, v model.Viewer, workspaceID string) (*Overview, error) {
	seesFeeds := v.All && v.Has(model.ScopeAdmin)
	if workspaceID != "" {
		narrowed := model.Viewer{PrincipalID: v.PrincipalID, Name: v.Name, Kind: v.Kind, Scopes: v.Scopes, Workspaces: map[string]bool{}}
		if v.CanSee(workspaceID) {
			narrowed.Workspaces[workspaceID] = true
		}
		v = narrowed
	}
	now := s.Store.Now()
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	slugs, err := s.slugs(ctx)
	if err != nil {
		return nil, err
	}
	wss, err := s.Store.ListWorkspaces(ctx, v)
	if err != nil {
		return nil, err
	}
	fs, err := s.Store.ListFindings(ctx, v, store.FindingFilter{Open: true, Limit: 500})
	if err != nil {
		return nil, err
	}
	o := &Overview{SchemaVersion: SchemaVersion, GeneratedAt: now, Attention: []model.Finding{}, Gaps: []model.Finding{},
		Matrix: []MatrixRow{}, Wall: []WallRow{}, Devices: []Card{}, Projects: []Card{}, Expiring: []Expiring{}, Sources: []SourceRow{}, Totals: map[string]int{}}
	active := []model.Finding{}
	for _, f := range fs {
		if f.SnoozeUntil != nil && f.SnoozeUntil.After(now) {
			o.Snoozed++
			continue
		}
		active = append(active, f)
		if f.Severity == model.SevInfo {
			o.Gaps = append(o.Gaps, f)
		} else {
			o.Attention = append(o.Attention, f)
		}
	}
	counts := findingCounts(active)
	rows := map[string]*MatrixRow{}
	walls := map[string]*WallRow{}
	for _, w := range wss {
		rows[w.ID] = &MatrixRow{Workspace: w, Devices: map[string]int{}, Services: map[string]int{}, Projects: map[string]int{}}
		walls[w.ID] = &WallRow{Workspace: w, Devices: []Lamp{}, Services: []Lamp{}}
	}
	for _, e := range snap.Entities {
		if !v.CanSee(e.WorkspaceID) {
			continue
		}
		o.Totals[e.Kind]++
		r := rows[e.WorkspaceID]
		wall := walls[e.WorkspaceID]
		h := snap.Health(e.ID)
		lamp := Lamp{ID: e.ID, Kind: e.Kind, Name: e.Name, State: h.State, Freshness: h.Freshness, Reason: h.Reason}
		switch e.Kind {
		case model.KindDevice:
			if r != nil {
				r.Devices[h.State]++
				wall.Devices = append(wall.Devices, lamp)
			}
			o.Devices = append(o.Devices, s.redact(ctx, v, s.card(v, snap, e, slugs, counts)))
		case model.KindService:
			if r != nil {
				r.Services[h.State]++
				wall.Services = append(wall.Services, lamp)
			}
			for _, g := range snap.Signals[e.ID] {
				if !strings.HasPrefix(g.Name, "check.tls") || g.Stale {
					continue
				}
				if t, err := time.Parse(time.RFC3339, strOf(g.Value, "not_after")); err == nil && t.Sub(now) < 45*24*time.Hour {
					name := strOf(g.Value, "host")
					if s.limited(ctx, v, e.WorkspaceID) {
						name = e.Name // the host name is the address being withheld
					}
					o.Expiring = append(o.Expiring, Expiring{EntityID: e.ID, Name: name, What: "证书", At: t, DaysLeft: int(t.Sub(now).Hours() / 24)})
				}
			}
		case model.KindProject:
			if r != nil {
				r.Projects[strOf(e.Attrs, "stage")]++
			}
			o.Projects = append(o.Projects, s.redact(ctx, v, s.card(v, snap, e, slugs, counts)))
		case model.KindRepository:
			if r != nil {
				r.Repos++
			}
		}
		if e.Kind == model.KindCredential || e.Kind == model.KindProject {
			key, what := "rotation_due", "凭据轮换"
			if e.Kind == model.KindProject {
				key, what = "due", "项目计划日期"
			}
			// rotation_due is outside the summary allow-list for credentials.
			hide := e.Kind == model.KindCredential && s.limited(ctx, v, e.WorkspaceID)
			if t, err := time.Parse("2006-01-02", strOf(e.Attrs, key)); err == nil && !hide && t.Sub(now) < 45*24*time.Hour {
				o.Expiring = append(o.Expiring, Expiring{EntityID: e.ID, Name: e.Name, What: what, At: t, DaysLeft: int(t.Sub(now).Hours() / 24)})
			}
		}
	}
	byHealth := func(l []Lamp) {
		sort.Slice(l, func(i, j int) bool {
			if a, b := healthOrder(l[i].State), healthOrder(l[j].State); a != b {
				return a < b
			}
			return l[i].Name < l[j].Name
		})
	}
	for _, w := range wss {
		o.Matrix = append(o.Matrix, *rows[w.ID])
		byHealth(walls[w.ID].Devices)
		byHealth(walls[w.ID].Services)
		o.Wall = append(o.Wall, *walls[w.ID])
	}
	sort.Slice(o.Devices, func(i, j int) bool {
		a, b := o.Devices[i], o.Devices[j]
		if ha, hb := healthOrder(a.Health.State), healthOrder(b.Health.State); ha != hb {
			return ha < hb
		}
		return a.Name < b.Name
	})
	sort.Slice(o.Projects, func(i, j int) bool {
		a, b := o.Projects[i], o.Projects[j]
		if pa, pb := a.Project.Priority, b.Project.Priority; pa != pb {
			if pa == "" {
				return false
			}
			if pb == "" {
				return true
			}
			return pa < pb
		}
		return a.Name < b.Name
	})
	sort.Slice(o.Expiring, func(i, j int) bool { return o.Expiring[i].At.Before(o.Expiring[j].At) })
	o.Attention, o.Gaps = s.redactFindings(ctx, v, o.Attention), s.redactFindings(ctx, v, o.Gaps)
	if seesFeeds {
		if o.Sources, err = s.Sources(ctx); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// Sources lists feeds with a computed condition.
func (s *Service) Sources(ctx context.Context) ([]SourceRow, error) {
	src, err := s.Store.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Store.Now()
	out := make([]SourceRow, 0, len(src))
	for _, x := range src {
		c := "ok"
		switch {
		case x.LastOKAt == nil:
			c = "never"
		case x.LastErrorAt != nil && x.LastErrorAt.After(*x.LastOKAt):
			c = "error"
		case x.IntervalS > 0 && now.Sub(*x.LastOKAt) > time.Duration(x.IntervalS*3+60)*time.Second:
			c = "stale"
		}
		out = append(out, SourceRow{Source: x, Condition: c})
	}
	return out, nil
}
