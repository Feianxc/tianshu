package store

import (
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// Log lines are evidence for locating a fault, not an archive: a week of warnings and
// errors per device, capped, redacted on the way in. Who may read them is decided in
// the view layer; this file only stores and filters.

const logsSchema = `
CREATE TABLE IF NOT EXISTS logs (
	device_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	ts        INTEGER NOT NULL, -- microseconds, the host's own clock
	hash      BLOB NOT NULL,
	unit      TEXT NOT NULL DEFAULT '',
	ident     TEXT NOT NULL DEFAULT '',
	priority  INTEGER NOT NULL,
	message   TEXT NOT NULL,
	repeat    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (device_id, ts, hash)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS logs_unit ON logs(device_id, unit, ts);
`

const (
	maxReportLogs = 200
	logMessageMax = 500
	logKeepDays   = 7
	logKeepRows   = 4000 // per device
)

// sanitizeLogs cuts a report's log lines to size and redacts each of them. Timestamps
// are the host's own and are kept as they are, even when its clock is ahead: they are
// part of the de-duplication key, and the per-device row cap bounds what a wrong clock
// can park beyond the age limit.
func sanitizeLogs(in []model.ReportLog) []model.ReportLog {
	if len(in) > maxReportLogs {
		in = in[len(in)-maxReportLogs:]
	}
	out := make([]model.ReportLog, 0, len(in))
	for _, l := range in {
		l.Message = strings.TrimSpace(model.RedactLog(l.Message, logMessageMax))
		if l.Message == "" || l.TS.IsZero() {
			continue
		}
		l.Unit, l.Ident = model.Scrub(l.Unit, 200), model.Scrub(l.Ident, 80)
		if l.Priority < 0 || l.Priority > 7 {
			l.Priority = 6
		}
		l.Repeat = min(max(l.Repeat, 0), 100000)
		out = append(out, l)
	}
	return out
}

// applyLogs stores new lines. The collector's window overlaps on purpose, so a line
// already held is only updated when it has since repeated more often.
func (s *Store) applyLogs(ctx context.Context, deviceID string, logs []model.ReportLog) (int, error) {
	if len(logs) == 0 {
		return 0, nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO logs(device_id,ts,hash,unit,ident,priority,message,repeat) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id,ts,hash) DO UPDATE SET repeat=max(repeat,excluded.repeat)`)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	for _, l := range logs {
		h := sha256.Sum256([]byte(l.Unit + "\x00" + l.Message))
		if _, err := st.ExecContext(ctx, deviceID, l.TS.UnixMicro(), h[:8], l.Unit, l.Ident, l.Priority, l.Message, l.Repeat); err != nil {
			return 0, err
		}
	}
	return len(logs), tx.Commit()
}

// LogFilter narrows a log query. Units nil means every unit; an empty non-nil slice
// means none, which is how "this service declares no unit" stays empty instead of
// returning the whole host.
type LogFilter struct {
	DeviceIDs    []string
	Units        []string
	ExcludeUnits []string
	MaxPriority  int // lines at this syslog priority or more severe; 0 means 7 (everything held)
	Since        time.Time
	Contains     string
	Limit        int
}

func (f LogFilter) where() (string, []any) {
	q, args := "device_id IN ("+placeholders(len(f.DeviceIDs))+")", make([]any, 0, len(f.DeviceIDs)+8)
	for _, id := range f.DeviceIDs {
		args = append(args, id)
	}
	if f.Units != nil {
		q += " AND unit IN (" + placeholders(len(f.Units)) + ")"
		for _, u := range f.Units {
			args = append(args, u)
		}
	}
	if len(f.ExcludeUnits) > 0 {
		q += " AND unit NOT IN (" + placeholders(len(f.ExcludeUnits)) + ")"
		for _, u := range f.ExcludeUnits {
			args = append(args, u)
		}
	}
	if f.MaxPriority > 0 && f.MaxPriority < 7 {
		q += " AND priority<=?"
		args = append(args, f.MaxPriority)
	}
	if !f.Since.IsZero() {
		q += " AND ts>=?"
		args = append(args, f.Since.UnixMicro())
	}
	if c := strings.TrimSpace(f.Contains); c != "" {
		q += " AND instr(lower(message), lower(?))>0"
		args = append(args, c)
	}
	return q, args
}

// ListLogs returns matching lines, newest first.
func (s *Store) ListLogs(ctx context.Context, f LogFilter) ([]model.LogLine, error) {
	out := []model.LogLine{}
	if len(f.DeviceIDs) == 0 || (f.Units != nil && len(f.Units) == 0) {
		return out, nil
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	where, args := f.where()
	rows, err := s.r.QueryContext(ctx, "SELECT device_id,ts,unit,ident,priority,message,repeat FROM logs WHERE "+where+" ORDER BY ts DESC LIMIT ?", append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l model.LogLine
		var us int64
		if err := rows.Scan(&l.DeviceID, &us, &l.Unit, &l.Ident, &l.Priority, &l.Message, &l.Repeat); err != nil {
			return nil, err
		}
		l.TS = time.UnixMicro(us).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// LogStats counts matching lines per unit. It is what a viewer who may not read the
// text still gets: where the noise is and when it last spoke.
func (s *Store) LogStats(ctx context.Context, f LogFilter) ([]model.LogStat, error) {
	out := []model.LogStat{}
	if len(f.DeviceIDs) == 0 || (f.Units != nil && len(f.Units) == 0) {
		return out, nil
	}
	where, args := f.where()
	rows, err := s.r.QueryContext(ctx, `SELECT unit, SUM(CASE WHEN priority<=3 THEN 1+repeat ELSE 0 END), SUM(CASE WHEN priority=4 THEN 1+repeat ELSE 0 END),
		SUM(1+repeat), MAX(ts) FROM logs WHERE `+where+` GROUP BY unit ORDER BY 2 DESC, 4 DESC LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x model.LogStat
		var us int64
		if err := rows.Scan(&x.Unit, &x.Errors, &x.Warnings, &x.Total, &us); err != nil {
			return nil, err
		}
		x.Newest = time.UnixMicro(us).UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}

// LogsEverSeen reports whether any line was ever stored for these devices, to tell
// "nothing went wrong" from "logs are not being collected here".
func (s *Store) LogsEverSeen(ctx context.Context, deviceIDs []string) (bool, error) {
	if len(deviceIDs) == 0 {
		return false, nil
	}
	args := make([]any, len(deviceIDs))
	for i, id := range deviceIDs {
		args[i] = id
	}
	var n int
	err := s.r.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM logs WHERE device_id IN ("+placeholders(len(deviceIDs))+"))", args...).Scan(&n)
	return n == 1, err
}

// UnitOwners maps each unit declared by a service running on a device to the
// workspaces of the services declaring it. A unit nobody declares belongs to the host.
func (s *Store) UnitOwners(ctx context.Context, deviceID string) (map[string][]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT e.workspace_id, e.attrs FROM entities e JOIN relations r ON r.from_id=e.id
		WHERE r.type='runs_on' AND r.to_id=? AND e.kind='service'`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var ws, attrs string
		if err := rows.Scan(&ws, &attrs); err != nil {
			return nil, err
		}
		for _, u := range DeclaredUnits(parseMap(attrs)) {
			out[u] = append(out[u], ws)
		}
	}
	return out, rows.Err()
}

// DeclaredUnits reads the unit and units attributes of a service.
func DeclaredUnits(attrs map[string]any) []string {
	out := []string{}
	if u, ok := attrs["unit"].(string); ok && u != "" {
		out = append(out, u)
	}
	if l, ok := attrs["units"].([]any); ok {
		for _, x := range l {
			if u, ok := x.(string); ok && u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

// pruneLogs applies age and size limits.
func (s *Store) pruneLogs(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, "DELETE FROM logs WHERE ts < ?", s.Now().Add(-logKeepDays*24*time.Hour).UnixMicro()); err != nil {
		return err
	}
	rows, err := s.r.QueryContext(ctx, "SELECT device_id FROM logs GROUP BY device_id HAVING COUNT(*) > ?", logKeepRows)
	if err != nil {
		return err
	}
	var over []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		over = append(over, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range over {
		if _, err := s.w.ExecContext(ctx, `DELETE FROM logs WHERE device_id=? AND ts < (
			SELECT ts FROM logs WHERE device_id=? ORDER BY ts DESC LIMIT 1 OFFSET ?)`, id, id, logKeepRows); err != nil {
			return err
		}
	}
	return nil
}

// RecentCheckRuns returns the latest results of one probe, newest first.
func (s *Store) RecentCheckRuns(ctx context.Context, checkID string, n int) ([]model.CheckRun, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT ts,ok,latency_ms,error FROM check_runs WHERE check_id=? ORDER BY ts DESC LIMIT ?", checkID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CheckRun{}
	for rows.Next() {
		var r model.CheckRun
		var ts int64
		var ok int
		var lat *int64
		if err := rows.Scan(&ts, &ok, &lat, &r.Error); err != nil {
			return nil, err
		}
		r.TS, r.OK, r.LatencyMS = fromMs(ts), ok == 1, lat
		out = append(out, r)
	}
	return out, rows.Err()
}
