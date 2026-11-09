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

// 第二轮独立复测发现的问题，包括第一轮修复引入的回归。

func TestPatchTagsCannotCarrySecretsOrLockAnEntity(t *testing.T) {
	f := newFixture(t)
	id := f.id(model.KindProject, "proj-a")
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, f.owner, map[string]any{"tags": []string{"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}); code != 400 {
		t.Fatalf("PATCH 标签写入秘密得到 %d", code)
	}
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, f.owner, map[string]any{"tags": []string{"正常"}}); code != 200 {
		t.Fatalf("正常标签得到 %d", code)
	}
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, f.owner, map[string]any{"tags": []string{}}); code != 200 {
		t.Fatalf("清空标签得到 %d", code)
	}
}

func TestRemoteTokensInQueryAndPathAreDropped(t *testing.T) {
	tok := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	cases := map[string]string{
		"git@github.com:a/b.git?token=abc":     "github.com/a/b",
		"github.com:a/b#frag":                  "github.com/a/b",
		"https://github.com/" + tok + "/b.git": "",
		"git@github.com:" + tok + "/b.git":     "",
	}
	for in, want := range cases {
		if got := store.NormalizeRemote(in); got != want {
			t.Errorf("%q => %q，应为 %q", in[:20], got, want)
		}
	}
}

func TestRunIDIsNotAPlaceToSmuggleText(t *testing.T) {
	f := newFixture(t)
	r := report("shared-host", time.Now(), 10<<30)
	r.RunID = "ghp_abcdefghijklmnopqrstuvwxyz0123456789 with spaces"
	r.Seq = 1
	if code, _ := f.do("POST", "/api/v1/ingest/report", f.owner, r); code != 200 {
		t.Fatal(code)
	}
	if strings.Contains(dbText(t, f), "ghp_") {
		t.Fatal("run_id 把秘密带进了数据库")
	}
}

func TestYAMLCannotHideSecretsBehindOddKeys(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	file := &catalog.File{Devices: []catalog.YEntity{{Slug: "odd", Workspace: "personal", Name: "x",
		Attrs: map[string]any{"nested": map[any]any{1: "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}}}}
	if _, err := catalog.Import(ctx, f.st, file); err == nil {
		t.Fatal("非字符串键应被拒绝")
	}
	file = &catalog.File{Services: []catalog.YEntity{{Slug: "svc-x", Workspace: "personal", Name: "x",
		Checks: []catalog.YCheck{{Kind: "http", Target: "https://x.example", ExpectBody: "ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}}}}
	if _, err := catalog.Import(ctx, f.st, file); err == nil {
		t.Fatal("expect_body 里的秘密应被拒绝")
	}
}

func TestEscalationEndsASnooze(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	warn := report("shared-host", time.Now(), 88<<30)
	f.do("POST", "/api/v1/ingest/report", f.owner, warn)
	f.must(f.eng.Evaluate(ctx))
	id := f.id(model.KindDevice, "shared-host")
	list, err := f.st.ListFindings(ctx, model.System(), store.FindingFilter{Open: true, EntityID: id})
	f.must(err)
	var fid string
	for _, x := range list {
		if x.Rule == "host.disk" {
			fid = x.ID
		}
	}
	if fid == "" {
		t.Fatal("应有磁盘告警")
	}
	if code, _ := f.do("POST", "/api/v1/findings/"+fid+"/ack", f.owner, map[string]any{"snooze_hours": 24}); code != 200 {
		t.Fatal(code)
	}
	if s, _ := json.Marshal(f.get("/api/v1/overview", f.owner)["attention"]); strings.Contains(string(s), "磁盘") {
		t.Fatalf("稍后提醒期间应隐藏: %s", s)
	}
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now().Add(time.Second), 99<<30))
	f.must(f.eng.Evaluate(ctx))
	if s, _ := json.Marshal(f.get("/api/v1/overview", f.owner)["attention"]); !strings.Contains(string(s), "磁盘") {
		t.Fatalf("升级后必须重新出现: %s", s)
	}
}

func TestTightestCertificateDecidesTheFinding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := f.id(model.KindService, "svc-a")
	now := time.Now().UTC()
	for name, days := range map[string]float64{"check.tls:a.example:443": 3, "check.tls:b.example:443": 25, "check.tls:c.example:443": 20} {
		f.must(f.st.PutSignal(ctx, model.Signal{EntityID: svc, Name: name, State: model.StateOK, Source: "probe", Value: map[string]any{"days_left": days}, ObservedAt: now, ReceivedAt: now, StaleAfterS: 3600}))
	}
	for range 3 {
		f.must(f.eng.Evaluate(ctx))
		fs, err := f.st.ListFindings(ctx, model.System(), store.FindingFilter{Open: true, EntityID: svc})
		f.must(err)
		seen := false
		for _, x := range fs {
			if x.Rule == "tls.expiring" {
				seen = true
				if x.Severity != model.SevCritical || !strings.Contains(x.Title, "3 天") {
					t.Fatalf("%+v", x)
				}
			}
		}
		if !seen {
			t.Fatal("应有证书告警")
		}
	}
}

func TestKindSpellingCannotEscapeTheCeiling(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.st.CreatePrincipal(ctx, model.Principal{Kind: "Agent", Name: "odd", Scopes: model.AllScopes, Workspaces: []string{"*"}}); err == nil {
		t.Fatal("未知身份类型应被拒绝")
	}
	// 即使库里已经有一条奇怪的记录，它也只能是最受限的 Agent。
	v, err := f.st.ViewerFor(ctx, model.Principal{Kind: "Agent", Name: "x", Scopes: model.AllScopes, Workspaces: []string{"*"}})
	f.must(err)
	if v.Has(model.ScopeAdmin) || v.Has(model.ScopeWrite) || v.Has(model.ScopeIngest) || v.All {
		t.Fatalf("%+v", v)
	}
}

func TestSlugsWithUnderscoresDoNotCollide(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ws, err := f.st.WorkspaceIDsBySlug(ctx)
	f.must(err)
	var ids = map[string]string{}
	for _, slug := range []string{"web-1", "web_1"} {
		d, err := f.st.CreateEntity(ctx, model.Entity{Kind: model.KindDevice, WorkspaceID: ws["personal"], Slug: slug, Name: slug, Attrs: map[string]any{"availability": "always_on"}})
		f.must(err)
		ids[slug] = d.ID
	}
	svc, err := f.st.CreateEntity(ctx, model.Entity{Kind: model.KindService, WorkspaceID: ws["personal"], Slug: "both", Name: "both", Attrs: map[string]any{"unit": "x.service"}})
	f.must(err)
	for _, d := range ids {
		f.must(f.st.AddRelation(ctx, model.Relation{FromID: svc.ID, Type: model.RelRunsOn, ToID: d}))
	}
	bad := report("web-1", time.Now(), 10<<30)
	bad.Units = []model.ReportUnit{{Name: "x.service", Active: "failed", Sub: "failed"}}
	good := report("web_1", time.Now(), 10<<30)
	good.Units = []model.ReportUnit{{Name: "x.service", Active: "active", Sub: "running"}}
	f.do("POST", "/api/v1/ingest/report", f.owner, bad)
	f.do("POST", "/api/v1/ingest/report", f.owner, good)
	if s := f.get("/api/v1/entities/"+svc.ID, f.owner)["health"].(map[string]any)["state"]; s != "down" {
		t.Fatalf("web_1 的上报删掉了 web-1 的故障: %v", s)
	}
}

func TestMovedServiceDropsOldHostSignals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := f.id(model.KindService, "svc-a")
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 10<<30))
	f.must(f.st.RemoveRelation(ctx, svc, model.RelRunsOn, f.id(model.KindDevice, "shared-host")))
	f.must(f.st.AddRelation(ctx, model.Relation{FromID: svc, Type: model.RelRunsOn, ToID: f.id(model.KindDevice, "a-host")}))
	f.must(f.st.Prune(ctx))
	sigs, err := f.st.AllSignals(ctx)
	f.must(err)
	for _, g := range sigs[svc] {
		if strings.HasPrefix(g.Name, "unit:shared-host:") {
			t.Fatalf("换主机后旧信号还在: %s", g.Name)
		}
	}
}

func TestRenamedDeviceNameDoesNotSurviveInSignals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ws, err := f.st.WorkspaceIDsBySlug(ctx)
	f.must(err)
	repo, err := f.st.CreateEntity(ctx, model.Entity{Kind: model.KindRepository, WorkspaceID: ws["a"], Slug: "repo-a2", Name: "甲仓库", Attrs: map[string]any{"url": "https://github.com/x/a2"}})
	f.must(err)
	f.renameHost("旧名字主机")
	r := report("shared-host", time.Now(), 10<<30)
	r.Repos = []model.ReportRepo{{Path: "/p", Remote: "https://github.com/x/a2.git", Dirty: 1}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	f.renameHost("新名字主机")
	if _, b := f.do("GET", "/api/v1/entities/"+repo.ID, f.human, nil); strings.Contains(string(b), "旧名字主机") {
		t.Fatalf("%s", b)
	}
}

func TestAgentGetsNoTraceOfDeniedObjects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 甲的服务依赖乙（deny）的服务。
	f.must(f.st.AddRelation(ctx, model.Relation{FromID: f.id(model.KindService, "svc-a"), Type: model.RelDependsOn, ToID: f.id(model.KindService, "svc-b")}))
	d := f.get("/api/v1/entities/"+f.id(model.KindService, "svc-a"), f.agentA)
	s, _ := json.Marshal(d["relations"])
	if strings.Contains(string(s), "restricted") && strings.Contains(string(s), `"restricted":true`) {
		t.Fatalf("Agent 不应看到范围外对象的任何痕迹: %s", s)
	}
	// 人仍然看到受限占位，知道依赖有问题。
	if s, _ := json.Marshal(f.get("/api/v1/entities/"+f.id(model.KindService, "svc-a"), f.human)["relations"]); !strings.Contains(string(s), `"restricted":true`) {
		t.Fatalf("%s", s)
	}
}

func TestFailedUnitNamesStayWithTheHostOwner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := report("shared-host", time.Now(), 10<<30)
	r.Units = append(r.Units, model.ReportUnit{Name: "secret-client.service", Active: "failed", Sub: "failed"})
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	f.must(f.eng.Evaluate(ctx))
	agent := f.token(model.Principal{Kind: "agent", Name: "pa", Scopes: []string{"read"}, Workspaces: []string{"personal"}})
	id := f.id(model.KindDevice, "shared-host")
	for _, p := range []string{"/api/v1/overview", "/api/v1/entities/" + id, "/api/v1/findings"} {
		if _, b := f.do("GET", p, agent, nil); strings.Contains(string(b), "secret-client") {
			t.Fatalf("%s 把别的租户的单元名给了 Agent: %s", p, b)
		}
	}
	if _, b := f.do("GET", "/api/v1/entities/"+id, f.owner, nil); !strings.Contains(string(b), "secret-client") {
		t.Fatal("主机主人应能看到")
	}
}

// 第三轮复测发现的问题。

func TestAgentCountsExcludeWhatItCannotSee(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	agent := f.token(model.Principal{Kind: "agent", Name: "pa2", Scopes: []string{"read"}, Workspaces: []string{"personal"}})
	d := f.get("/api/v1/entities/"+f.id(model.KindDevice, "shared-host"), agent)
	if n := d["device"].(map[string]any)["services"].(float64); n != 0 {
		t.Fatalf("Agent 看到了 %v 个范围外的服务", n)
	}
	if n := f.get("/api/v1/entities/"+f.id(model.KindDevice, "shared-host"), f.owner)["device"].(map[string]any)["services"].(float64); n != 2 {
		t.Fatalf("主人应看到全部 2 个，得到 %v", n)
	}
	// 项目只统计自己空间的服务。
	f.must(f.st.AddRelation(ctx, model.Relation{FromID: f.id(model.KindService, "svc-b"), Type: model.RelPartOf, ToID: f.id(model.KindProject, "proj-a")}))
	r := report("shared-host", time.Now(), 10<<30)
	r.Units = []model.ReportUnit{{Name: "a.service", Active: "active", Sub: "running"}, {Name: "b.service", Active: "failed", Sub: "failed"}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	d = f.get("/api/v1/entities/"+f.id(model.KindProject, "proj-a"), f.agentA)
	if n := d["project"].(map[string]any)["services"].(float64); n != 1 {
		t.Fatalf("项目数到了别的空间的服务: %v", n)
	}
	if s := d["health"].(map[string]any)["state"]; s != "ok" {
		t.Fatalf("别的空间的故障从项目健康透了过来: %v", s)
	}
}

func TestDisabledCheckStopsSpeakingForTheService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	svc := f.id(model.KindService, "svc-a")
	c := model.Check{EntityID: svc, Kind: "http", Target: "https://a.example/x", IntervalS: 60, TimeoutS: 5, Enabled: true}
	_, err := f.st.UpsertCheck(ctx, c)
	f.must(err)
	now := time.Now().UTC()
	f.must(f.st.PutSignal(ctx, model.Signal{EntityID: svc, Name: "check.http:https://a.example/x", State: model.StateFail, Source: "probe", ObservedAt: now, ReceivedAt: now, StaleAfterS: 60}))
	f.must(f.st.Prune(ctx))
	sigs, err := f.st.AllSignals(ctx)
	f.must(err)
	if len(sigs[svc]) != 1 {
		t.Fatal("启用中的探测信号不能被清掉")
	}
	c.Enabled = false
	_, err = f.st.UpsertCheck(ctx, c)
	f.must(err)
	f.must(f.st.Prune(ctx))
	if sigs, _ = f.st.AllSignals(ctx); len(sigs[svc]) != 0 {
		t.Fatalf("停用探测后信号要清除: %+v", sigs[svc])
	}
}
