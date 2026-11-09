package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// UpsertWorkspace creates or updates a workspace by slug and returns its id.
func (s *Store) UpsertWorkspace(ctx context.Context, w model.Workspace) (string, error) {
	var id string
	err := s.w.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE slug=?", w.Slug).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = NewID("ws")
		_, err = s.w.ExecContext(ctx, `INSERT INTO workspaces(id,slug,name,kind,color,note,cloud_policy,confirmed,created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, id, w.Slug, w.Name, w.Kind, w.Color, w.Note, w.CloudPolicy, boolInt(w.Confirmed), ms(s.Now()))
	case err == nil:
		_, err = s.w.ExecContext(ctx, `UPDATE workspaces SET name=?,kind=?,color=?,note=?,cloud_policy=?,confirmed=? WHERE id=?`,
			w.Name, w.Kind, w.Color, w.Note, w.CloudPolicy, boolInt(w.Confirmed), id)
	}
	return id, err
}

// ListWorkspaces returns the workspaces a viewer may read.
func (s *Store) ListWorkspaces(ctx context.Context, v model.Viewer) ([]model.Workspace, error) {
	clause, args := workspaceClause(v, "id")
	rows, err := s.r.QueryContext(ctx, `SELECT id,slug,name,kind,color,note,cloud_policy,confirmed,created_at
		FROM workspaces WHERE `+clause+` ORDER BY CASE kind WHEN 'personal' THEN 0 WHEN 'company' THEN 1 ELSE 2 END, name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Workspace{}
	for rows.Next() {
		var w model.Workspace
		var conf int
		var created int64
		if err := rows.Scan(&w.ID, &w.Slug, &w.Name, &w.Kind, &w.Color, &w.Note, &w.CloudPolicy, &conf, &created); err != nil {
			return nil, err
		}
		w.Confirmed = conf != 0
		w.CreatedAt = fromMs(created)
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkspaceIDsBySlug maps slugs to ids for every workspace. It is for building a
// Viewer, which is why it takes none.
func (s *Store) WorkspaceIDsBySlug(ctx context.Context) (map[string]string, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT slug,id FROM workspaces")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}

const entityCols = `id,kind,workspace_id,slug,name,summary,lifecycle,attrs,tags,origin,confirmed,rev,created_at,updated_at`

func scanEntity(row interface{ Scan(...any) error }) (model.Entity, error) {
	var e model.Entity
	var attrs, tags string
	var conf int
	var created, updated int64
	err := row.Scan(&e.ID, &e.Kind, &e.WorkspaceID, &e.Slug, &e.Name, &e.Summary, &e.Lifecycle,
		&attrs, &tags, &e.Origin, &conf, &e.Rev, &created, &updated)
	if err != nil {
		return e, err
	}
	e.Attrs = parseMap(attrs)
	e.Tags = parseStrings(tags)
	e.Confirmed = conf != 0
	e.CreatedAt = fromMs(created)
	e.UpdatedAt = fromMs(updated)
	return e, nil
}

// CreateEntity inserts a new entity and returns it with its id.
func (s *Store) CreateEntity(ctx context.Context, e model.Entity) (model.Entity, error) {
	if e.ID == "" {
		e.ID = NewID(e.Kind[:3])
	}
	if e.Attrs == nil {
		e.Attrs = map[string]any{}
	}
	if e.Tags == nil {
		e.Tags = []string{}
	}
	if e.Lifecycle == "" {
		e.Lifecycle = model.LifeActive
	}
	now := s.Now()
	e.Rev, e.CreatedAt, e.UpdatedAt = 1, now, now
	_, err := s.w.ExecContext(ctx, `INSERT INTO entities(`+entityCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Kind, e.WorkspaceID, e.Slug, e.Name, e.Summary, e.Lifecycle, toJSON(e.Attrs), toJSON(e.Tags),
		e.Origin, boolInt(e.Confirmed), e.Rev, ms(now), ms(now))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return e, fmt.Errorf("%w: %s/%s already exists", ErrConflict, e.Kind, e.Slug)
		}
		return e, err
	}
	return e, nil
}

// GetEntity returns one entity the viewer may read. An entity in another
// workspace is reported as not found so its existence does not leak.
func (s *Store) GetEntity(ctx context.Context, v model.Viewer, id string) (model.Entity, error) {
	clause, args := workspaceClause(v, "workspace_id")
	row := s.r.QueryRowContext(ctx, `SELECT `+entityCols+` FROM entities WHERE id=? AND `+clause, append([]any{id}, args...)...)
	e, err := scanEntity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// GetEntityBySlug is GetEntity addressed by kind and slug.
func (s *Store) GetEntityBySlug(ctx context.Context, v model.Viewer, kind, slug string) (model.Entity, error) {
	clause, args := workspaceClause(v, "workspace_id")
	row := s.r.QueryRowContext(ctx, `SELECT `+entityCols+` FROM entities WHERE kind=? AND slug=? AND `+clause,
		append([]any{kind, slug}, args...)...)
	e, err := scanEntity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// EntityFilter narrows ListEntities. Workspace is a workspace id; a workspace
// the viewer cannot see yields an empty page, never an error that confirms it.
type EntityFilter struct {
	Kind      string
	Workspace string
	Lifecycle string
	Tag       string
	Q         string
	Attr      map[string]string
	Cursor    string
	Limit     int
}

// Page is one page of entities.
type Page struct {
	Items      []model.Entity `json:"items"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

var attrKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// ListEntities returns one page in name order.
func (s *Store) ListEntities(ctx context.Context, v model.Viewer, f EntityFilter) (Page, error) {
	clause, args := workspaceClause(v, "workspace_id")
	q := `SELECT ` + entityCols + ` FROM entities WHERE ` + clause
	if f.Kind != "" {
		q += " AND kind=?"
		args = append(args, f.Kind)
	}
	if f.Workspace != "" {
		q += " AND workspace_id=?"
		args = append(args, f.Workspace)
	}
	if f.Lifecycle != "" {
		q += " AND lifecycle=?"
		args = append(args, f.Lifecycle)
	}
	if f.Tag != "" {
		q += " AND EXISTS (SELECT 1 FROM json_each(entities.tags) WHERE value=?)"
		args = append(args, f.Tag)
	}
	for k, val := range f.Attr {
		if !attrKey.MatchString(k) {
			return Page{}, fmt.Errorf("%w: attribute name", ErrInvalid)
		}
		q += " AND json_extract(attrs,'$." + k + "')=?"
		args = append(args, val)
	}
	if f.Q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Q) + "%"
		// An agent must not be able to confirm a hidden field by guessing it, so
		// attributes are only searchable where it may read them in full.
		attrs := `attrs LIKE ? ESCAPE '\'`
		if v.Kind == "agent" {
			attrs = `(attrs LIKE ? ESCAPE '\' AND workspace_id IN (SELECT id FROM workspaces WHERE cloud_policy='allow'))`
		}
		q += ` AND (name LIKE ? ESCAPE '\' OR slug LIKE ? ESCAPE '\' OR summary LIKE ? ESCAPE '\' OR ` + attrs + ` OR tags LIKE ? ESCAPE '\')`
		args = append(args, like, like, like, like, like)
	}
	if f.Cursor != "" {
		name, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return Page{}, fmt.Errorf("%w: cursor", ErrInvalid)
		}
		q += " AND (name,id) > (?,?)"
		args = append(args, name, id)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += " ORDER BY name,id LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	p := Page{Items: []model.Entity{}}
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, e)
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	if len(p.Items) > limit {
		p.Items = p.Items[:limit]
		last := p.Items[limit-1]
		p.HasMore = true
		p.NextCursor = encodeCursor(last.Name, last.ID)
	}
	return p, nil
}

func encodeCursor(name, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name + "\x00" + id))
}

func decodeCursor(c string) (string, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", err
	}
	name, id, ok := strings.Cut(string(b), "\x00")
	if !ok {
		return "", "", errors.New("malformed cursor")
	}
	return name, id, nil
}

// EntityPatch is a partial update. A nil field is left alone. In Attrs a nil
// value deletes the key.
type EntityPatch struct {
	Name        *string        `json:"name"`
	Summary     *string        `json:"summary"`
	Lifecycle   *string        `json:"lifecycle"`
	WorkspaceID *string        `json:"workspace_id"`
	Confirmed   *bool          `json:"confirmed"`
	Tags        *[]string      `json:"tags"`
	Attrs       map[string]any `json:"attrs"`
	Rev         int64          `json:"rev"` // expected revision; 0 skips the check
}

// PatchEntity applies a patch under a revision check.
func (s *Store) PatchEntity(ctx context.Context, v model.Viewer, id string, p EntityPatch) (model.Entity, error) {
	e, err := s.GetEntity(ctx, v, id)
	if err != nil {
		return e, err
	}
	if p.Rev != 0 && p.Rev != e.Rev {
		return e, ErrConflict
	}
	if p.Name != nil {
		e.Name = *p.Name
	}
	if p.Summary != nil {
		e.Summary = *p.Summary
	}
	if p.Lifecycle != nil {
		e.Lifecycle = *p.Lifecycle
	}
	if p.WorkspaceID != nil {
		if !v.CanSee(*p.WorkspaceID) {
			return e, ErrNotFound
		}
		e.WorkspaceID = *p.WorkspaceID
	}
	if p.Confirmed != nil {
		e.Confirmed = *p.Confirmed
	}
	if p.Tags != nil {
		e.Tags = *p.Tags
	}
	for k, val := range p.Attrs {
		if val == nil {
			delete(e.Attrs, k)
		} else {
			e.Attrs[k] = val
		}
	}
	now := s.Now()
	res, err := s.w.ExecContext(ctx, `UPDATE entities SET name=?,summary=?,lifecycle=?,workspace_id=?,attrs=?,tags=?,confirmed=?,rev=rev+1,updated_at=?
		WHERE id=? AND rev=?`, e.Name, e.Summary, e.Lifecycle, e.WorkspaceID, toJSON(e.Attrs), toJSON(e.Tags), boolInt(e.Confirmed), ms(now), id, e.Rev)
	if err != nil {
		return e, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return e, ErrConflict
	}
	e.Rev++
	e.UpdatedAt = now
	return e, nil
}

// SetSourceAttrs merges fields owned by a connector without touching anything a
// person edits and without a revision bump: a sync must never overwrite manual
// fields or race an editor.
func (s *Store) SetSourceAttrs(ctx context.Context, id string, attrs map[string]any) error {
	var cur string
	if err := s.w.QueryRowContext(ctx, "SELECT attrs FROM entities WHERE id=?", id).Scan(&cur); err != nil {
		return err
	}
	m := parseMap(cur)
	for k, val := range attrs {
		if val == nil {
			delete(m, k)
		} else {
			m[k] = val
		}
	}
	_, err := s.w.ExecContext(ctx, "UPDATE entities SET attrs=? WHERE id=?", toJSON(m), id)
	return err
}

// AllEntities returns every entity for background evaluation.
func (s *Store) AllEntities(ctx context.Context) ([]model.Entity, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+entityCols+` FROM entities ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Entity{}
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddRelation creates an edge. Adding an existing edge is a no-op.
func (s *Store) AddRelation(ctx context.Context, r model.Relation) error {
	_, err := s.w.ExecContext(ctx, `INSERT INTO relations(from_id,type,to_id,note,origin) VALUES(?,?,?,?,?)
		ON CONFLICT(from_id,type,to_id) DO UPDATE SET note=excluded.note`, r.FromID, r.Type, r.ToID, r.Note, r.Origin)
	return err
}

// RemoveRelation deletes an edge.
func (s *Store) RemoveRelation(ctx context.Context, from, typ, to string) error {
	_, err := s.w.ExecContext(ctx, "DELETE FROM relations WHERE from_id=? AND type=? AND to_id=?", from, typ, to)
	return err
}

// AllRelations returns every edge for background evaluation.
func (s *Store) AllRelations(ctx context.Context) ([]model.Relation, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT from_id,type,to_id,note,origin FROM relations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Relation{}
	for rows.Next() {
		var r model.Relation
		if err := rows.Scan(&r.FromID, &r.Type, &r.ToID, &r.Note, &r.Origin); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
