// Package github keeps repository entities in step with GitHub. The platform stays
// the owner of code and CI; this connector only reads, and writes back nothing.
// A repository is keyed by GitHub's immutable numeric id, so a rename is an
// update, not a new object.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

const sourceName = "connector:github"

// Connector syncs one account.
type Connector struct {
	Store     *store.Store
	Token     func(context.Context) (string, error)
	Workspace string // workspace slug for repositories the catalog does not place
	Interval  time.Duration
	BaseURL   string
	HTTP      *http.Client
}

type repo struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Description   string    `json:"description"`
	HTMLURL       string    `json:"html_url"`
	Private       bool      `json:"private"`
	Archived      bool      `json:"archived"`
	Fork          bool      `json:"fork"`
	DefaultBranch string    `json:"default_branch"`
	Language      string    `json:"language"`
	PushedAt      time.Time `json:"pushed_at"`
	OpenIssues    int       `json:"open_issues_count"`
}

type runs struct {
	WorkflowRuns []struct {
		Name       string    `json:"name"`
		Status     string    `json:"status"`
		Conclusion string    `json:"conclusion"`
		HTMLURL    string    `json:"html_url"`
		HeadSHA    string    `json:"head_sha"`
		UpdatedAt  time.Time `json:"updated_at"`
	} `json:"workflow_runs"`
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (c *Connector) get(ctx context.Context, token, url string, out any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "wanglou")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", errors.New("无法连接 GitHub")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return "", errors.New("GitHub 限流，下一轮再试")
		}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", errors.New("GitHub 令牌无效或已过期")
	}
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", err
	}
	next := ""
	if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
		next = m[1]
	}
	return next, nil
}

// Sync runs one full reconciliation. It walks every page: a partial listing must
// not be mistaken for the whole set.
func (c *Connector) Sync(ctx context.Context) error {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.github.com"
	}
	interval := int64(c.Interval.Seconds())
	err := c.sync(ctx, interval)
	if err != nil {
		_ = c.Store.SourceError(ctx, sourceName, "connector", interval, err.Error())
	}
	return err
}

func (c *Connector) sync(ctx context.Context, interval int64) error {
	token, err := c.Token(ctx)
	if err != nil {
		return errors.New("取不到 GitHub 令牌")
	}
	ws, err := c.Store.WorkspaceIDsBySlug(ctx)
	if err != nil {
		return err
	}
	defaultWS, ok := ws[c.Workspace]
	if !ok {
		return fmt.Errorf("默认空间 %q 不存在", c.Workspace)
	}
	all := []repo{}
	url := c.BaseURL + "/user/repos?per_page=100&affiliation=owner,collaborator,organization_member&sort=pushed"
	for url != "" {
		var page []repo
		next, err := c.get(ctx, token, url, &page)
		if err != nil {
			return err
		}
		all = append(all, page...)
		url = next
	}
	existing, err := c.index(ctx)
	if err != nil {
		return err
	}
	sys := model.System()
	now := c.Store.Now()
	stale := max(interval*3, 3600)
	seen := map[string]bool{}
	ciFail := 0
	for _, r := range all {
		ext := strconv.FormatInt(r.ID, 10)
		attrs := map[string]any{
			"provider": "github", "external_id": ext, "full_name": r.FullName, "url": r.HTMLURL,
			"visibility": visibility(r), "default_branch": r.DefaultBranch, "language": r.Language,
			"pushed_at": r.PushedAt.UTC().Format(time.RFC3339), "fork": r.Fork,
		}
		id, ok := existing[ext]
		if !ok {
			id, ok = existing[strings.ToLower(r.HTMLURL)]
		}
		summary := r.Description
		if model.LooksSecret(summary) {
			summary = ""
		}
		if r := []rune(summary); len(r) > 300 {
			summary = string(r[:300])
		}
		if !ok {
			created, err := c.Store.CreateEntity(ctx, model.Entity{Kind: model.KindRepository, WorkspaceID: defaultWS,
				Slug: repoSlug(r.Name), Name: r.Name, Summary: summary, Attrs: attrs, Origin: "github", Confirmed: false})
			if errors.Is(err, store.ErrConflict) {
				created, err = c.Store.CreateEntity(ctx, model.Entity{Kind: model.KindRepository, WorkspaceID: defaultWS,
					Slug: repoSlug(strings.ReplaceAll(r.FullName, "/", ".")), Name: r.FullName, Summary: summary, Attrs: attrs, Origin: "github", Confirmed: false})
			}
			if err != nil {
				return err
			}
			id = created.ID
		} else if err := c.Store.SetSourceAttrs(ctx, id, attrs); err != nil {
			return err
		}
		seen[id] = true
		if r.Archived {
			if e, err := c.Store.GetEntity(ctx, sys, id); err == nil && e.Lifecycle != model.LifeRetired {
				life := model.LifeRetired
				if _, err := c.Store.PatchEntity(ctx, sys, id, store.EntityPatch{Lifecycle: &life}); err != nil {
					return err
				}
			}
		}
		// The issues count GitHub reports includes pull requests; the split is
		// fetched separately so neither number is misread.
		var pulls []struct {
			Number int `json:"number"`
		}
		if _, err := c.get(ctx, token, fmt.Sprintf("%s/repos/%s/pulls?state=open&per_page=100", c.BaseURL, r.FullName), &pulls); err == nil {
			_ = c.Store.SetSourceAttrs(ctx, id, map[string]any{"open_prs": len(pulls), "open_issues": max(r.OpenIssues-len(pulls), 0)})
		}
		if err := c.Store.PutSignal(ctx, model.Signal{EntityID: id, Name: "repo.meta", State: model.StateOK, Summary: "已同步",
			Value: map[string]any{"pushed_at": r.PushedAt}, Source: sourceName, ObservedAt: now, ReceivedAt: now, StaleAfterS: stale}); err != nil {
			return err
		}
		if failed, err := c.syncCI(ctx, token, id, r, now, stale); err != nil {
			return err
		} else if failed {
			ciFail++
		}
	}
	// A repository that vanished upstream is marked, never deleted: its history
	// and links stay meaningful and the disappearance may be a permissions change.
	missing := 0
	for key, id := range existing {
		if _, err := strconv.ParseInt(key, 10, 64); err != nil || seen[id] {
			continue
		}
		missing++
		if err := c.Store.PutSignal(ctx, model.Signal{EntityID: id, Name: "repo.meta", State: model.StateWarn, Summary: "GitHub 上已找不到这个仓库",
			Source: sourceName, ObservedAt: now, ReceivedAt: now, StaleAfterS: stale}); err != nil {
			return err
		}
	}
	return c.Store.SourceOK(ctx, sourceName, "connector", interval, map[string]any{"repos": len(all), "ci_failing": ciFail, "missing": missing})
}

func visibility(r repo) string {
	if r.Private {
		return "private"
	}
	return "public"
}

func (c *Connector) syncCI(ctx context.Context, token, id string, r repo, now time.Time, stale int64) (bool, error) {
	var rs runs
	url := fmt.Sprintf("%s/repos/%s/actions/runs?branch=%s&per_page=1&exclude_pull_requests=true", c.BaseURL, r.FullName, r.DefaultBranch)
	if _, err := c.get(ctx, token, url, &rs); err != nil || len(rs.WorkflowRuns) == 0 {
		return false, nil
	}
	w := rs.WorkflowRuns[0]
	state, summary := model.StateUnknown, w.Name+" 运行中"
	switch w.Conclusion {
	case "success":
		state, summary = model.StateOK, w.Name+" 通过"
	case "failure", "timed_out", "startup_failure":
		state, summary = model.StateFail, w.Name+" 失败"
	case "cancelled", "skipped", "neutral":
		state, summary = model.StateUnknown, w.Name+" "+w.Conclusion
	}
	short := w.HeadSHA
	if len(short) > 7 {
		short = short[:7]
	}
	err := c.Store.PutSignal(ctx, model.Signal{EntityID: id, Name: "repo.ci", State: state, Summary: summary,
		Value:  map[string]any{"url": w.HTMLURL, "conclusion": w.Conclusion, "sha": short, "workflow": w.Name},
		Source: sourceName, ObservedAt: w.UpdatedAt, ReceivedAt: now, StaleAfterS: stale})
	return state == model.StateFail, err
}

func (c *Connector) index(ctx context.Context) (map[string]string, error) {
	ents, err := c.Store.AllEntities(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range ents {
		if e.Kind != model.KindRepository {
			continue
		}
		if x, ok := e.Attrs["external_id"].(string); ok && x != "" {
			out[x] = e.ID
		}
		if u, ok := e.Attrs["url"].(string); ok && u != "" {
			out[strings.ToLower(u)] = e.ID
		}
	}
	return out, nil
}

var slugBad = regexp.MustCompile(`[^a-z0-9._-]+`)

// repoSlug turns a repository name into a catalog slug: lowercase, starting with a
// letter or digit, at most 81 characters. ".github" becomes "github", "_x" becomes "x".
func repoSlug(name string) string {
	s := strings.TrimLeft(slugBad.ReplaceAllString(strings.ToLower(name), "-"), "._-")
	if len(s) > 81 {
		s = s[:81]
	}
	if s == "" {
		s = "repo"
	}
	return s
}
