// Package store persists the catalog in SQLite. One writer connection serializes
// every write; reads go through a query-only pool under WAL. Every query that
// can return catalog data takes a model.Viewer, so workspace isolation is
// enforced here rather than left to each handler.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// Sentinel errors, mapped to wire codes by the api package.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("revision conflict")
	ErrInvalid  = errors.New("invalid")
)

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS workspaces (
	id           TEXT PRIMARY KEY,
	slug         TEXT NOT NULL UNIQUE,
	name         TEXT NOT NULL,
	kind         TEXT NOT NULL,
	color        TEXT NOT NULL DEFAULT '',
	note         TEXT NOT NULL DEFAULT '',
	cloud_policy TEXT NOT NULL DEFAULT 'summary',
	confirmed    INTEGER NOT NULL DEFAULT 1,
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS entities (
	id           TEXT PRIMARY KEY,
	kind         TEXT NOT NULL,
	workspace_id TEXT NOT NULL REFERENCES workspaces(id),
	slug         TEXT NOT NULL,
	name         TEXT NOT NULL,
	summary      TEXT NOT NULL DEFAULT '',
	lifecycle    TEXT NOT NULL DEFAULT 'active',
	attrs        TEXT NOT NULL DEFAULT '{}',
	tags         TEXT NOT NULL DEFAULT '[]',
	origin       TEXT NOT NULL DEFAULT 'manual',
	confirmed    INTEGER NOT NULL DEFAULT 1,
	rev          INTEGER NOT NULL DEFAULT 1,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL,
	UNIQUE (kind, slug)
);
CREATE INDEX IF NOT EXISTS entities_ws ON entities(workspace_id, kind);

CREATE TABLE IF NOT EXISTS relations (
	from_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	type    TEXT NOT NULL,
	to_id   TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	note    TEXT NOT NULL DEFAULT '',
	origin  TEXT NOT NULL DEFAULT 'manual',
	PRIMARY KEY (from_id, type, to_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS relations_to ON relations(to_id, type);

CREATE TABLE IF NOT EXISTS signals (
	entity_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	signal        TEXT NOT NULL,
	state         TEXT NOT NULL,
	value         TEXT NOT NULL DEFAULT '{}',
	summary       TEXT NOT NULL DEFAULT '',
	source        TEXT NOT NULL,
	observed_at   INTEGER NOT NULL,
	received_at   INTEGER NOT NULL,
	stale_after_s INTEGER NOT NULL,
	error         TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (entity_id, signal)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS samples (
	entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	ts        INTEGER NOT NULL,
	cpu REAL, mem REAL, disk REAL, swap REAL, load1 REAL, rx REAL, tx REAL, battery REAL,
	PRIMARY KEY (entity_id, ts)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS checks (
	id            TEXT PRIMARY KEY,
	entity_id     TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	kind          TEXT NOT NULL,
	target        TEXT NOT NULL,
	interval_s    INTEGER NOT NULL DEFAULT 60,
	timeout_s     INTEGER NOT NULL DEFAULT 10,
	expect_status TEXT NOT NULL DEFAULT '[]',
	expect_body   TEXT NOT NULL DEFAULT '',
	enabled       INTEGER NOT NULL DEFAULT 1,
	next_run_at   INTEGER NOT NULL DEFAULT 0,
	fail_streak   INTEGER NOT NULL DEFAULT 0,
	UNIQUE (entity_id, kind, target)
);

CREATE TABLE IF NOT EXISTS check_runs (
	check_id   TEXT NOT NULL REFERENCES checks(id) ON DELETE CASCADE,
	ts         INTEGER NOT NULL,
	ok         INTEGER NOT NULL,
	latency_ms INTEGER,
	error      TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (check_id, ts)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS findings (
	id           TEXT PRIMARY KEY,
	rule         TEXT NOT NULL,
	entity_id    TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	severity     TEXT NOT NULL,
	title        TEXT NOT NULL,
	detail       TEXT NOT NULL DEFAULT '',
	action       TEXT NOT NULL DEFAULT '',
	evidence     TEXT NOT NULL DEFAULT '{}',
	first_seen   INTEGER NOT NULL,
	last_seen    INTEGER NOT NULL,
	resolved_at  INTEGER,
	ack_at       INTEGER,
	snooze_until INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS findings_open ON findings(rule, entity_id) WHERE resolved_at IS NULL;

CREATE TABLE IF NOT EXISTS evidence (
	id          TEXT PRIMARY KEY,
	project_id  TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	verdict     TEXT NOT NULL,
	scope       TEXT NOT NULL,
	environment TEXT NOT NULL DEFAULT '',
	method      TEXT NOT NULL DEFAULT '',
	source_url  TEXT NOT NULL DEFAULT '',
	by_kind     TEXT NOT NULL,
	by_name     TEXT NOT NULL,
	note        TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS discoveries (
	id         TEXT PRIMARY KEY,
	kind       TEXT NOT NULL,
	device_id  TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
	key        TEXT NOT NULL,
	payload    TEXT NOT NULL DEFAULT '{}',
	state      TEXT NOT NULL DEFAULT 'new',
	first_seen INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	UNIQUE (kind, device_id, key)
);

CREATE TABLE IF NOT EXISTS sources (
	name          TEXT PRIMARY KEY,
	kind          TEXT NOT NULL,
	interval_s    INTEGER NOT NULL DEFAULT 0,
	last_ok_at    INTEGER,
	last_error_at INTEGER,
	last_error    TEXT NOT NULL DEFAULT '',
	detail        TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS report_cursors (
	source    TEXT PRIMARY KEY,
	run_id    TEXT NOT NULL DEFAULT '',
	seq       INTEGER NOT NULL DEFAULT 0,
	past_runs TEXT NOT NULL DEFAULT '[]',
	last_obs  INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS principals (
	id           TEXT PRIMARY KEY,
	kind         TEXT NOT NULL,
	name         TEXT NOT NULL UNIQUE,
	token_hash   BLOB NOT NULL UNIQUE,
	token_prefix TEXT NOT NULL,
	scopes       TEXT NOT NULL,
	workspaces   TEXT NOT NULL,
	devices      TEXT NOT NULL DEFAULT '[]',
	expires_at   INTEGER,
	revoked_at   INTEGER,
	last_used_at INTEGER,
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash   BLOB PRIMARY KEY,
	principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
	expires_at   INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS audit (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	ts           INTEGER NOT NULL,
	principal_id TEXT NOT NULL,
	principal    TEXT NOT NULL,
	action       TEXT NOT NULL,
	target       TEXT NOT NULL DEFAULT '',
	result       TEXT NOT NULL,
	ip           TEXT NOT NULL DEFAULT '',
	detail       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_ts ON audit(ts);
`

// Store is the database handle.
type Store struct {
	w   *sql.DB
	r   *sql.DB
	Now func() time.Time
}

// Open creates the data directory and opens the database.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.ToSlash(filepath.Join(dir, "wanglou.db"))
	dsn := func(extra string) string {
		q := url.Values{}
		q.Add("_pragma", "busy_timeout(5000)")
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		if extra != "" {
			q.Add("_pragma", extra)
		}
		return "file:" + path + "?" + q.Encode()
	}
	w, err := sql.Open("sqlite", dsn(""))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(schema + oauthSchema + logsSchema); err != nil {
		w.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	// Unit signals used to be keyed by unit alone; those rows can only go stale now.
	if _, err := w.Exec(`DELETE FROM signals WHERE signal LIKE 'unit:%' AND signal NOT LIKE 'unit:%:%'`); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn("query_only(1)"))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	return &Store{w: w, r: r, Now: func() time.Time { return time.Now().UTC() }}, nil
}

// Close closes both pools.
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

// Backup writes a consistent copy of the database. The target must not exist.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}
	_, err := s.w.ExecContext(ctx, "VACUUM INTO ?", dest)
	return err
}

// Prune applies retention. Raw samples and probe runs are kept for days;
// resolved findings and audit are kept longer.
func (s *Store) Prune(ctx context.Context) error {
	now := s.Now()
	steps := []struct {
		q   string
		age time.Duration
	}{
		{"DELETE FROM samples WHERE ts < ?", 30 * 24 * time.Hour},
		{"DELETE FROM check_runs WHERE ts < ?", 30 * 24 * time.Hour},
		{"DELETE FROM findings WHERE resolved_at IS NOT NULL AND resolved_at < ?", 180 * 24 * time.Hour},
		{"DELETE FROM sessions WHERE expires_at < ?", 0},
		{"DELETE FROM audit WHERE ts < ?", 365 * 24 * time.Hour},
	}
	for _, st := range steps {
		if _, err := s.w.ExecContext(ctx, st.q, ms(now.Add(-st.age))); err != nil {
			return err
		}
	}
	// A service moved to another host leaves unit signals nothing will ever refresh.
	if _, err := s.w.ExecContext(ctx, `DELETE FROM signals WHERE signal LIKE 'unit:%' AND NOT EXISTS (
		SELECT 1 FROM relations r JOIN entities d ON d.id=r.to_id
		WHERE r.from_id=signals.entity_id AND r.type='runs_on'
		AND substr(signals.signal,1,length(d.slug)+6)='unit:'||d.slug||':')`); err != nil {
		return err
	}
	// Likewise a check that was disabled or removed.
	if _, err := s.w.ExecContext(ctx, `DELETE FROM signals WHERE signal LIKE 'check.%' AND NOT EXISTS (
		SELECT 1 FROM checks c WHERE c.entity_id=signals.entity_id AND c.enabled=1
		AND signals.signal='check.'||c.kind||':'||c.target)`); err != nil {
		return err
	}
	if err := s.pruneLogs(ctx); err != nil {
		return err
	}
	return s.PruneOAuth(ctx)
}

var idEnc = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewID returns a prefixed random identifier that carries no meaning.
func NewID(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + idEnc.EncodeToString(b)
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v int64) time.Time { return time.UnixMilli(v).UTC() }

func fromNullMs(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMs(v.Int64)
	return &t
}

func nullMs(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ms(*t)
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func parseMap(s string) map[string]any {
	m := map[string]any{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func parseStrings(s string) []string {
	out := []string{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// workspaceClause builds the isolation predicate for a viewer.
func workspaceClause(v model.Viewer, col string) (string, []any) {
	if v.All {
		return "1=1", nil
	}
	if len(v.Workspaces) == 0 {
		return "0=1", nil
	}
	marks := make([]string, 0, len(v.Workspaces))
	args := make([]any, 0, len(v.Workspaces))
	for id := range v.Workspaces {
		marks = append(marks, "?")
		args = append(args, id)
	}
	return col + " IN (" + strings.Join(marks, ",") + ")", args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
