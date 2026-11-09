package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// File is one YAML document. A catalog is any number of files in a directory;
// their contents are merged, so a file can hold one company or one object kind.
type File struct {
	Workspaces   []YWorkspace `yaml:"workspaces,omitempty"`
	Devices      []YEntity    `yaml:"devices,omitempty"`
	Services     []YEntity    `yaml:"services,omitempty"`
	Projects     []YEntity    `yaml:"projects,omitempty"`
	Repositories []YEntity    `yaml:"repositories,omitempty"`
	Credentials  []YEntity    `yaml:"credentials,omitempty"`
}

// YWorkspace is a workspace in YAML.
type YWorkspace struct {
	Slug        string `yaml:"slug"`
	Name        string `yaml:"name"`
	Kind        string `yaml:"kind"`
	Color       string `yaml:"color,omitempty"`
	Note        string `yaml:"note,omitempty"`
	CloudPolicy string `yaml:"cloud_policy,omitempty"`
	Unconfirmed bool   `yaml:"unconfirmed,omitempty"`
}

// YEntity is any catalog object in YAML. Relations are written from the
// dependent side using slugs, the way a person thinks about them.
type YEntity struct {
	Slug        string         `yaml:"slug"`
	Workspace   string         `yaml:"workspace"`
	Name        string         `yaml:"name"`
	Summary     string         `yaml:"summary,omitempty"`
	Lifecycle   string         `yaml:"lifecycle,omitempty"`
	Unconfirmed bool           `yaml:"unconfirmed,omitempty"`
	Tags        []string       `yaml:"tags,omitempty"`
	Attrs       map[string]any `yaml:"attrs,omitempty"`
	RunsOn      []string       `yaml:"runs_on,omitempty"`
	Project     []string       `yaml:"project,omitempty"`
	Repo        []string       `yaml:"repo,omitempty"`
	DependsOn   []string       `yaml:"depends_on,omitempty"`
	Uses        []string       `yaml:"uses,omitempty"`
	Checks      []YCheck       `yaml:"checks,omitempty"`
}

// YCheck is a probe in YAML.
type YCheck struct {
	Kind         string `yaml:"kind"`
	Target       string `yaml:"target"`
	IntervalS    int64  `yaml:"interval_s,omitempty"`
	TimeoutS     int64  `yaml:"timeout_s,omitempty"`
	ExpectStatus []int  `yaml:"expect_status,omitempty"`
	ExpectBody   string `yaml:"expect_body,omitempty"`
	Disabled     bool   `yaml:"disabled,omitempty"`
}

// Load reads every YAML file in a directory.
func Load(dir string) (*File, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s 下没有 .yaml 文件", dir)
	}
	all := &File{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var f File
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		all.Workspaces = append(all.Workspaces, f.Workspaces...)
		all.Devices = append(all.Devices, f.Devices...)
		all.Services = append(all.Services, f.Services...)
		all.Projects = append(all.Projects, f.Projects...)
		all.Repositories = append(all.Repositories, f.Repositories...)
		all.Credentials = append(all.Credentials, f.Credentials...)
	}
	return all, nil
}

// Result counts what an import did.
type Result struct {
	Workspaces int `json:"workspaces"`
	Created    int `json:"created"`
	Updated    int `json:"updated"`
	Relations  int `json:"relations"`
	Checks     int `json:"checks"`
}

// Import applies a catalog. It is an explicit act by a person, so fields present
// in the file win; fields absent from the file are left alone, which keeps edits
// made in the UI. Nothing is deleted.
func Import(ctx context.Context, s *store.Store, f *File) (Result, error) {
	var res Result
	for _, w := range f.Workspaces {
		if !slugRe.MatchString(w.Slug) {
			return res, fmt.Errorf("空间 slug %q 不合法", w.Slug)
		}
		pol := w.CloudPolicy
		if pol == "" {
			pol = "summary"
		}
		if !slices.Contains([]string{"allow", "summary", "deny"}, pol) {
			return res, fmt.Errorf("空间 %s 的 cloud_policy %q 不合法，只能是 allow、summary 或 deny", w.Slug, pol)
		}
		if !slices.Contains([]string{"personal", "company", "client"}, w.Kind) {
			return res, fmt.Errorf("空间 %s 的 kind %q 不合法", w.Slug, w.Kind)
		}
		for _, t := range []string{w.Name, w.Note} {
			if err := CheckText("空间 "+w.Slug, t); err != nil {
				return res, err
			}
		}
		if _, err := s.UpsertWorkspace(ctx, model.Workspace{Slug: w.Slug, Name: w.Name, Kind: w.Kind, Color: w.Color,
			Note: w.Note, CloudPolicy: pol, Confirmed: !w.Unconfirmed}); err != nil {
			return res, err
		}
		res.Workspaces++
	}
	ws, err := s.WorkspaceIDsBySlug(ctx)
	if err != nil {
		return res, err
	}
	sys := model.System()
	groups := []struct {
		kind  string
		items []YEntity
	}{
		{model.KindDevice, f.Devices}, {model.KindCredential, f.Credentials}, {model.KindProject, f.Projects},
		{model.KindRepository, f.Repositories}, {model.KindService, f.Services},
	}
	ids := map[string]string{}
	for _, g := range groups {
		for i := range g.items {
			y := &g.items[i]
			wsID, ok := ws[y.Workspace]
			if !ok {
				return res, fmt.Errorf("%s/%s 引用了不存在的空间 %q", g.kind, y.Slug, y.Workspace)
			}
			e := model.Entity{Kind: g.kind, WorkspaceID: wsID, Slug: y.Slug, Name: y.Name, Summary: y.Summary,
				Lifecycle: y.Lifecycle, Attrs: y.Attrs, Tags: y.Tags, Origin: "catalog", Confirmed: !y.Unconfirmed}
			if e.Attrs == nil {
				e.Attrs = map[string]any{}
			}
			if err := Validate(&e); err != nil {
				return res, err
			}
			cur, err := s.GetEntityBySlug(ctx, sys, g.kind, y.Slug)
			switch {
			case errors.Is(err, store.ErrNotFound):
				cur, err = s.CreateEntity(ctx, e)
				if err != nil {
					return res, err
				}
				res.Created++
			case err != nil:
				return res, err
			default:
				p := store.EntityPatch{Name: &e.Name, WorkspaceID: &wsID, Attrs: e.Attrs, Confirmed: &e.Confirmed}
				if y.Summary != "" {
					p.Summary = &e.Summary
				}
				if y.Lifecycle != "" {
					p.Lifecycle = &e.Lifecycle
				}
				if y.Tags != nil {
					p.Tags = &e.Tags
				}
				if cur, err = s.PatchEntity(ctx, sys, cur.ID, p); err != nil {
					return res, err
				}
				res.Updated++
			}
			ids[g.kind+"/"+y.Slug] = cur.ID
		}
	}
	resolve := func(kind, slug string) (string, error) {
		if id, ok := ids[kind+"/"+slug]; ok {
			return id, nil
		}
		e, err := s.GetEntityBySlug(ctx, sys, kind, slug)
		if err != nil {
			return "", fmt.Errorf("引用的 %s/%s 不存在", kind, slug)
		}
		return e.ID, nil
	}
	link := func(from, typ, kind string, slugs []string) error {
		for _, sl := range slugs {
			to, err := resolve(kind, sl)
			if err != nil {
				return err
			}
			if err := s.AddRelation(ctx, model.Relation{FromID: from, Type: typ, ToID: to, Origin: "catalog"}); err != nil {
				return err
			}
			res.Relations++
		}
		return nil
	}
	for _, g := range groups {
		for i := range g.items {
			y := &g.items[i]
			id := ids[g.kind+"/"+y.Slug]
			steps := []struct {
				typ, kind string
				slugs     []string
			}{
				{model.RelRunsOn, model.KindDevice, y.RunsOn},
				{model.RelPartOf, model.KindProject, y.Project},
				{model.RelBuiltFrom, model.KindRepository, y.Repo},
				{model.RelDependsOn, model.KindService, y.DependsOn},
				{model.RelUses, model.KindCredential, y.Uses},
			}
			for _, st := range steps {
				if err := link(id, st.typ, st.kind, st.slugs); err != nil {
					return res, fmt.Errorf("%s/%s: %w", g.kind, y.Slug, err)
				}
			}
			for _, c := range y.Checks {
				if err := CheckText(y.Slug+".checks", c.Target+" "+c.ExpectBody); err != nil {
					return res, err
				}
				chk := model.Check{EntityID: id, Kind: c.Kind, Target: c.Target, IntervalS: c.IntervalS, TimeoutS: c.TimeoutS,
					ExpectStatus: c.ExpectStatus, ExpectBody: c.ExpectBody, Enabled: !c.Disabled}
				if chk.IntervalS == 0 {
					chk.IntervalS = 60
					if c.Kind == "tls" {
						chk.IntervalS = 3600
					}
				}
				if chk.TimeoutS == 0 {
					chk.TimeoutS = 10
				}
				if _, err := s.UpsertCheck(ctx, chk); err != nil {
					return res, err
				}
				res.Checks++
			}
		}
	}
	return res, nil
}

// Export writes everything a viewer may read as one YAML document. Local
// signals and metrics are not part of a catalog; they are rebuilt by collectors.
func Export(ctx context.Context, s *store.Store, v model.Viewer) (*File, error) {
	out := &File{}
	wss, err := s.ListWorkspaces(ctx, v)
	if err != nil {
		return nil, err
	}
	slugOf := map[string]string{}
	for _, w := range wss {
		slugOf[w.ID] = w.Slug
		out.Workspaces = append(out.Workspaces, YWorkspace{Slug: w.Slug, Name: w.Name, Kind: w.Kind, Color: w.Color,
			Note: w.Note, CloudPolicy: w.CloudPolicy, Unconfirmed: !w.Confirmed})
	}
	all, err := s.AllEntities(ctx)
	if err != nil {
		return nil, err
	}
	rels, err := s.AllRelations(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]model.Entity{}
	var ids []string
	for _, e := range all {
		byID[e.ID] = e
		if v.CanSee(e.WorkspaceID) {
			ids = append(ids, e.ID)
		}
	}
	checks, err := s.ChecksFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	checksBy := map[string][]YCheck{}
	for _, c := range checks {
		checksBy[c.EntityID] = append(checksBy[c.EntityID], YCheck{Kind: c.Kind, Target: c.Target, IntervalS: c.IntervalS,
			TimeoutS: c.TimeoutS, ExpectStatus: c.ExpectStatus, ExpectBody: c.ExpectBody, Disabled: !c.Enabled})
	}
	for _, e := range all {
		if !v.CanSee(e.WorkspaceID) {
			continue
		}
		y := YEntity{Slug: e.Slug, Workspace: slugOf[e.WorkspaceID], Name: e.Name, Summary: e.Summary, Lifecycle: e.Lifecycle,
			Unconfirmed: !e.Confirmed, Tags: e.Tags, Attrs: e.Attrs, Checks: checksBy[e.ID]}
		for _, r := range rels {
			to, ok := byID[r.ToID]
			if r.FromID != e.ID || !ok || !v.CanSee(to.WorkspaceID) {
				continue
			}
			switch r.Type {
			case model.RelRunsOn:
				y.RunsOn = append(y.RunsOn, to.Slug)
			case model.RelPartOf:
				y.Project = append(y.Project, to.Slug)
			case model.RelBuiltFrom:
				y.Repo = append(y.Repo, to.Slug)
			case model.RelDependsOn:
				y.DependsOn = append(y.DependsOn, to.Slug)
			case model.RelUses:
				y.Uses = append(y.Uses, to.Slug)
			}
		}
		switch e.Kind {
		case model.KindDevice:
			out.Devices = append(out.Devices, y)
		case model.KindService:
			out.Services = append(out.Services, y)
		case model.KindProject:
			out.Projects = append(out.Projects, y)
		case model.KindRepository:
			out.Repositories = append(out.Repositories, y)
		case model.KindCredential:
			out.Credentials = append(out.Credentials, y)
		}
	}
	return out, nil
}
