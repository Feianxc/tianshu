// Package model holds the shared vocabulary of the catalog. Every state that
// reaches a human or an agent is built from these types, so the wire contract
// lives here: collections are always arrays, unknown numbers are null, and time
// is RFC 3339 in UTC.
package model

import "time"

// Entity kinds. A company or personal space is not a kind: it is a Workspace,
// the authorization boundary every entity belongs to.
const (
	KindDevice     = "device"
	KindService    = "service"
	KindProject    = "project"
	KindRepository = "repository"
	KindCredential = "credential"
)

// Kinds lists every entity kind in display order.
var Kinds = []string{KindDevice, KindService, KindProject, KindRepository, KindCredential}

// Lifecycle says what state an entity is meant to be in. It is declared by a
// person, never inferred from a probe: "paused" is why a stopped site is not an
// outage.
const (
	LifeActive  = "active"
	LifePaused  = "paused"
	LifePlanned = "planned"
	LifeRetired = "retired"
)

// Lifecycles lists every valid lifecycle.
var Lifecycles = []string{LifeActive, LifePaused, LifePlanned, LifeRetired}

// Relation types. Every relation points from the dependent to what it needs.
const (
	RelRunsOn    = "runs_on"    // service -> device
	RelPartOf    = "part_of"    // service|repository -> project
	RelBuiltFrom = "built_from" // service -> repository
	RelDependsOn = "depends_on" // service -> service
	RelUses      = "uses"       // any -> credential
)

// Relations lists every valid relation type.
var Relations = []string{RelRunsOn, RelPartOf, RelBuiltFrom, RelDependsOn, RelUses}

// Signal states. Unknown is a first-class value; a missing measurement must
// never collapse into ok or zero.
const (
	StateOK      = "ok"
	StateWarn    = "warn"
	StateFail    = "fail"
	StateUnknown = "unknown"
)

// Rolled-up health. Paused, offline and retired are expected conditions and are
// kept apart from faults.
const (
	HealthOK       = "ok"
	HealthDegraded = "degraded"
	HealthDown     = "down"
	HealthUnknown  = "unknown"
	HealthPaused   = "paused"
	HealthOffline  = "offline"
	HealthRetired  = "retired"
	HealthPlanned  = "planned"
)

// Freshness says how far the evidence behind a health value can be trusted.
const (
	FreshFresh   = "fresh"
	FreshStale   = "stale"
	FreshMissing = "missing"
)

// Finding severities.
const (
	SevCritical = "critical"
	SevWarning  = "warning"
	SevInfo     = "info"
)

// Verdicts are deliberately distinct from health and from task state: an agent
// saying "done" produces evidence, not a pass.
var Verdicts = []string{"pass", "fail", "partial", "blocked", "not run"}

// ProjectStages is the ordered list of project stages.
var ProjectStages = []string{"idea", "planned", "building", "verifying", "delivered", "maintaining", "paused", "archived"}

// Workspace is a person or a company. It is the authorization boundary.
type Workspace struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"` // personal | company | client
	Color       string    `json:"color"`
	Note        string    `json:"note"`
	CloudPolicy string    `json:"cloud_policy"` // allow | summary | deny
	Confirmed   bool      `json:"confirmed"`
	CreatedAt   time.Time `json:"created_at"`
}

// Entity is one catalog object. Attrs holds the kind-specific fields.
type Entity struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	WorkspaceID string         `json:"workspace_id"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Summary     string         `json:"summary"`
	Lifecycle   string         `json:"lifecycle"`
	Attrs       map[string]any `json:"attrs"`
	Tags        []string       `json:"tags"`
	Origin      string         `json:"origin"`    // catalog | manual | github | discovered
	Confirmed   bool           `json:"confirmed"` // false: inferred, waits for a person
	Rev         int64          `json:"rev"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Relation is a directed edge.
type Relation struct {
	FromID string `json:"from_id"`
	Type   string `json:"type"`
	ToID   string `json:"to_id"`
	Note   string `json:"note"`
	Origin string `json:"origin"`
}

// Signal is the newest observation of one aspect of an entity. ObservedAt is
// the clock of the source, ReceivedAt is ours; freshness is judged on ReceivedAt
// so a device with a wrong clock cannot look current forever.
type Signal struct {
	EntityID    string         `json:"entity_id"`
	Name        string         `json:"signal"`
	State       string         `json:"state"`
	Value       map[string]any `json:"value"`
	Summary     string         `json:"summary"`
	Source      string         `json:"source"`
	ObservedAt  time.Time      `json:"observed_at"`
	ReceivedAt  time.Time      `json:"received_at"`
	StaleAfterS int64          `json:"stale_after_s"`
	Error       string         `json:"error,omitempty"`
	Stale       bool           `json:"stale"` // computed on read
}

// Sample is one row of host metrics. A nil field was not measured.
type Sample struct {
	TS         time.Time `json:"ts"`
	CPUPct     *float64  `json:"cpu_pct"`
	MemPct     *float64  `json:"mem_pct"`
	DiskPct    *float64  `json:"disk_pct"`
	SwapPct    *float64  `json:"swap_pct"`
	Load1      *float64  `json:"load1"`
	NetRxBps   *float64  `json:"net_rx_bps"`
	NetTxBps   *float64  `json:"net_tx_bps"`
	BatteryPct *float64  `json:"battery_pct"`
}

// Check is an active probe owned by an entity.
type Check struct {
	ID           string `json:"id"`
	EntityID     string `json:"entity_id"`
	Kind         string `json:"kind"` // http | tcp | tls
	Target       string `json:"target"`
	IntervalS    int64  `json:"interval_s"`
	TimeoutS     int64  `json:"timeout_s"`
	ExpectStatus []int  `json:"expect_status"`
	ExpectBody   string `json:"expect_body,omitempty"`
	Enabled      bool   `json:"enabled"`
}

// Finding is one thing a person may need to act on. It is keyed by rule and
// entity, so a condition that persists stays one row instead of a stream of
// alerts.
type Finding struct {
	ID          string         `json:"id"`
	Rule        string         `json:"rule"`
	EntityID    string         `json:"entity_id"`
	EntityName  string         `json:"entity_name"`
	EntityKind  string         `json:"entity_kind"`
	WorkspaceID string         `json:"workspace_id"`
	Severity    string         `json:"severity"`
	Title       string         `json:"title"`
	Detail      string         `json:"detail"`
	Action      string         `json:"action"`
	Evidence    map[string]any `json:"evidence"`
	FirstSeen   time.Time      `json:"first_seen"`
	LastSeen    time.Time      `json:"last_seen"`
	ResolvedAt  *time.Time     `json:"resolved_at"`
	AckAt       *time.Time     `json:"ack_at"`
	SnoozeUntil *time.Time     `json:"snooze_until"`
}

// Evidence is one verification record attached to a project.
type Evidence struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Verdict     string    `json:"verdict"`
	Scope       string    `json:"scope"`
	Environment string    `json:"environment"`
	Method      string    `json:"method"`
	SourceURL   string    `json:"source_url"`
	ByKind      string    `json:"by_kind"`
	ByName      string    `json:"by_name"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}

// Discovery is something a collector saw that the catalog does not know yet. It
// waits in an inbox; nothing is adopted automatically.
type Discovery struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"` // unit | repo
	DeviceID  string         `json:"device_id"`
	Key       string         `json:"key"`
	Payload   map[string]any `json:"payload"`
	State     string         `json:"state"` // new | adopted | ignored
	FirstSeen time.Time      `json:"first_seen"`
	LastSeen  time.Time      `json:"last_seen"`
}

// Source is one collector or connector, so a broken feed is visible on its own
// instead of silently aging every number it used to provide.
type Source struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"` // agent | ssh | connector | probe | heartbeat
	IntervalS   int64          `json:"interval_s"`
	LastOKAt    *time.Time     `json:"last_ok_at"`
	LastErrorAt *time.Time     `json:"last_error_at"`
	LastError   string         `json:"last_error"`
	Detail      map[string]any `json:"detail"`
}

// Principal is an identity: a person, an agent, or a collector. Agents never
// inherit the rights of a person.
type Principal struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"` // human | agent | collector
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	Workspaces  []string   `json:"workspaces"` // workspace slugs, or ["*"]
	Devices     []string   `json:"devices"`    // device slugs a collector may report for, or ["*"]
	ExpiresAt   *time.Time `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AuditEvent records who did what. It never carries secret values or full
// request bodies.
type AuditEvent struct {
	ID          int64     `json:"id"`
	TS          time.Time `json:"ts"`
	PrincipalID string    `json:"principal_id"`
	Principal   string    `json:"principal"`
	Action      string    `json:"action"`
	Target      string    `json:"target"`
	Result      string    `json:"result"`
	IP          string    `json:"ip"`
	Detail      string    `json:"detail"`
}

// LogLine is one stored journal line. DeviceID is internal; the view layer replaces
// it with a slug the viewer is allowed to know.
type LogLine struct {
	TS       time.Time `json:"ts"`
	DeviceID string    `json:"-"`
	Device   string    `json:"device,omitempty"`
	Unit     string    `json:"unit"`
	Ident    string    `json:"ident,omitempty"`
	Priority int       `json:"priority"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
	Repeat   int       `json:"repeat,omitempty"`
}

// LogStat counts the lines of one unit.
type LogStat struct {
	Unit     string    `json:"unit"`
	Errors   int       `json:"errors"`
	Warnings int       `json:"warnings"`
	Total    int       `json:"total"`
	Newest   time.Time `json:"newest"`
}

// CheckRun is one probe result.
type CheckRun struct {
	TS        time.Time `json:"ts"`
	OK        bool      `json:"ok"`
	LatencyMS *int64    `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
}

// LogLevel names a syslog priority.
func LogLevel(p int) string {
	switch {
	case p <= 2:
		return "critical"
	case p == 3:
		return "error"
	case p == 4:
		return "warning"
	case p == 5:
		return "notice"
	}
	return "info"
}
