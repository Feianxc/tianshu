package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/catalog"
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// 这个文件里的每个测试都对应一次独立安全审查实测复现的缺陷。

func (f *fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) renameHost(name string) {
	f.t.Helper()
	_, err := f.st.PatchEntity(context.Background(), model.System(), f.id(model.KindDevice, "shared-host"), store.EntityPatch{Name: &name})
	f.must(err)
}

func TestFindingsNameOnlyTheirOwnSpace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.renameHost("机房一号")
	r := report("shared-host", time.Now(), 95<<30)
	r.Units = []model.ReportUnit{{Name: "a.service", Active: "failed", Sub: "failed"}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	f.must(f.eng.Evaluate(ctx))

	// 主机属于个人空间。只看个人空间的 Agent 不该读到公司的服务名。
	personal := f.token(model.Principal{Kind: "agent", Name: "p", Scopes: []string{"read"}, Workspaces: []string{"personal"}})
	_, b := f.do("GET", "/api/v1/overview", personal, nil)
	for _, bad := range []string{"甲服务", "乙绝密"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("告警带出了别的空间的名称 %s: %s", bad, b)
		}
	}
	if strings.Contains(string(b), "其他空间") {
		t.Fatalf("对 Agent 来说别的空间不存在，数量也不能有: %s", b)
	}
	if _, b := f.do("GET", "/api/v1/findings", f.owner, nil); !strings.Contains(string(b), "其他空间") {
		t.Fatalf("主机主人应该知道影响范围: %s", b)
	}
	// 服务属于公司甲。公司甲的人不该从故障文字里读到主机名。
	_, b = f.do("GET", "/api/v1/findings", f.human, nil)
	if !strings.Contains(string(b), "甲服务 故障") {
		t.Fatalf("公司甲应该看到自己的故障: %s", b)
	}
	if strings.Contains(string(b), "机房一号") {
		t.Fatalf("故障文字带出了主机名: %s", b)
	}
}

func TestFeedLostWitnessStaysInItsSpace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	real := f.st.Now
	old := time.Now().UTC().Add(-time.Hour)
	f.st.Now = func() time.Time { return old }
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", old, 10<<30))
	f.st.Now = real
	now := time.Now().UTC()
	f.must(f.st.PutSignal(ctx, model.Signal{EntityID: f.id(model.KindService, "svc-a"), Name: "check.http:https://a.example", State: model.StateOK, Source: "probe", ObservedAt: now, ReceivedAt: now, StaleAfterS: 300}))
	f.must(f.eng.Evaluate(ctx))
	fs, err := f.st.ListFindings(ctx, model.System(), store.FindingFilter{Open: true, EntityID: f.id(model.KindDevice, "shared-host")})
	f.must(err)
	s, _ := json.Marshal(fs)
	if !strings.Contains(string(s), "采集中断") || strings.Contains(string(s), "甲服务") {
		t.Fatalf("%s", s)
	}
}

func TestSignalsLoseTheirOriginAcrossWorkspaces(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.renameHost("机房一号")
	ws, err := f.st.WorkspaceIDsBySlug(ctx)
	f.must(err)
	repo, err := f.st.CreateEntity(ctx, model.Entity{Kind: model.KindRepository, WorkspaceID: ws["a"], Slug: "repo-a", Name: "甲仓库", Confirmed: true,
		Attrs: map[string]any{"url": "https://github.com/x/a"}})
	f.must(err)
	r := report("shared-host", time.Now(), 10<<30)
	r.Repos = []model.ReportRepo{{Path: "D:/私人目录/a", Remote: "https://github.com/x/a.git", Branch: "main", Ahead: 2, UserEmail: "me@private.example"}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	f.must(f.eng.Evaluate(ctx))

	// 个人设备上的公司仓库副本：公司的人看得到状态，看不到是哪台设备、什么路径。
	for _, path := range []string{"/api/v1/entities/" + repo.ID, "/api/v1/entities/" + f.id(model.KindService, "svc-a"), "/api/v1/findings"} {
		_, b := f.do("GET", path, f.human, nil)
		for _, bad := range []string{"shared-host", "机房一号", "私人目录", "me@private.example"} {
			if strings.Contains(string(b), bad) {
				t.Fatalf("%s 泄露 %s: %s", path, bad, b)
			}
		}
	}
	d := f.get("/api/v1/entities/"+repo.ID, f.human)
	if s, _ := json.Marshal(d["signals"]); !strings.Contains(string(s), `"ahead":2`) {
		t.Fatalf("状态本身应该保留: %s", s)
	}
	// 主人视角完整。
	if _, b := f.do("GET", "/api/v1/entities/"+repo.ID, f.owner, nil); !strings.Contains(string(b), "私人目录") {
		t.Fatalf("主人应看到路径: %s", b)
	}
}

func TestOneCompanyCannotWriteOntoAnothersRepository(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ws, err := f.st.WorkspaceIDsBySlug(ctx)
	f.must(err)
	repo, err := f.st.CreateEntity(ctx, model.Entity{Kind: model.KindRepository, WorkspaceID: ws["b"], Slug: "repo-b", Name: "乙仓库", Confirmed: true,
		Attrs: map[string]any{"url": "https://github.com/x/b"}})
	f.must(err)
	c := f.token(model.Principal{Kind: "collector", Name: "ca", Scopes: []string{"ingest"}, Devices: []string{"a-host"}})
	r := report("a-host", time.Now(), 10<<30)
	r.Repos = []model.ReportRepo{{Path: "/tmp/fake", Remote: "git@github.com:x/b.git", Dirty: 99}}
	if code, _ := f.do("POST", "/api/v1/ingest/report", c, r); code != 200 {
		t.Fatal(code)
	}
	sigs, err := f.st.AllSignals(ctx)
	f.must(err)
	if len(sigs[repo.ID]) != 0 {
		t.Fatalf("公司甲的采集器写到了公司乙的仓库: %+v", sigs[repo.ID])
	}
}

func TestSummaryIsAWhitelist(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := f.id(model.KindService, "svc-a")
	dev := f.id(model.KindDevice, "a-host")
	_, err := f.st.PatchEntity(ctx, model.System(), dev, store.EntityPatch{Attrs: map[string]any{"new_field_nobody_listed": "内部信息"}})
	f.must(err)
	_, err = f.st.UpsertCheck(ctx, model.Check{EntityID: svc, Kind: "http", Target: "https://internal.a.example/health", IntervalS: 60, TimeoutS: 5, Enabled: true})
	f.must(err)
	now := time.Now().UTC()
	f.must(f.st.PutSignal(ctx, model.Signal{EntityID: svc, Name: "check.http:https://internal.a.example/health", State: model.StateOK, Summary: "HTTP 200", Source: "probe:shared-host",
		Value: map[string]any{"url": "https://internal.a.example/health", "status": 200}, ObservedAt: now, ReceivedAt: now, StaleAfterS: 300}))

	for _, p := range []string{"/api/v1/entities/" + svc, "/api/v1/entities/" + dev, "/api/v1/entities?kind=service", "/api/v1/entities?kind=device", "/api/v1/overview"} {
		_, b := f.do("GET", p, f.agentA, nil)
		for _, bad := range []string{"internal.a.example", "a.example", "10.0.0.2", "内部信息", "a.service"} {
			if strings.Contains(string(b), bad) {
				t.Fatalf("%s 泄露 %s: %s", p, bad, b)
			}
		}
	}
	if d := f.get("/api/v1/entities/"+svc, f.agentA); len(d["checks"].([]any)) != 0 {
		t.Fatal("探测目标就是地址，不能给")
	}
	// 同一空间的人不受影响。
	if _, b := f.do("GET", "/api/v1/entities/"+svc, f.human, nil); !strings.Contains(string(b), "internal.a.example") {
		t.Fatalf("人的视图应完整: %s", b)
	}
}

func TestHiddenFieldsCannotBeGuessedThroughSearch(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"10.0.0.2", "10.0.0", "a-host"} {
		_, b := f.do("GET", "/api/v1/entities?kind=device&q="+q, f.agentA, nil)
		if q == "a-host" {
			continue // slug 本来就公开
		}
		if strings.Contains(string(b), "a-host") {
			t.Fatalf("Agent 用搜索确认了隐藏字段 %s: %s", q, b)
		}
	}
	_, b := f.do("GET", "/api/v1/entities?kind=device&q=10.0.0.2", f.human, nil)
	if !strings.Contains(string(b), "a-host") {
		t.Fatalf("人可以按地址搜索: %s", b)
	}
}

func TestEvidenceDetailsFollowThePolicy(t *testing.T) {
	f := newFixture(t)
	id := f.id(model.KindProject, "proj-a")
	if code, b := f.do("POST", "/api/v1/entities/"+id+"/evidence", f.owner, map[string]any{"verdict": "pass", "scope": "接口",
		"source_url": "https://internal.a.example/report", "note": "/srv/secret/path"}); code != 201 {
		t.Fatalf("%d %s", code, b)
	}
	_, b := f.do("GET", "/api/v1/entities/"+id, f.agentA, nil)
	if strings.Contains(string(b), "internal.a.example") || strings.Contains(string(b), "/srv/secret/path") {
		t.Fatalf("%s", b)
	}
}

func TestAgentCeilingHoldsOnEveryPath(t *testing.T) {
	f := newFixture(t)
	tok := f.token(model.Principal{Kind: "agent", Name: "cli-made", Scopes: model.AllScopes, Workspaces: []string{"a"}, Devices: []string{"*"}})
	id := f.id(model.KindDevice, "a-host")
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, tok, map[string]any{}); code != 403 {
		t.Fatalf("Agent 的 PATCH 得到 %d", code)
	}
	if code, _ := f.do("GET", "/api/v1/audit", tok, nil); code != 403 {
		t.Fatalf("得到 %d", code)
	}
	if code, _ := f.do("POST", "/api/v1/ingest/report", tok, report("a-host", time.Now(), 1)); code != 403 {
		t.Fatalf("得到 %d", code)
	}
	if code, _ := f.do("POST", "/api/v1/principals", f.owner, map[string]any{"kind": "agent", "name": "w", "scopes": []string{"read", "write"}, "workspaces": []string{"a"}}); code != 400 {
		t.Fatalf("得到 %d", code)
	}
}

func TestScopedAdminIsNotAGlobalAdmin(t *testing.T) {
	f := newFixture(t)
	tok := f.token(model.Principal{Kind: "human", Name: "只管甲", Scopes: model.AllScopes, Workspaces: []string{"a"}})
	for _, p := range []string{"/api/v1/principals", "/api/v1/audit", "/api/v1/sources", "/api/v1/discoveries"} {
		if code, _ := f.do("GET", p, tok, nil); code != 403 {
			t.Fatalf("%s 得到 %d", p, code)
		}
	}
	code, _ := f.do("POST", "/api/v1/principals", tok, map[string]any{"kind": "human", "name": "提权", "scopes": []string{"read"}, "workspaces": []string{"*"}})
	if code != 403 {
		t.Fatalf("空间管理员铸出全局令牌，得到 %d", code)
	}
	o := f.get("/api/v1/overview", tok)
	if len(o["sources"].([]any)) != 0 {
		t.Fatal("来源列表是全局信息")
	}
}

func TestSlugConflictRevealsNothing(t *testing.T) {
	f := newFixture(t)
	tok := f.token(model.Principal{Kind: "human", Name: "写甲", Scopes: []string{"read", "write"}, Workspaces: []string{"a"}})
	exists, _ := f.do("POST", "/api/v1/entities", tok, map[string]any{"kind": "project", "workspace": "a", "slug": "proj-b", "name": "x"})
	absent, _ := f.do("POST", "/api/v1/entities", tok, map[string]any{"kind": "project", "workspace": "a", "slug": "no-such", "name": "y"})
	if exists != absent {
		t.Fatalf("结果不同就暴露了对象是否存在: %d / %d", exists, absent)
	}
}

func dbText(t *testing.T, f *fixture) string {
	t.Helper()
	ctx := context.Background()
	sigs, err := f.st.AllSignals(ctx)
	f.must(err)
	src, err := f.st.ListSources(ctx)
	f.must(err)
	d, err := f.st.ListDiscoveries(ctx, "new")
	f.must(err)
	b, _ := json.Marshal([]any{sigs, src, d})
	return string(b)
}

func TestReportsCannotCarrySecretsIn(t *testing.T) {
	f := newFixture(t)
	tok := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	r := report("shared-host", time.Now(), 10<<30)
	r.Errors = []string{"git clone https://x:" + tok + "@github.com/a/b failed"}
	r.Host = &model.ReportHost{Hostname: tok}
	r.Repos = []model.ReportRepo{
		{Path: "/a", Branch: tok, Remote: "https://bob:pa#ss@github.com/x/y.git"},
		{Path: "/b", Remote: "https://bob:pa/ss@github.com/x/z.git", UserEmail: tok},
	}
	if code, b := f.do("POST", "/api/v1/ingest/report", f.owner, r); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	text := dbText(t, f)
	for _, bad := range []string{tok, "pa#ss", "pa/ss", "bob"} {
		if strings.Contains(text, bad) {
			t.Fatalf("秘密进了数据库 %q", bad)
		}
	}
}

func TestRemotesAreReducedToHostAndPath(t *testing.T) {
	cases := map[string]string{
		"https://github.com/a/b.git":             "github.com/a/b",
		"git@github.com:a/b.git":                 "github.com/a/b",
		"ssh://git@github.com:22/a/b.git":        "github.com/a/b",
		"https://u:p@github.com/a/b":             "github.com/a/b",
		"https://bob:pa#ss@github.com/A/B.git":   "github.com/a/b",
		"https://bob:pa/ss@github.com/a/b":       "",
		"https://github.com/a/b.git?token=abc#x": "github.com/a/b",
		"D:/local/path":                          "",
		"":                                       "",
	}
	for in, want := range cases {
		if got := store.NormalizeRemote(in); got != want {
			t.Errorf("%q => %q，应为 %q", in, got, want)
		}
	}
}

func TestGuardsCoverMoreShapesAndFields(t *testing.T) {
	f := newFixture(t)
	bad := []string{
		"wl_o_" + strings.Repeat("A", 43),
		"wl_r_" + strings.Repeat("b", 43),
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abcdefgh",
		"-----BEGIN PGP PRIVATE KEY BLOCK-----",
	}
	for _, v := range bad {
		if code, _ := f.do("POST", "/api/v1/entities", f.owner, map[string]any{"kind": "credential", "workspace": "personal", "slug": "x-tag", "name": "x", "tags": []string{v}}); code != 400 {
			t.Fatalf("标签 %s 得到 %d", v[:12], code)
		}
		if code, _ := f.do("POST", "/api/v1/entities", f.owner, map[string]any{"kind": "credential", "workspace": "personal", "slug": "x-val", "name": "x", "attrs": map[string]any{"note": v}}); code != 400 {
			t.Fatalf("属性 %s 得到 %d", v[:12], code)
		}
	}
	code, _ := f.do("POST", "/api/v1/relations", f.owner, map[string]any{"from_id": f.id(model.KindService, "svc-a"), "type": "uses", "to_id": f.id(model.KindCredential, "cred-a"), "note": bad[0]})
	if code != 400 {
		t.Fatalf("关系备注得到 %d", code)
	}
}

func TestUnknownCloudPolicyFailsClosed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := catalog.Import(ctx, f.st, &catalog.File{Workspaces: []catalog.YWorkspace{{Slug: "typo", Name: "t", Kind: "client", CloudPolicy: "Deny"}}}); err == nil {
		t.Fatal("拼错的策略应被拒绝")
	}
	id, err := f.st.UpsertWorkspace(ctx, model.Workspace{Slug: "typo", Name: "t", Kind: "client", CloudPolicy: "Deny"})
	f.must(err)
	_, err = f.st.CreateEntity(ctx, model.Entity{Kind: model.KindProject, WorkspaceID: id, Slug: "typo-p", Name: "隐藏项目"})
	f.must(err)
	tok := f.token(model.Principal{Kind: "agent", Name: "wide2", Scopes: []string{"read"}, Workspaces: []string{"*"}})
	if _, b := f.do("GET", "/api/v1/entities", tok, nil); strings.Contains(string(b), "隐藏项目") {
		t.Fatal("未知策略必须按禁止处理")
	}
}

func run(device, id string, seq int64, at time.Time) model.Report {
	r := report(device, at, 10<<30)
	r.RunID, r.Seq, r.SentAt = id, seq, at
	return r
}

func (f *fixture) heartbeat(device string) model.Signal {
	f.t.Helper()
	sigs, err := f.st.AllSignals(context.Background())
	f.must(err)
	for _, g := range sigs[f.id(model.KindDevice, device)] {
		if g.Name == "host.heartbeat" {
			return g
		}
	}
	f.t.Fatal("没有心跳")
	return model.Signal{}
}

func TestReplayCannotReviveASilentHost(t *testing.T) {
	f := newFixture(t)
	real := f.st.Now
	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	f.st.Now = func() time.Time { return old }
	captured := run("shared-host", "run-1", 1, old)
	f.do("POST", "/api/v1/ingest/report", f.owner, captured)
	f.st.Now = real
	f.do("POST", "/api/v1/ingest/report", f.owner, captured)
	if g := f.heartbeat("shared-host"); !g.Stale {
		t.Fatalf("重放让失联主机复活: %+v", g)
	}
	// 没有 run_id 的旧式报告同样不行。
	legacy := report("a-host", old, 10<<30)
	f.st.Now = func() time.Time { return old }
	f.do("POST", "/api/v1/ingest/report", f.owner, legacy)
	f.st.Now = real
	f.do("POST", "/api/v1/ingest/report", f.owner, legacy)
	if g := f.heartbeat("a-host"); !g.Stale {
		t.Fatal("重放旧式报告让主机复活")
	}
}

func TestOldRunCannotReturnAfterANewOne(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	f.do("POST", "/api/v1/ingest/report", f.owner, run("shared-host", "run-1", 5, now))
	f.do("POST", "/api/v1/ingest/report", f.owner, run("shared-host", "run-2", 1, now.Add(time.Second)))
	_, b := f.do("POST", "/api/v1/ingest/report", f.owner, run("shared-host", "run-1", 6, now.Add(2*time.Second)))
	if !strings.Contains(string(b), "dropped") {
		t.Fatalf("旧运行的报告应被丢弃: %s", b)
	}
}

func TestClockSetBackDoesNotSilenceAHost(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	f.do("POST", "/api/v1/ingest/report", f.owner, run("shared-host", "run-1", 1, now))
	back := now.Add(-24 * time.Hour)
	r := run("shared-host", "run-1", 2, back)
	r.Disks = []model.ReportDisk{{Mount: "/", Used: 50 << 30, Total: 100 << 30}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	g := f.heartbeat("shared-host")
	if g.Stale || g.ObservedAt.Before(now.Add(-time.Minute)) {
		t.Fatalf("时钟落后的主机仍在上报，不能算失联: %+v", g)
	}
	if g.Value["clock_skew"] != true {
		t.Fatalf("时钟异常要标出来: %+v", g.Value)
	}
	sigs, _ := f.st.AllSignals(context.Background())
	for _, x := range sigs[f.id(model.KindDevice, "shared-host")] {
		if x.Name == "host.disk:/" && x.Value["pct"].(float64) != 50 {
			t.Fatalf("新数据应写入: %v", x.Value)
		}
	}
}

func TestLateReportLeavesEverythingAlone(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	r := report("shared-host", now, 10<<30)
	r.Disks = append(r.Disks, model.ReportDisk{Mount: "/data", Used: 1 << 30, Total: 100 << 30})
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	late := report("shared-host", now.Add(-time.Hour), 10<<30)
	_, b := f.do("POST", "/api/v1/ingest/report", f.owner, late)
	if !strings.Contains(string(b), "dropped") {
		t.Fatalf("%s", b)
	}
	sigs, _ := f.st.AllSignals(context.Background())
	found := false
	for _, g := range sigs[f.id(model.KindDevice, "shared-host")] {
		found = found || g.Name == "host.disk:/data"
	}
	if !found {
		t.Fatal("迟到的报告删掉了新磁盘信号")
	}
}

func TestIntervalCannotPostponeExpiry(t *testing.T) {
	f := newFixture(t)
	r := report("shared-host", time.Now(), 10<<30)
	r.IntervalS = 1e9
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	if g := f.heartbeat("shared-host"); g.StaleAfterS > 3*86400 {
		t.Fatalf("上报周期不能把过期时间推到一年后: %d", g.StaleAfterS)
	}
}

func TestServiceOnTwoHostsKeepsBothResults(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := f.id(model.KindService, "svc-a")
	f.must(f.st.AddRelation(ctx, model.Relation{FromID: svc, Type: model.RelRunsOn, ToID: f.id(model.KindDevice, "a-host")}))
	bad := report("shared-host", time.Now(), 10<<30)
	bad.Units = []model.ReportUnit{{Name: "a.service", Active: "failed", Sub: "failed"}}
	good := report("a-host", time.Now(), 10<<30)
	good.Units = []model.ReportUnit{{Name: "a.service", Active: "active", Sub: "running"}}
	f.do("POST", "/api/v1/ingest/report", f.owner, bad)
	f.do("POST", "/api/v1/ingest/report", f.owner, good)
	if s := f.get("/api/v1/entities/"+svc, f.owner)["health"].(map[string]any)["state"]; s != "down" {
		t.Fatalf("一台主机上的故障被另一台的正常盖住了: %v", s)
	}
}

func TestWorstDiskDecidesTheFinding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := report("shared-host", time.Now(), 10<<30)
	r.Disks = []model.ReportDisk{{Mount: "/a", Used: 99 << 30, Total: 100 << 30}, {Mount: "/b", Used: 88 << 30, Total: 100 << 30}, {Mount: "/c", Used: 86 << 30, Total: 100 << 30}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	for range 3 {
		f.must(f.eng.Evaluate(ctx))
		fs, err := f.st.ListFindings(ctx, model.System(), store.FindingFilter{Open: true, EntityID: f.id(model.KindDevice, "shared-host")})
		f.must(err)
		for _, x := range fs {
			if x.Rule == "host.disk" && (x.Severity != model.SevCritical || !strings.Contains(x.Title, "99%")) {
				t.Fatalf("%+v", x)
			}
		}
	}
}
