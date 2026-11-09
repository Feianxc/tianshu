package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/status"
	"github.com/Feianxc/tianshu/server/internal/store"
)

type fakeHub struct {
	repos    []repo
	perPage  int
	status   int
	ciResult string
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	switch {
	case r.URL.Path == "/user/repos":
		page := 1
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		lo := (page - 1) * f.perPage
		hi := min(lo+f.perPage, len(f.repos))
		if hi < len(f.repos) {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/user/repos?page=%d>; rel="next"`, r.Host, page+1))
		}
		json.NewEncoder(w).Encode(f.repos[lo:hi])
	case strings.HasSuffix(r.URL.Path, "/pulls"):
		json.NewEncoder(w).Encode([]map[string]int{{"number": 1}})
	case strings.HasSuffix(r.URL.Path, "/actions/runs"):
		if f.ciResult == "" {
			json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []any{}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []map[string]any{{"name": "ci", "status": "completed", "conclusion": f.ciResult, "html_url": "https://x", "head_sha": "abcdef012345", "updated_at": time.Now()}}})
	default:
		w.WriteHeader(404)
	}
}

func setup(t *testing.T, f *fakeHub) (*Connector, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	st.UpsertWorkspace(ctx, model.Workspace{Slug: "personal", Name: "个人", Kind: "personal", CloudPolicy: "allow"})
	st.UpsertWorkspace(ctx, model.Workspace{Slug: "company", Name: "公司", Kind: "company", CloudPolicy: "summary"})
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	return &Connector{Store: st, BaseURL: ts.URL, Workspace: "personal", Interval: 15 * time.Minute,
		Token: func(context.Context) (string, error) { return "secret-token-value", nil }}, st
}

func r(id int64, name string) repo {
	return repo{ID: id, Name: name, FullName: "me/" + name, HTMLURL: "https://github.com/me/" + name, DefaultBranch: "main", PushedAt: time.Now(), OpenIssues: 3}
}

func repos(t *testing.T, st *store.Store) []model.Entity {
	t.Helper()
	all, err := st.AllEntities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := []model.Entity{}
	for _, e := range all {
		if e.Kind == model.KindRepository {
			out = append(out, e)
		}
	}
	return out
}

func TestEveryPageIsRead(t *testing.T) {
	f := &fakeHub{perPage: 2}
	for i := range 5 {
		f.repos = append(f.repos, r(int64(i+1), fmt.Sprintf("r%d", i)))
	}
	c, st := setup(t, f)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(repos(t, st)); n != 5 {
		t.Fatalf("分页不完整: %d", n)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(repos(t, st)); n != 5 {
		t.Fatalf("重复同步产生了重复对象: %d", n)
	}
}

func TestRenameIsTheSameRepository(t *testing.T) {
	f := &fakeHub{perPage: 10, repos: []repo{r(7, "old-name")}}
	c, st := setup(t, f)
	c.Sync(context.Background())
	before := repos(t, st)[0]
	f.repos = []repo{r(7, "new-name")}
	c.Sync(context.Background())
	all := repos(t, st)
	if len(all) != 1 || all[0].ID != before.ID || all[0].Attrs["full_name"] != "me/new-name" {
		t.Fatalf("改名不能变成新对象: %+v", all)
	}
}

func TestSyncDoesNotOverwriteWhatAPersonOwns(t *testing.T) {
	f := &fakeHub{perPage: 10, repos: []repo{r(1, "a")}}
	c, st := setup(t, f)
	ctx := context.Background()
	c.Sync(ctx)
	e := repos(t, st)[0]
	ids, _ := st.WorkspaceIDsBySlug(ctx)
	name, summary, ws := "我起的名字", "我写的说明", ids["company"]
	confirmed := true
	if _, err := st.PatchEntity(ctx, model.System(), e.ID, store.EntityPatch{Name: &name, Summary: &summary, WorkspaceID: &ws, Confirmed: &confirmed,
		Attrs: map[string]any{"note": "人工备注"}}); err != nil {
		t.Fatal(err)
	}
	c.Sync(ctx)
	got := repos(t, st)[0]
	if got.Name != name || got.Summary != summary || got.WorkspaceID != ws || got.Attrs["note"] != "人工备注" || !got.Confirmed {
		t.Fatalf("同步覆盖了人工字段: %+v", got)
	}
}

func TestPullRequestsAreNotCountedAsIssues(t *testing.T) {
	f := &fakeHub{perPage: 10, repos: []repo{r(1, "a")}}
	c, st := setup(t, f)
	c.Sync(context.Background())
	a := repos(t, st)[0].Attrs
	if a["open_prs"] != float64(1) || a["open_issues"] != float64(2) {
		t.Fatalf("%v", a)
	}
}

func TestVanishedRepositoryIsMarkedNotDeleted(t *testing.T) {
	f := &fakeHub{perPage: 10, repos: []repo{r(1, "a"), r(2, "b")}}
	c, st := setup(t, f)
	ctx := context.Background()
	c.Sync(ctx)
	f.repos = f.repos[:1]
	c.Sync(ctx)
	if n := len(repos(t, st)); n != 2 {
		t.Fatalf("上游消失不能删除本地记录: %d", n)
	}
	sigs, _ := st.AllSignals(ctx)
	found := false
	for _, list := range sigs {
		for _, g := range list {
			if g.Name == "repo.meta" && g.State == model.StateWarn {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("要提示仓库已找不到")
	}
	ents, _ := st.AllEntities(ctx)
	rels, _ := st.AllRelations(ctx)
	snap := status.NewSnapshot(st.Now(), ents, sigs, rels)
	degraded := 0
	for _, e := range repos(t, st) {
		if snap.Health(e.ID).State == model.HealthDegraded {
			degraded++
		}
	}
	if degraded != 1 {
		t.Fatalf("上游消失的仓库不能显示正常，降级数 %d", degraded)
	}
}

func TestArchivedRepositoryIsRetired(t *testing.T) {
	x := r(1, "a")
	x.Archived = true
	c, st := setup(t, &fakeHub{perPage: 10, repos: []repo{x}})
	c.Sync(context.Background())
	if l := repos(t, st)[0].Lifecycle; l != model.LifeRetired {
		t.Fatalf("%s", l)
	}
}

func TestCIConclusionBecomesASignal(t *testing.T) {
	for concl, want := range map[string]string{"success": model.StateOK, "failure": model.StateFail, "cancelled": model.StateUnknown} {
		f := &fakeHub{perPage: 10, repos: []repo{r(1, "a")}, ciResult: concl}
		c, st := setup(t, f)
		ctx := context.Background()
		c.Sync(ctx)
		sigs, _ := st.AllSignals(ctx)
		got := ""
		for _, g := range sigs[repos(t, st)[0].ID] {
			if g.Name == "repo.ci" {
				got = g.State
			}
		}
		if got != want {
			t.Fatalf("%s => %s", concl, got)
		}
	}
}

func TestFailureIsVisibleAndCarriesNoToken(t *testing.T) {
	f := &fakeHub{perPage: 10, repos: []repo{r(1, "a")}}
	c, st := setup(t, f)
	ctx := context.Background()
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.status = 401
	if err := c.Sync(ctx); err == nil {
		t.Fatal("令牌失效应报错")
	}
	src, _ := st.ListSources(ctx)
	if len(src) != 1 || src[0].LastError == "" || src[0].LastErrorAt == nil {
		t.Fatalf("连接器故障要可见: %+v", src)
	}
	if strings.Contains(src[0].LastError, "secret-token-value") {
		t.Fatal("错误信息带出了令牌")
	}
	if n := len(repos(t, st)); n != 1 {
		t.Fatalf("来源故障不能清空本地数据: %d", n)
	}
}
