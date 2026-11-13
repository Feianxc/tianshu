package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// PutSignal records the newest observation of one aspect of an entity. An
// observation older than the stored one is dropped, so a delayed or replayed
// report cannot walk the state backwards.
func (s *Store) PutSignal(ctx context.Context, g model.Signal) error {
	if g.Value == nil {
		g.Value = map[string]any{}
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO signals(entity_id,signal,state,value,summary,source,observed_at,received_at,stale_after_s,error)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(entity_id,signal) DO UPDATE SET
			state=excluded.state,value=excluded.value,summary=excluded.summary,source=excluded.source,
			observed_at=excluded.observed_at,received_at=excluded.received_at,
			stale_after_s=excluded.stale_after_s,error=excluded.error
		WHERE excluded.observed_at >= signals.observed_at`,
		g.EntityID, g.Name, g.State, toJSON(g.Value), g.Summary, g.Source, ms(g.ObservedAt), ms(g.ReceivedAt), g.StaleAfterS, g.Error)
	return err
}

// DeleteSignals removes every signal of an entity whose name starts with prefix
// and that a source no longer reports.
func (s *Store) DeleteSignals(ctx context.Context, entityID, prefix string, keep map[string]bool) error {
	// Compared here, not with LIKE: _ is a legal character in a slug and a wildcard
	// there, so "web_1" would have matched and deleted the signals of "web-1".
	rows, err := s.r.QueryContext(ctx, "SELECT signal FROM signals WHERE entity_id=?", entityID)
	if err != nil {
		return err
	}
	var drop []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(n, prefix) && !keep[n] {
			drop = append(drop, n)
		}
	}
	rows.Close()
	for _, n := range drop {
		if _, err := s.w.ExecContext(ctx, "DELETE FROM signals WHERE entity_id=? AND signal=?", entityID, n); err != nil {
			return err
		}
	}
	return nil
}

// AllSignals returns every signal grouped by entity with Stale computed.
func (s *Store) AllSignals(ctx context.Context) (map[string][]model.Signal, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT entity_id,signal,state,value,summary,source,observed_at,received_at,stale_after_s,error
		FROM signals ORDER BY entity_id,signal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := s.Now()
	out := map[string][]model.Signal{}
	for rows.Next() {
		var g model.Signal
		var value string
		var obs, rec int64
		if err := rows.Scan(&g.EntityID, &g.Name, &g.State, &value, &g.Summary, &g.Source, &obs, &rec, &g.StaleAfterS, &g.Error); err != nil {
			return nil, err
		}
		g.Value = parseMap(value)
		g.ObservedAt = fromMs(obs)
		g.ReceivedAt = fromMs(rec)
		g.Stale = now.Sub(g.ReceivedAt) > time.Duration(g.StaleAfterS)*time.Second
		out[g.EntityID] = append(out[g.EntityID], g)
	}
	return out, rows.Err()
}

// PutSample stores one metrics row. A duplicate timestamp is ignored.
func (s *Store) PutSample(ctx context.Context, entityID string, x model.Sample) error {
	_, err := s.w.ExecContext(ctx, `INSERT OR IGNORE INTO samples(entity_id,ts,cpu,mem,disk,swap,load1,rx,tx,battery) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		entityID, ms(x.TS), x.CPUPct, x.MemPct, x.DiskPct, x.SwapPct, x.Load1, x.NetRxBps, x.NetTxBps, x.BatteryPct)
	return err
}

// Series returns metrics averaged into at most `points` buckets. An empty bucket
// is simply absent so a gap stays a gap in the chart.
func (s *Store) Series(ctx context.Context, entityID string, from, to time.Time, points int) ([]model.Sample, error) {
	if points <= 0 {
		points = 120
	}
	bucket := to.Sub(from).Milliseconds() / int64(points)
	if bucket < 1000 {
		bucket = 1000
	}
	rows, err := s.r.QueryContext(ctx, `SELECT (ts/?1)*?1 AS b, AVG(cpu),AVG(mem),AVG(disk),AVG(swap),AVG(load1),AVG(rx),AVG(tx),AVG(battery)
		FROM samples WHERE entity_id=?2 AND ts>=?3 AND ts<=?4 GROUP BY b ORDER BY b`, bucket, entityID, ms(from), ms(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Sample{}
	for rows.Next() {
		var ts int64
		var v [8]sql.NullFloat64
		if err := rows.Scan(&ts, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5], &v[6], &v[7]); err != nil {
			return nil, err
		}
		f := func(n sql.NullFloat64) *float64 {
			if !n.Valid {
				return nil
			}
			x := n.Float64
			return &x
		}
		out = append(out, model.Sample{TS: fromMs(ts), CPUPct: f(v[0]), MemPct: f(v[1]), DiskPct: f(v[2]), SwapPct: f(v[3]),
			Load1: f(v[4]), NetRxBps: f(v[5]), NetTxBps: f(v[6]), BatteryPct: f(v[7])})
	}
	return out, rows.Err()
}

// DiskTrend fits a line through disk usage over the window and returns the
// growth in percentage points per day. ok is false unless the data spans at
// least minSpan, because a projection from an hour of data would be noise.
func (s *Store) DiskTrend(ctx context.Context, entityID string, window, minSpan time.Duration) (perDay float64, span time.Duration, ok bool, err error) {
	now := s.Now()
	rows, err := s.r.QueryContext(ctx, "SELECT ts,disk FROM samples WHERE entity_id=? AND ts>=? AND disk IS NOT NULL ORDER BY ts", entityID, ms(now.Add(-window)))
	if err != nil {
		return 0, 0, false, err
	}
	defer rows.Close()
	var n, sx, sy, sxx, sxy float64
	var first, last int64
	for rows.Next() {
		var ts int64
		var d float64
		if err := rows.Scan(&ts, &d); err != nil {
			return 0, 0, false, err
		}
		if first == 0 {
			first = ts
		}
		last = ts
		x := float64(ts-first) / 86400000
		n++
		sx += x
		sy += d
		sxx += x * x
		sxy += x * d
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, err
	}
	span = time.Duration(last-first) * time.Millisecond
	den := n*sxx - sx*sx
	if n < 10 || span < minSpan || den == 0 {
		return 0, span, false, nil
	}
	return (n*sxy - sx*sy) / den, span, true, nil
}

// UpsertCheck creates or updates a probe.
func (s *Store) UpsertCheck(ctx context.Context, c model.Check) (string, error) {
	var id string
	err := s.w.QueryRowContext(ctx, "SELECT id FROM checks WHERE entity_id=? AND kind=? AND target=?", c.EntityID, c.Kind, c.Target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = NewID("chk")
		_, err = s.w.ExecContext(ctx, `INSERT INTO checks(id,entity_id,kind,target,interval_s,timeout_s,expect_status,expect_body,enabled)
			VALUES(?,?,?,?,?,?,?,?,?)`, id, c.EntityID, c.Kind, c.Target, c.IntervalS, c.TimeoutS, toJSON(c.ExpectStatus), c.ExpectBody, boolInt(c.Enabled))
		return id, err
	}
	if err != nil {
		return "", err
	}
	_, err = s.w.ExecContext(ctx, `UPDATE checks SET interval_s=?,timeout_s=?,expect_status=?,expect_body=?,enabled=? WHERE id=?`,
		c.IntervalS, c.TimeoutS, toJSON(c.ExpectStatus), c.ExpectBody, boolInt(c.Enabled), id)
	return id, err
}

func scanCheck(row interface{ Scan(...any) error }) (model.Check, error) {
	var c model.Check
	var expect string
	var enabled int
	err := row.Scan(&c.ID, &c.EntityID, &c.Kind, &c.Target, &c.IntervalS, &c.TimeoutS, &expect, &c.ExpectBody, &enabled)
	if err != nil {
		return c, err
	}
	c.Enabled = enabled != 0
	c.ExpectStatus = parseInts(expect)
	return c, nil
}

// DueChecks returns enabled probes whose time has come and claims them by moving
// next_run_at forward, so a slow probe is not started twice.
func (s *Store) DueChecks(ctx context.Context) ([]model.Check, error) {
	now := s.Now()
	rows, err := s.w.QueryContext(ctx, `SELECT id,entity_id,kind,target,interval_s,timeout_s,expect_status,expect_body,enabled
		FROM checks WHERE enabled=1 AND next_run_at<=?`, ms(now))
	if err != nil {
		return nil, err
	}
	out := []model.Check{}
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, c := range out {
		next := now.Add(time.Duration(c.IntervalS) * time.Second)
		if _, err := s.w.ExecContext(ctx, "UPDATE checks SET next_run_at=? WHERE id=?", ms(next), c.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RecordCheck stores a run and returns the consecutive failure count.
func (s *Store) RecordCheck(ctx context.Context, id string, ts time.Time, ok bool, latency *int64, msg string) (int, error) {
	if _, err := s.w.ExecContext(ctx, `INSERT OR REPLACE INTO check_runs(check_id,ts,ok,latency_ms,error) VALUES(?,?,?,?,?)`,
		id, ms(ts), boolInt(ok), latency, msg); err != nil {
		return 0, err
	}
	var streak int
	q := "UPDATE checks SET fail_streak=fail_streak+1 WHERE id=? RETURNING fail_streak"
	if ok {
		q = "UPDATE checks SET fail_streak=0 WHERE id=? RETURNING fail_streak"
	}
	err := s.w.QueryRowContext(ctx, q, id).Scan(&streak)
	return streak, err
}

// ChecksFor returns the probes of the given entities.
func (s *Store) ChecksFor(ctx context.Context, entityIDs []string) ([]model.Check, error) {
	out := []model.Check{}
	if len(entityIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(entityIDs))
	for i, id := range entityIDs {
		args[i] = id
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id,entity_id,kind,target,interval_s,timeout_s,expect_status,expect_body,enabled
		FROM checks WHERE entity_id IN (`+placeholders(len(args))+`) ORDER BY kind,target`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EntitiesWithChecks returns the set of entity ids that own an enabled probe.
func (s *Store) EntitiesWithChecks(ctx context.Context) (map[string]bool, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT DISTINCT entity_id FROM checks WHERE enabled=1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UpsertFinding opens or refreshes a finding. Severity and wording follow the
// current evaluation; first_seen and any acknowledgement survive. Getting worse
// ends a snooze: a person put off the warning, not the outage it became.
func (s *Store) UpsertFinding(ctx context.Context, f model.Finding) error {
	now := ms(s.Now())
	_, err := s.w.ExecContext(ctx, `INSERT INTO findings(id,rule,entity_id,severity,title,detail,action,evidence,first_seen,last_seen)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(rule,entity_id) WHERE resolved_at IS NULL DO UPDATE SET
			snooze_until=CASE WHEN (CASE excluded.severity WHEN 'critical' THEN 2 WHEN 'warning' THEN 1 ELSE 0 END)
				> (CASE findings.severity WHEN 'critical' THEN 2 WHEN 'warning' THEN 1 ELSE 0 END)
				THEN NULL ELSE findings.snooze_until END,
			severity=excluded.severity,title=excluded.title,detail=excluded.detail,action=excluded.action,
			evidence=excluded.evidence,last_seen=excluded.last_seen`,
		NewID("fnd"), f.Rule, f.EntityID, f.Severity, f.Title, f.Detail, f.Action, toJSON(f.Evidence), now, now)
	return err
}

// ResolveAbsent closes every open finding not in the keep set. keep is keyed by
// "rule|entity_id".
func (s *Store) ResolveAbsent(ctx context.Context, keep map[string]bool) (int, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT id,rule,entity_id FROM findings WHERE resolved_at IS NULL")
	if err != nil {
		return 0, err
	}
	var drop []string
	for rows.Next() {
		var id, rule, ent string
		if err := rows.Scan(&id, &rule, &ent); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[rule+"|"+ent] {
			drop = append(drop, id)
		}
	}
	rows.Close()
	now := ms(s.Now())
	for _, id := range drop {
		if _, err := s.w.ExecContext(ctx, "UPDATE findings SET resolved_at=? WHERE id=?", now, id); err != nil {
			return 0, err
		}
	}
	return len(drop), nil
}

// FindingFilter narrows ListFindings.
type FindingFilter struct {
	Open     bool
	EntityID string
	Limit    int
}

// ListFindings returns findings the viewer may read, worst first.
func (s *Store) ListFindings(ctx context.Context, v model.Viewer, f FindingFilter) ([]model.Finding, error) {
	clause, args := workspaceClause(v, "e.workspace_id")
	q := `SELECT f.id,f.rule,f.entity_id,e.name,e.kind,e.workspace_id,f.severity,f.title,f.detail,f.action,f.evidence,
		f.first_seen,f.last_seen,f.resolved_at,f.ack_at,f.snooze_until
		FROM findings f JOIN entities e ON e.id=f.entity_id WHERE ` + clause
	// Saying a key is shared with another workspace tells an agent that workspace
	// exists, and for an agent it does not.
	if v.Kind == "agent" {
		// Both rules describe other workspaces, which an agent cannot know exist.
		q += " AND f.rule NOT IN ('host.ssh_shared_key','credential.cross_workspace')"
	}
	if f.Open {
		q += " AND f.resolved_at IS NULL"
	}
	if f.EntityID != "" {
		q += " AND f.entity_id=?"
		args = append(args, f.EntityID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q += ` ORDER BY CASE f.severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, f.first_seen DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Finding{}
	for rows.Next() {
		var x model.Finding
		var ev string
		var first, last int64
		var res, ack, snooze sql.NullInt64
		if err := rows.Scan(&x.ID, &x.Rule, &x.EntityID, &x.EntityName, &x.EntityKind, &x.WorkspaceID, &x.Severity, &x.Title,
			&x.Detail, &x.Action, &ev, &first, &last, &res, &ack, &snooze); err != nil {
			return nil, err
		}
		x.Evidence = parseMap(ev)
		x.FirstSeen, x.LastSeen = fromMs(first), fromMs(last)
		x.ResolvedAt, x.AckAt, x.SnoozeUntil = fromNullMs(res), fromNullMs(ack), fromNullMs(snooze)
		out = append(out, x)
	}
	return out, rows.Err()
}

// AckFinding marks a finding as seen, optionally hiding it until a time. The
// viewer must be able to read the entity behind it.
func (s *Store) AckFinding(ctx context.Context, v model.Viewer, id string, snoozeUntil *time.Time) error {
	clause, args := workspaceClause(v, "e.workspace_id")
	var found string
	row := s.r.QueryRowContext(ctx, `SELECT f.id FROM findings f JOIN entities e ON e.id=f.entity_id WHERE f.id=? AND `+clause, append([]any{id}, args...)...)
	if err := row.Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_, err := s.w.ExecContext(ctx, "UPDATE findings SET ack_at=?,snooze_until=? WHERE id=?", ms(s.Now()), nullMs(snoozeUntil), id)
	return err
}

// AddEvidence attaches a verification record to a project.
func (s *Store) AddEvidence(ctx context.Context, x model.Evidence) (model.Evidence, error) {
	x.ID = NewID("evd")
	x.CreatedAt = s.Now()
	_, err := s.w.ExecContext(ctx, `INSERT INTO evidence(id,project_id,verdict,scope,environment,method,source_url,by_kind,by_name,note,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, x.ID, x.ProjectID, x.Verdict, x.Scope, x.Environment, x.Method, x.SourceURL, x.ByKind, x.ByName, x.Note, ms(x.CreatedAt))
	return x, err
}

// EvidenceFor returns newest-first records for a project. Callers have already
// authorized the project.
func (s *Store) EvidenceFor(ctx context.Context, projectID string, limit int) ([]model.Evidence, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id,project_id,verdict,scope,environment,method,source_url,by_kind,by_name,note,created_at
		FROM evidence WHERE project_id=? ORDER BY created_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Evidence{}
	for rows.Next() {
		var x model.Evidence
		var at int64
		if err := rows.Scan(&x.ID, &x.ProjectID, &x.Verdict, &x.Scope, &x.Environment, &x.Method, &x.SourceURL, &x.ByKind, &x.ByName, &x.Note, &at); err != nil {
			return nil, err
		}
		x.CreatedAt = fromMs(at)
		out = append(out, x)
	}
	return out, rows.Err()
}

// SeeDiscovery records that a collector saw something unmapped. A person's
// earlier decision to ignore or adopt it stands.
func (s *Store) SeeDiscovery(ctx context.Context, kind, deviceID, key string, payload map[string]any) error {
	now := ms(s.Now())
	// A device can propose at most 300 entries; past that, only known ones are refreshed.
	var n int
	if err := s.r.QueryRowContext(ctx, "SELECT count(*) FROM discoveries WHERE device_id=?", deviceID).Scan(&n); err != nil {
		return err
	}
	if n >= 300 {
		_, err := s.w.ExecContext(ctx, "UPDATE discoveries SET payload=?,last_seen=? WHERE kind=? AND device_id=? AND key=?", toJSON(payload), now, kind, deviceID, key)
		return err
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO discoveries(id,kind,device_id,key,payload,state,first_seen,last_seen) VALUES(?,?,?,?,?,'new',?,?)
		ON CONFLICT(kind,device_id,key) DO UPDATE SET payload=excluded.payload,last_seen=excluded.last_seen`,
		NewID("dsc"), kind, deviceID, key, toJSON(payload), now, now)
	return err
}

// ListDiscoveries returns inbox items in a state.
func (s *Store) ListDiscoveries(ctx context.Context, state string) ([]model.Discovery, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id,kind,device_id,key,payload,state,first_seen,last_seen FROM discoveries WHERE state=? ORDER BY kind,key`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Discovery{}
	for rows.Next() {
		var d model.Discovery
		var p string
		var a, b int64
		if err := rows.Scan(&d.ID, &d.Kind, &d.DeviceID, &d.Key, &p, &d.State, &a, &b); err != nil {
			return nil, err
		}
		d.Payload = parseMap(p)
		d.FirstSeen, d.LastSeen = fromMs(a), fromMs(b)
		out = append(out, d)
	}
	return out, rows.Err()
}

// closeDiscovery settles an inbox item once the catalog has caught up with it, so
// something added through a YAML edit does not keep asking to be adopted.
func (s *Store) closeDiscovery(ctx context.Context, kind, deviceID, key string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE discoveries SET state='adopted' WHERE kind=? AND device_id=? AND key=? AND state='new'`, kind, deviceID, key)
	return err
}

// DiscoveryByID returns one inbox item.
func (s *Store) DiscoveryByID(ctx context.Context, id string) (model.Discovery, error) {
	var d model.Discovery
	var p string
	var a, b int64
	err := s.r.QueryRowContext(ctx, `SELECT id,kind,device_id,key,payload,state,first_seen,last_seen FROM discoveries WHERE id=?`, id).
		Scan(&d.ID, &d.Kind, &d.DeviceID, &d.Key, &p, &d.State, &a, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.Payload = parseMap(p)
	d.FirstSeen, d.LastSeen = fromMs(a), fromMs(b)
	return d, err
}

// SetDiscoveryState records a decision about an inbox item.
func (s *Store) SetDiscoveryState(ctx context.Context, id, state string) error {
	res, err := s.w.ExecContext(ctx, "UPDATE discoveries SET state=? WHERE id=?", state, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SourceOK records a successful run of a feed.
// DropOtherProbeSources forgets probe sources named after other vantage points.
// Probes only ever run where the hub runs, so after the hub moves to another host
// the old vantage would otherwise sit in the source list as "stopped" forever.
func (s *Store) DropOtherProbeSources(ctx context.Context, keep string) error {
	_, err := s.w.ExecContext(ctx, "DELETE FROM sources WHERE kind='probe' AND name<>?", keep)
	return err
}

func (s *Store) SourceOK(ctx context.Context, name, kind string, intervalS int64, detail map[string]any) error {
	now := ms(s.Now())
	_, err := s.w.ExecContext(ctx, `INSERT INTO sources(name,kind,interval_s,last_ok_at,detail) VALUES(?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET kind=excluded.kind,interval_s=excluded.interval_s,last_ok_at=excluded.last_ok_at,detail=excluded.detail`,
		name, kind, intervalS, now, toJSON(detail))
	return err
}

// SourceError records a failed run. The message must already be free of secrets.
func (s *Store) SourceError(ctx context.Context, name, kind string, intervalS int64, msg string) error {
	now := ms(s.Now())
	_, err := s.w.ExecContext(ctx, `INSERT INTO sources(name,kind,interval_s,last_error_at,last_error) VALUES(?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET kind=excluded.kind,interval_s=excluded.interval_s,last_error_at=excluded.last_error_at,last_error=excluded.last_error`,
		name, kind, intervalS, now, msg)
	return err
}

// ListSources returns every feed.
func (s *Store) ListSources(ctx context.Context) ([]model.Source, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT name,kind,interval_s,last_ok_at,last_error_at,last_error,detail FROM sources")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Source{}
	for rows.Next() {
		var x model.Source
		var ok, bad sql.NullInt64
		var detail string
		if err := rows.Scan(&x.Name, &x.Kind, &x.IntervalS, &ok, &bad, &x.LastError, &detail); err != nil {
			return nil, err
		}
		x.LastOKAt, x.LastErrorAt = fromNullMs(ok), fromNullMs(bad)
		x.Detail = parseMap(detail)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.Compare(out[i].Name, out[j].Name) < 0
	})
	return out, nil
}
