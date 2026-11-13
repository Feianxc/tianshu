package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

func parseInts(s string) []int {
	out := []int{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []int{}
	}
	return out
}

// hashToken is a plain SHA-256: tokens carry 256 bits of randomness, so there is
// nothing for a slow password hash to protect.
func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

func newToken(kind string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "wl_" + kind[:1] + "_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// CreatePrincipal stores a new identity and returns its token. The token is
// shown once; only its hash is kept.
func (s *Store) CreatePrincipal(ctx context.Context, p model.Principal) (model.Principal, string, error) {
	if p.Kind != "human" && p.Kind != "agent" && p.Kind != "collector" {
		return p, "", fmt.Errorf("%w: 身份类型只能是 human、agent 或 collector", ErrInvalid)
	}
	tok := newToken(p.Kind)
	p.ID = NewID("prn")
	p.TokenPrefix = tok[:9]
	p.CreatedAt = s.Now()
	if p.Scopes == nil {
		p.Scopes = []string{}
	}
	if p.Workspaces == nil {
		p.Workspaces = []string{}
	}
	if p.Devices == nil {
		p.Devices = []string{}
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO principals(id,kind,name,token_hash,token_prefix,scopes,workspaces,devices,expires_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, p.ID, p.Kind, p.Name, hashToken(tok), p.TokenPrefix, toJSON(p.Scopes), toJSON(p.Workspaces),
		toJSON(p.Devices), nullMs(p.ExpiresAt), ms(p.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return p, "", ErrConflict
		}
		return p, "", err
	}
	return p, tok, nil
}

const principalCols = `id,kind,name,token_prefix,scopes,workspaces,devices,expires_at,revoked_at,last_used_at,created_at`

func scanPrincipal(row interface{ Scan(...any) error }) (model.Principal, error) {
	var p model.Principal
	var scopes, ws, devs string
	var exp, rev, used sql.NullInt64
	var created int64
	err := row.Scan(&p.ID, &p.Kind, &p.Name, &p.TokenPrefix, &scopes, &ws, &devs, &exp, &rev, &used, &created)
	if err != nil {
		return p, err
	}
	p.Scopes, p.Workspaces, p.Devices = parseStrings(scopes), parseStrings(ws), parseStrings(devs)
	p.ExpiresAt, p.RevokedAt, p.LastUsedAt = fromNullMs(exp), fromNullMs(rev), fromNullMs(used)
	p.CreatedAt = fromMs(created)
	return p, nil
}

// ListPrincipals returns every identity. Token hashes never leave this package.
func (s *Store) ListPrincipals(ctx context.Context) ([]model.Principal, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+principalCols+` FROM principals ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Principal{}
	for rows.Next() {
		p, err := scanPrincipal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RevokePrincipal disables an identity and ends its sessions at once.
func (s *Store) RevokePrincipal(ctx context.Context, id string) error {
	res, err := s.w.ExecContext(ctx, "UPDATE principals SET revoked_at=? WHERE id=? AND revoked_at IS NULL", ms(s.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.w.ExecContext(ctx, "DELETE FROM sessions WHERE principal_id=?", id)
	return err
}

// permits is the ceiling of each identity kind. It is applied here, where a
// principal becomes a Viewer, so no path of creating a principal can exceed it. An
// agent reads and leaves evidence; it never edits, reports, or administers.
func permits(kind, scope string) bool {
	switch kind {
	case "human":
		return true
	case "collector":
		return scope == model.ScopeIngest
	}
	return scope == model.ScopeRead || scope == model.ScopeEvidence || scope == model.ScopeLogs
}

// ViewerFor turns a principal into a Viewer.
func (s *Store) ViewerFor(ctx context.Context, p model.Principal) (model.Viewer, error) {
	// Anything that is not recognisably a person or a collector is treated as an
	// agent, the most restricted kind, so a stored oddity cannot widen access.
	if p.Kind != "human" && p.Kind != "collector" {
		p.Kind = "agent"
	}
	v := model.Viewer{PrincipalID: p.ID, Name: p.Name, Kind: p.Kind,
		Scopes: map[string]bool{}, Workspaces: map[string]bool{}, Devices: map[string]bool{}}
	for _, sc := range p.Scopes {
		if permits(p.Kind, sc) {
			v.Scopes[sc] = true
		}
	}
	ids, err := s.WorkspaceIDsBySlug(ctx)
	if err != nil {
		return v, err
	}
	for _, slug := range p.Workspaces {
		if slug == "*" {
			v.All = true
		} else if id, ok := ids[slug]; ok {
			v.Workspaces[id] = true
		}
	}
	for _, d := range p.Devices {
		if d == "*" {
			v.AllDevices = true
		} else {
			v.Devices[d] = true
		}
	}
	if p.Kind == "agent" {
		return s.applyCloudPolicy(ctx, v, ids)
	}
	return v, nil
}

// applyCloudPolicy removes workspaces whose data may not reach a cloud model.
// This happens when the viewer is built, not when results are filtered, so a
// "deny" workspace does not exist as far as an agent can tell. An agent token
// holding "*" is expanded the same way and never carries the All flag.
func (s *Store) applyCloudPolicy(ctx context.Context, v model.Viewer, ids map[string]string) (model.Viewer, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT id,cloud_policy FROM workspaces")
	if err != nil {
		return v, err
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var id, policy string
		if err := rows.Scan(&id, &policy); err != nil {
			return v, err
		}
		// Anything but a known permissive value is treated as deny, so a typo in a
		// policy closes the door instead of opening it.
		if (policy == "allow" || policy == "summary") && (v.All || v.Workspaces[id]) {
			allowed[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return v, err
	}
	v.All, v.Workspaces = false, allowed
	return v, nil
}

// CloudPolicy returns a workspace's cloud handling rule.
func (s *Store) CloudPolicy(ctx context.Context, workspaceID string) (string, error) {
	var p string
	err := s.r.QueryRowContext(ctx, "SELECT cloud_policy FROM workspaces WHERE id=?", workspaceID).Scan(&p)
	return p, err
}

// Authenticate resolves a bearer token. Expired and revoked tokens fail the same
// way an unknown token does.
func (s *Store) Authenticate(ctx context.Context, tok string) (model.Viewer, error) {
	row := s.r.QueryRowContext(ctx, `SELECT `+principalCols+` FROM principals WHERE token_hash=?`, hashToken(tok))
	return s.finishAuth(ctx, row)
}

func (s *Store) finishAuth(ctx context.Context, row *sql.Row) (model.Viewer, error) {
	p, err := scanPrincipal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Viewer{}, ErrNotFound
	}
	if err != nil {
		return model.Viewer{}, err
	}
	now := s.Now()
	if p.RevokedAt != nil || (p.ExpiresAt != nil && now.After(*p.ExpiresAt)) {
		return model.Viewer{}, ErrNotFound
	}
	if p.LastUsedAt == nil || now.Sub(*p.LastUsedAt) > time.Minute {
		_, _ = s.w.ExecContext(ctx, "UPDATE principals SET last_used_at=? WHERE id=?", ms(now), p.ID)
	}
	return s.ViewerFor(ctx, p)
}

// CreateSession opens a browser session for a principal.
func (s *Store) CreateSession(ctx context.Context, principalID string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	_, err := s.w.ExecContext(ctx, "INSERT INTO sessions(token_hash,principal_id,expires_at) VALUES(?,?,?)",
		hashToken(tok), principalID, ms(s.Now().Add(ttl)))
	return tok, err
}

// SessionViewer resolves a session cookie.
func (s *Store) SessionViewer(ctx context.Context, tok string) (model.Viewer, error) {
	row := s.r.QueryRowContext(ctx, `SELECT `+prefixed("p.", principalCols)+` FROM sessions x JOIN principals p ON p.id=x.principal_id
		WHERE x.token_hash=? AND x.expires_at>?`, hashToken(tok), ms(s.Now()))
	return s.finishAuth(ctx, row)
}

// DeleteSession ends a browser session.
func (s *Store) DeleteSession(ctx context.Context, tok string) error {
	_, err := s.w.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hashToken(tok))
	return err
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ",")
}

// Audit appends one event.
func (s *Store) Audit(ctx context.Context, v model.Viewer, action, target, result, ip, detail string) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO audit(ts,principal_id,principal,action,target,result,ip,detail) VALUES(?,?,?,?,?,?,?,?)`,
		ms(s.Now()), v.PrincipalID, v.Name, action, target, result, ip, detail)
	return err
}

// ListAudit returns newest-first events.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id,ts,principal_id,principal,action,target,result,ip,detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEvent{}
	for rows.Next() {
		var a model.AuditEvent
		var ts int64
		if err := rows.Scan(&a.ID, &ts, &a.PrincipalID, &a.Principal, &a.Action, &a.Target, &a.Result, &a.IP, &a.Detail); err != nil {
			return nil, err
		}
		a.TS = fromMs(ts)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAgentScopes replaces the scopes of a live agent. Other kinds are not changed here:
// a person or a collector gets a new token instead.
func (s *Store) SetAgentScopes(ctx context.Context, id string, scopes []string) (model.Principal, error) {
	res, err := s.w.ExecContext(ctx, "UPDATE principals SET scopes=? WHERE id=? AND kind NOT IN ('human','collector') AND revoked_at IS NULL", toJSON(scopes), id)
	if err != nil {
		return model.Principal{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Principal{}, ErrNotFound
	}
	row := s.w.QueryRowContext(ctx, `SELECT `+principalCols+` FROM principals WHERE id=?`, id)
	return scanPrincipal(row)
}
