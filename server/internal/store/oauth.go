package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

const oauthSchema = `
CREATE TABLE IF NOT EXISTS oauth_clients (
	client_id     TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	redirect_uris TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_requests (
	id             TEXT PRIMARY KEY,
	client_id      TEXT NOT NULL,
	client_name    TEXT NOT NULL,
	redirect_uri   TEXT NOT NULL,
	state          TEXT NOT NULL,
	code_challenge TEXT NOT NULL,
	scope          TEXT NOT NULL,
	resource       TEXT NOT NULL,
	expires_at     INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_codes (
	code_hash      BLOB PRIMARY KEY,
	client_id      TEXT NOT NULL,
	redirect_uri   TEXT NOT NULL,
	code_challenge TEXT NOT NULL,
	resource       TEXT NOT NULL,
	principal_id   TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
	expires_at     INTEGER NOT NULL
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS oauth_tokens (
	token_hash   BLOB PRIMARY KEY,
	kind         TEXT NOT NULL,
	family       TEXT NOT NULL,
	client_id    TEXT NOT NULL,
	resource     TEXT NOT NULL,
	principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
	expires_at   INTEGER NOT NULL,
	used_at      INTEGER
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS oauth_tokens_family ON oauth_tokens(family);
`

// OAuthClient is a registered public client.
type OAuthClient struct {
	ClientID     string
	Name         string
	RedirectURIs []string
}

// OAuthRequest is an authorization request waiting for the owner's decision.
type OAuthRequest struct {
	ID            string
	ClientID      string
	ClientName    string
	RedirectURI   string
	State         string
	CodeChallenge string
	Scope         string
	Resource      string
	ExpiresAt     time.Time
}

// OAuthGrant is what an exchanged code or refresh token stands for.
type OAuthGrant struct {
	ClientID    string
	Resource    string
	PrincipalID string
	Family      string
}

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// SaveOAuthClient stores a dynamically registered client.
func (s *Store) SaveOAuthClient(ctx context.Context, c OAuthClient) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO oauth_clients(client_id,name,redirect_uris,created_at) VALUES(?,?,?,?)`,
		c.ClientID, c.Name, toJSON(c.RedirectURIs), ms(s.Now()))
	return err
}

// OAuthClientByID returns a registered client.
func (s *Store) OAuthClientByID(ctx context.Context, id string) (OAuthClient, error) {
	var c OAuthClient
	var uris string
	err := s.r.QueryRowContext(ctx, "SELECT client_id,name,redirect_uris FROM oauth_clients WHERE client_id=?", id).Scan(&c.ClientID, &c.Name, &uris)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	c.RedirectURIs = parseStrings(uris)
	return c, err
}

// SaveOAuthRequest parks an authorization request until the owner decides.
func (s *Store) SaveOAuthRequest(ctx context.Context, r OAuthRequest) (string, error) {
	r.ID = NewID("oar")
	_, err := s.w.ExecContext(ctx, `INSERT INTO oauth_requests(id,client_id,client_name,redirect_uri,state,code_challenge,scope,resource,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, r.ID, r.ClientID, r.ClientName, r.RedirectURI, r.State, r.CodeChallenge, r.Scope, r.Resource, ms(r.ExpiresAt))
	return r.ID, err
}

// OAuthRequestByID returns a request that has not expired.
func (s *Store) OAuthRequestByID(ctx context.Context, id string) (OAuthRequest, error) {
	var r OAuthRequest
	var exp int64
	err := s.w.QueryRowContext(ctx, `SELECT id,client_id,client_name,redirect_uri,state,code_challenge,scope,resource,expires_at
		FROM oauth_requests WHERE id=? AND expires_at>?`, id, ms(s.Now())).
		Scan(&r.ID, &r.ClientID, &r.ClientName, &r.RedirectURI, &r.State, &r.CodeChallenge, &r.Scope, &r.Resource, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.ExpiresAt = fromMs(exp)
	return r, err
}

// DeleteOAuthRequest removes a decided request.
func (s *Store) DeleteOAuthRequest(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, "DELETE FROM oauth_requests WHERE id=?", id)
	return err
}

// IssueOAuthCode creates a short-lived one-time code bound to a request.
func (s *Store) IssueOAuthCode(ctx context.Context, r OAuthRequest, principalID string, ttl time.Duration) (string, error) {
	code := randToken()
	_, err := s.w.ExecContext(ctx, `INSERT INTO oauth_codes(code_hash,client_id,redirect_uri,code_challenge,resource,principal_id,expires_at)
		VALUES(?,?,?,?,?,?,?)`, hashToken(code), r.ClientID, r.RedirectURI, r.CodeChallenge, r.Resource, principalID, ms(s.Now().Add(ttl)))
	return code, err
}

// OAuthCodeRecord is a stored code.
type OAuthCodeRecord struct {
	ClientID, RedirectURI, CodeChallenge, Resource, PrincipalID string
}

// ConsumeOAuthCode returns and deletes a code. The delete is what makes it
// one-time: a second attempt finds nothing.
func (s *Store) ConsumeOAuthCode(ctx context.Context, code string) (OAuthCodeRecord, error) {
	var c OAuthCodeRecord
	err := s.w.QueryRowContext(ctx, `DELETE FROM oauth_codes WHERE code_hash=? AND expires_at>?
		RETURNING client_id,redirect_uri,code_challenge,resource,principal_id`, hashToken(code), ms(s.Now())).
		Scan(&c.ClientID, &c.RedirectURI, &c.CodeChallenge, &c.Resource, &c.PrincipalID)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) mint(ctx context.Context, x execer, g OAuthGrant, accessTTL, refreshTTL time.Duration) (access, refresh string, err error) {
	if g.Family == "" {
		g.Family = NewID("fam")
	}
	access, refresh = "wl_o_"+randToken(), "wl_r_"+randToken()
	now := s.Now()
	for _, t := range []struct {
		tok, kind string
		ttl       time.Duration
	}{{access, "access", accessTTL}, {refresh, "refresh", refreshTTL}} {
		if _, err := x.ExecContext(ctx, `INSERT INTO oauth_tokens(token_hash,kind,family,client_id,resource,principal_id,expires_at) VALUES(?,?,?,?,?,?,?)`,
			hashToken(t.tok), t.kind, g.Family, g.ClientID, g.Resource, g.PrincipalID, ms(now.Add(t.ttl))); err != nil {
			return "", "", err
		}
	}
	return access, refresh, nil
}

// IssueOAuthTokens mints an access and refresh pair in a token family.
func (s *Store) IssueOAuthTokens(ctx context.Context, g OAuthGrant, accessTTL, refreshTTL time.Duration) (access, refresh string, err error) {
	return s.mint(ctx, s.w, g, accessTTL, refreshTTL)
}

// ErrRefreshReuse means a refresh token was presented twice, which is how a stolen
// token shows itself. The whole family is dead after this.
var ErrRefreshReuse = errors.New("refresh token reused")

// RefreshOAuth spends a refresh token and mints its successors in one transaction.
// The writer pool has a single connection, so two concurrent presentations of one
// token run strictly one after the other: the second sees it spent and kills the
// family, including whatever the first just minted.
func (s *Store) RefreshOAuth(ctx context.Context, tok, clientID string, accessTTL, refreshTTL time.Duration) (access, refresh string, err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var g OAuthGrant
	var exp int64
	var used sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT client_id,resource,principal_id,family,expires_at,used_at FROM oauth_tokens WHERE token_hash=? AND kind='refresh'`,
		hashToken(tok)).Scan(&g.ClientID, &g.Resource, &g.PrincipalID, &g.Family, &exp, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if clientID != "" && clientID != g.ClientID {
		return "", "", ErrNotFound
	}
	now := ms(s.Now())
	if used.Valid {
		if _, err := tx.ExecContext(ctx, "DELETE FROM oauth_tokens WHERE family=?", g.Family); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return "", "", ErrRefreshReuse
	}
	if exp < now {
		return "", "", ErrNotFound
	}
	var revoked sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT revoked_at FROM principals WHERE id=?", g.PrincipalID).Scan(&revoked); err != nil {
		return "", "", ErrNotFound
	}
	if revoked.Valid {
		return "", "", ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "UPDATE oauth_tokens SET used_at=? WHERE token_hash=?", now, hashToken(tok)); err != nil {
		return "", "", err
	}
	access, refresh, err = s.mint(ctx, tx, g, accessTTL, refreshTTL)
	if err != nil {
		return "", "", err
	}
	return access, refresh, tx.Commit()
}

// CountOAuthClients returns the number of registered clients.
func (s *Store) CountOAuthClients(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, "SELECT count(*) FROM oauth_clients").Scan(&n)
	return n, err
}

// AuthenticateOAuth resolves an access token for one resource. A token issued for
// a different resource is not valid here, whatever else is true of it.
func (s *Store) AuthenticateOAuth(ctx context.Context, tok, resource string) (model.Viewer, error) {
	row := s.r.QueryRowContext(ctx, `SELECT `+prefixed("p.", principalCols)+` FROM oauth_tokens t JOIN principals p ON p.id=t.principal_id
		WHERE t.token_hash=? AND t.kind='access' AND t.resource=? AND t.expires_at>?`, hashToken(tok), resource, ms(s.Now()))
	return s.finishAuth(ctx, row)
}

// PruneOAuth removes expired rows.
func (s *Store) PruneOAuth(ctx context.Context) error {
	now := ms(s.Now())
	for _, q := range []string{"DELETE FROM oauth_requests WHERE expires_at<?", "DELETE FROM oauth_codes WHERE expires_at<?", "DELETE FROM oauth_tokens WHERE expires_at<?"} {
		if _, err := s.w.ExecContext(ctx, q, now); err != nil {
			return err
		}
	}
	return s.PruneOAuthClients(ctx, 24*time.Hour)
}

// PruneOAuthClients removes registrations that never led to a token. Anyone can
// register, so without this a burst of junk would fill the table for good.
func (s *Store) PruneOAuthClients(ctx context.Context, age time.Duration) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM oauth_clients WHERE created_at<?
		AND client_id NOT IN (SELECT client_id FROM oauth_tokens)
		AND client_id NOT IN (SELECT client_id FROM oauth_codes)
		AND client_id NOT IN (SELECT client_id FROM oauth_requests)`, ms(s.Now().Add(-age)))
	return err
}
