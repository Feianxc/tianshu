package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Feianxc/tianshu/server/internal/api"
	"github.com/Feianxc/tianshu/server/internal/catalog"
	"github.com/Feianxc/tianshu/server/internal/engine"
	"github.com/Feianxc/tianshu/server/internal/mcpsrv"
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/probe"
	"github.com/Feianxc/tianshu/server/internal/store"
	"github.com/Feianxc/tianshu/server/internal/view"
)

// fixture builds the shape that makes isolation hard: two companies whose services
// share one physical host owned by a third space.
type fixture struct {
	t      *testing.T
	st     *store.Store
	ts     *httptest.Server
	owner  string
	agentA string
	human  string
	eng    *engine.Engine
}

const fixtureYAML = `
workspaces:
  - {slug: personal, name: 个人, kind: personal, cloud_policy: allow}
  - {slug: a, name: 公司甲, kind: company, cloud_policy: summary}
  - {slug: b, name: 公司乙, kind: company, cloud_policy: deny}
devices:
  - slug: shared-host
    workspace: personal
    name: 共享主机
    attrs: {device_type: server, availability: always_on, address: 10.0.0.1}
  - slug: a-host
    workspace: a
    name: 甲专用主机
    attrs: {device_type: server, availability: always_on, address: 10.0.0.2, ssh_alias: a-host}
projects:
  - {slug: proj-a, workspace: a, name: 甲项目, attrs: {stage: building, next_step: 继续}}
  - {slug: proj-b, workspace: b, name: 乙绝密项目, attrs: {stage: building}}
  - {slug: idea-only, workspace: personal, name: 只是个想法, attrs: {stage: idea}}
credentials:
  - {slug: cred-a, workspace: a, name: 甲的凭据, attrs: {provider: mixia, locator: mixia://v/1, mode: 代理, usage: 查询}}
services:
  - {slug: svc-a, workspace: a, name: 甲服务, runs_on: [shared-host], project: [proj-a], uses: [cred-a], attrs: {unit: a.service, url: "https://a.example"}}
  - {slug: svc-b, workspace: b, name: 乙绝密服务, runs_on: [shared-host], project: [proj-b], attrs: {unit: b.service}}
`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, dir+"/c.yaml", fixtureYAML)
	f, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Import(ctx, st, f); err != nil {
		t.Fatal(err)
	}
	vw := &view.Service{Store: st}
	srv := api.New(api.Config{Store: st, View: vw, MCP: mcpsrv.Handler(st, vw, "", "", false)})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	fx := &fixture{t: t, st: st, ts: ts, eng: &engine.Engine{Store: st, View: vw, Probe: &probe.Scheduler{Store: st, Vantage: "test"}}}
	fx.owner = fx.token(model.Principal{Kind: "human", Name: "owner", Scopes: model.AllScopes, Workspaces: []string{"*"}, Devices: []string{"*"}})
	fx.human = fx.token(model.Principal{Kind: "human", Name: "只看甲", Scopes: []string{"read"}, Workspaces: []string{"a"}})
	fx.agentA = fx.token(model.Principal{Kind: "agent", Name: "agent-a", Scopes: []string{"read"}, Workspaces: []string{"a"}})
	return fx
}

func (f *fixture) token(p model.Principal) string {
	f.t.Helper()
	_, tok, err := f.st.CreatePrincipal(context.Background(), p)
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fixture) do(method, path, tok string, body any) (int, []byte) {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.ts.URL+path, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (f *fixture) get(path, tok string) map[string]any {
	f.t.Helper()
	code, b := f.do("GET", path, tok, nil)
	if code != 200 {
		f.t.Fatalf("GET %s: %d %s", path, code, b)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) id(kind, slug string) string {
	f.t.Helper()
	e, err := f.st.GetEntityBySlug(context.Background(), model.System(), kind, slug)
	if err != nil {
		f.t.Fatal(err)
	}
	return e.ID
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := osWrite(path, s); err != nil {
		t.Fatal(err)
	}
}

// A01: 身份决定可见范围，客户端传什么都不算数。
func TestIsolationAcrossEveryReadPath(t *testing.T) {
	f := newFixture(t)
	secret := "乙绝密"
	idB := f.id(model.KindProject, "proj-b")
	for _, tok := range []string{f.human, f.agentA} {
		paths := []string{"/api/v1/overview", "/api/v1/entities", "/api/v1/entities?q=绝密", "/api/v1/entities?workspace=b",
			"/api/v1/overview?workspace=b", "/api/v1/findings", "/api/v1/workspaces"}
		for _, p := range paths {
			code, b := f.do("GET", p, tok, nil)
			if code != 200 {
				t.Fatalf("%s: %d", p, code)
			}
			if strings.Contains(string(b), secret) {
				t.Fatalf("%s 泄露了另一公司的数据: %s", p, b)
			}
		}
		if code, _ := f.do("GET", "/api/v1/entities/"+idB, tok, nil); code != 404 {
			t.Fatalf("跨空间读取应为 404，得到 %d", code)
		}
		if code, _ := f.do("GET", "/api/v1/entities/"+idB+"/metrics", tok, nil); code != 404 {
			t.Fatalf("跨空间指标应为 404，得到 %d", code)
		}
	}
}

// 共享主机：能知道自己的服务跑在一台主机上，但不能知道是哪台、还有谁。
func TestSharedHostIsProjectedNotExposed(t *testing.T) {
	f := newFixture(t)
	d := f.get("/api/v1/entities/"+f.id(model.KindService, "svc-a"), f.human)
	s, _ := json.Marshal(d["relations"])
	if !strings.Contains(string(s), `"restricted":true`) || strings.Contains(string(s), "共享主机") || strings.Contains(string(s), "shared-host") {
		t.Fatalf("relations: %s", s)
	}
	// 主人视角下同一条关系是完整的。
	d = f.get("/api/v1/entities/"+f.id(model.KindService, "svc-a"), f.owner)
	s, _ = json.Marshal(d["relations"])
	if !strings.Contains(string(s), "shared-host") {
		t.Fatalf("relations: %s", s)
	}
}

func TestCloudPolicyAppliesToAgents(t *testing.T) {
	f := newFixture(t)
	all := f.token(model.Principal{Kind: "agent", Name: "wide", Scopes: []string{"read"}, Workspaces: []string{"*"}})
	code, b := f.do("GET", "/api/v1/entities?kind=project", all, nil)
	if code != 200 || strings.Contains(string(b), "绝密") {
		t.Fatalf("deny 空间对 Agent 必须不存在，即使令牌写了 *: %d %s", code, b)
	}
	if !strings.Contains(string(b), "只是个想法") {
		t.Fatalf("allow 空间应可见: %s", b)
	}
	// summary 空间：能看到对象，看不到地址。
	id := f.id(model.KindDevice, "a-host")
	d := f.get("/api/v1/entities/"+id, f.agentA)
	attrs := d["attrs"].(map[string]any)
	if _, ok := attrs["address"]; ok {
		t.Fatalf("summary 空间不应给 Agent 地址: %v", attrs)
	}
	if _, ok := attrs["ssh_alias"]; ok {
		t.Fatalf("summary 空间不应给 Agent SSH 别名: %v", attrs)
	}
	// 同一空间内的人可以看到。
	h := f.token(model.Principal{Kind: "human", Name: "h2", Scopes: []string{"read"}, Workspaces: []string{"a"}})
	if _, ok := f.get("/api/v1/entities/"+id, h)["attrs"].(map[string]any)["address"]; !ok {
		t.Fatal("人的视图不受云端策略影响")
	}
}

// A07: 0、1、N 条结果形态一致，分页完整。
func TestCollectionsAreAlwaysArraysAndPagesComplete(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"kind=credential&workspace=personal", "kind=credential&workspace=a", "kind=project"} {
		m := f.get("/api/v1/entities?"+q, f.owner)
		if _, ok := m["items"].([]any); !ok {
			t.Fatalf("%s items 不是数组: %v", q, m["items"])
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 10; i++ {
		m := f.get("/api/v1/entities?limit=2&cursor="+cursor, f.owner)
		for _, it := range m["items"].([]any) {
			id := it.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("分页重复 %s", id)
			}
			seen[id] = true
		}
		if m["has_more"] == false {
			break
		}
		cursor = m["next_cursor"].(string)
	}
	all, _ := f.st.AllEntities(context.Background())
	if len(seen) != len(all) {
		t.Fatalf("分页丢数据: %d / %d", len(seen), len(all))
	}
}

func f64(v float64) *float64 { return &v }

func report(device string, at time.Time, disk float64) model.Report {
	return model.Report{Device: device, Collector: "agent", IntervalS: 30, ObservedAt: at,
		Metrics: &model.ReportMetric{CPUPct: f64(10)}, Disks: []model.ReportDisk{{Mount: "/", Used: disk, Total: 100 << 30}},
		SystemdState: "running", Units: []model.ReportUnit{{Name: "a.service", Active: "active", Sub: "running"}, {Name: "my-new.service", Active: "active", Sub: "running"}}}
}

func TestCollectorCanOnlyReportForItsDevices(t *testing.T) {
	f := newFixture(t)
	c := f.token(model.Principal{Kind: "collector", Name: "c", Scopes: []string{"ingest"}, Devices: []string{"shared-host"}})
	if code, _ := f.do("POST", "/api/v1/ingest/report", c, report("shared-host", time.Now(), 10<<30)); code != 200 {
		t.Fatalf("自己的设备: %d", code)
	}
	if code, _ := f.do("POST", "/api/v1/ingest/report", c, report("a-host", time.Now(), 10<<30)); code != 403 {
		t.Fatalf("别的设备应 403，得到 %d", code)
	}
	if code, _ := f.do("GET", "/api/v1/overview", c, nil); code != 403 {
		t.Fatalf("采集身份不能读数据，得到 %d", code)
	}
}

// A07: 乱序和时钟错误不能让状态回退。
func TestLateReportDoesNotRollStateBack(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", now, 90<<30))
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", now.Add(-time.Hour), 10<<30))
	sigs, _ := f.st.AllSignals(context.Background())
	for _, g := range sigs[f.id(model.KindDevice, "shared-host")] {
		if g.Name == "host.disk:/" && g.Value["pct"].(float64) < 80 {
			t.Fatalf("旧报告覆盖了新状态: %v", g.Value)
		}
	}
}

func TestClockInTheFutureCannotShadowHonestReports(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", now.Add(24*time.Hour), 90<<30))
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", now.Add(time.Second), 10<<30))
	sigs, _ := f.st.AllSignals(context.Background())
	for _, g := range sigs[f.id(model.KindDevice, "shared-host")] {
		if g.Name == "host.disk:/" && g.Value["pct"].(float64) > 80 {
			t.Fatalf("时钟超前的设备挡住了后续报告: %v", g.Value)
		}
		if g.Name == "host.disk:/" && g.ObservedAt.After(now.Add(5*time.Minute)) {
			t.Fatalf("未来时间写进了状态: %v", g.ObservedAt)
		}
	}
}

func TestUnitMappingAndDiscoveryInbox(t *testing.T) {
	f := newFixture(t)
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 10<<30))
	d := f.get("/api/v1/entities/"+f.id(model.KindService, "svc-a"), f.owner)
	if d["health"].(map[string]any)["state"] != "ok" {
		t.Fatalf("主机上报应映射到服务: %v", d["health"])
	}
	// 目录声明了 b.service，主机上却没有：这本身是一条事实。
	d = f.get("/api/v1/entities/"+f.id(model.KindService, "svc-b"), f.owner)
	if d["health"].(map[string]any)["state"] != "down" {
		t.Fatalf("声明的单元不存在应显示故障: %v", d["health"])
	}
	// 主机上有目录不知道的服务：进收件箱，不自动收录。
	m := f.get("/api/v1/discoveries", f.owner)
	if s, _ := json.Marshal(m["items"]); !strings.Contains(string(s), "my-new.service") {
		t.Fatalf("items: %s", s)
	}
	all, _ := f.st.AllEntities(context.Background())
	for _, e := range all {
		if strings.Contains(e.Name, "my-new") {
			t.Fatal("新发现的服务不能自动进目录")
		}
	}
}

func TestRenamedUnitStopsReportingTheOldFault(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.id(model.KindService, "svc-b")
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 10<<30))
	if s := f.get("/api/v1/entities/"+id, f.owner)["health"].(map[string]any)["state"]; s != "down" {
		t.Fatalf("声明的单元不存在应故障，得到 %v", s)
	}
	// 目录改成主机上真实存在的单元名后，旧信号不能继续说话。
	if _, err := f.st.PatchEntity(ctx, model.System(), id, store.EntityPatch{Attrs: map[string]any{"unit": "my-new.service"}}); err != nil {
		t.Fatal(err)
	}
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now().Add(time.Second), 10<<30))
	if s := f.get("/api/v1/entities/"+id, f.owner)["health"].(map[string]any)["state"]; s != "ok" {
		t.Fatalf("改名后应恢复正常，得到 %v", s)
	}
}

func TestInboxClearsOnceTheCatalogCatchesUp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 10<<30))
	if s, _ := json.Marshal(f.get("/api/v1/discoveries", f.owner)["items"]); !strings.Contains(string(s), "my-new.service") {
		t.Fatalf("items: %s", s)
	}
	if _, err := f.st.PatchEntity(ctx, model.System(), f.id(model.KindService, "svc-b"), store.EntityPatch{Attrs: map[string]any{"unit": "my-new.service"}}); err != nil {
		t.Fatal(err)
	}
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now().Add(time.Second), 10<<30))
	if s, _ := json.Marshal(f.get("/api/v1/discoveries", f.owner)["items"]); strings.Contains(string(s), "my-new.service") {
		t.Fatalf("目录已有的单元不该留在收件箱: %s", s)
	}
}

func TestSecretsAreRefused(t *testing.T) {
	f := newFixture(t)
	bad := []map[string]any{
		{"password": "hunter2"},
		{"api_key": "abc"},
		{"note": "-----BEGIN OPENSSH PRIVATE KEY-----"},
		{"note": "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"url": "https://user:pw@example.com/x"},
		{"nested": map[string]any{"token": "x"}},
		{"list": []any{"sk-abcdefghijklmnopqrstuvwxyz012345"}},
	}
	for _, attrs := range bad {
		code, b := f.do("POST", "/api/v1/entities", f.owner, map[string]any{"kind": "credential", "workspace": "personal", "slug": "x-1", "name": "x", "attrs": attrs})
		if code != 400 {
			t.Fatalf("%v 应被拒绝，得到 %d %s", attrs, code, b)
		}
	}
	id := f.id(model.KindCredential, "cred-a")
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, f.owner, map[string]any{"attrs": map[string]any{"password": "hunter2"}}); code != 400 {
		t.Fatalf("PATCH 也要拒绝，得到 %d", code)
	}
	// 引用字段是允许的。
	if code, b := f.do("POST", "/api/v1/entities", f.owner, map[string]any{"kind": "credential", "workspace": "personal", "slug": "ok-1", "name": "ok", "attrs": map[string]any{"token_ref": "mixia://v/2"}}); code != 201 {
		t.Fatalf("引用应通过: %d %s", code, b)
	}
}

func TestRevokedAndExpiredTokensStopWorking(t *testing.T) {
	f := newFixture(t)
	past := time.Now().Add(-time.Hour)
	exp := f.token(model.Principal{Kind: "agent", Name: "exp", Scopes: []string{"read"}, Workspaces: []string{"a"}, ExpiresAt: &past})
	if code, _ := f.do("GET", "/api/v1/overview", exp, nil); code != 401 {
		t.Fatalf("过期令牌得到 %d", code)
	}
	p, tok, _ := f.st.CreatePrincipal(context.Background(), model.Principal{Kind: "agent", Name: "rev", Scopes: []string{"read"}, Workspaces: []string{"a"}})
	if code, _ := f.do("GET", "/api/v1/overview", tok, nil); code != 200 {
		t.Fatalf("有效令牌得到 %d", code)
	}
	if code, _ := f.do("DELETE", "/api/v1/principals/"+p.ID, f.owner, nil); code != 200 {
		t.Fatalf("吊销得到 %d", code)
	}
	if code, _ := f.do("GET", "/api/v1/overview", tok, nil); code != 401 {
		t.Fatalf("吊销后得到 %d", code)
	}
}

func TestAgentsCannotHoldAdminPower(t *testing.T) {
	f := newFixture(t)
	code, _ := f.do("POST", "/api/v1/principals", f.owner, map[string]any{"kind": "agent", "name": "evil", "scopes": []string{"read", "admin"}, "workspaces": []string{"*"}})
	if code != 400 {
		t.Fatalf("Agent 不能持有 admin，得到 %d", code)
	}
	for _, p := range []string{"/api/v1/principals", "/api/v1/audit", "/api/v1/export", "/api/v1/discoveries", "/api/v1/sources"} {
		if code, _ := f.do("GET", p, f.agentA, nil); code != 403 {
			t.Fatalf("%s 得到 %d", p, code)
		}
	}
}

func TestSessionWritesNeedTheAntiForgeryHeader(t *testing.T) {
	f := newFixture(t)
	code, _ := f.do("POST", "/api/v1/session", "", map[string]any{"token": f.agentA})
	if code != 401 {
		t.Fatalf("Agent 令牌不能开浏览器会话，得到 %d", code)
	}
	body, _ := json.Marshal(map[string]any{"token": f.owner})
	resp, err := http.Post(f.ts.URL+"/api/v1/session", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "wl_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", cookie)
	}
	id := f.id(model.KindProject, "proj-a")
	send := func(header bool) int {
		req, _ := http.NewRequest("PATCH", f.ts.URL+"/api/v1/entities/"+id, strings.NewReader(`{"summary":"x"}`))
		req.AddCookie(cookie)
		if header {
			req.Header.Set("X-Wanglou", "1")
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := send(false); c != 403 {
		t.Fatalf("无标记得到 %d", c)
	}
	if c := send(true); c != 200 {
		t.Fatalf("有标记得到 %d", c)
	}
}

func TestStaleRevisionIsRefused(t *testing.T) {
	f := newFixture(t)
	id := f.id(model.KindProject, "proj-a")
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, f.owner, map[string]any{"summary": "新", "rev": 1}); code != 200 {
		t.Fatal(code)
	}
	if code, _ := f.do("PATCH", "/api/v1/entities/"+id, f.owner, map[string]any{"summary": "旧", "rev": 1}); code != 409 {
		t.Fatalf("旧版本的修改应被拒绝，得到 %d", code)
	}
}

// A03/R07: Agent 说通过，不等于项目通过。
func TestEvidenceIsAClaimNotAStateChange(t *testing.T) {
	f := newFixture(t)
	writer := f.token(model.Principal{Kind: "agent", Name: "reviewer", Scopes: []string{"read", "evidence"}, Workspaces: []string{"a"}})
	id := f.id(model.KindProject, "proj-a")
	code, b := f.do("POST", "/api/v1/entities/"+id+"/evidence", writer, map[string]any{"verdict": "pass", "scope": "接口冒烟"})
	if code != 201 {
		t.Fatalf("%d %s", code, b)
	}
	d := f.get("/api/v1/entities/"+id, f.owner)
	if d["attrs"].(map[string]any)["stage"] != "building" {
		t.Fatal("证据不能改变项目阶段")
	}
	ev := d["evidence"].([]any)[0].(map[string]any)
	if ev["by_kind"] != "agent" || ev["by_name"] != "reviewer" {
		t.Fatalf("证据要保留作者: %v", ev)
	}
	if code, _ := f.do("POST", "/api/v1/entities/"+id+"/evidence", writer, map[string]any{"verdict": "blocked", "scope": "x"}); code != 400 {
		t.Fatalf("blocked 必须写原因，得到 %d", code)
	}
	if code, _ := f.do("POST", "/api/v1/entities/"+id+"/evidence", writer, map[string]any{"verdict": "done", "scope": "x"}); code != 400 {
		t.Fatalf("未知结论应被拒绝，得到 %d", code)
	}
	if code, _ := f.do("POST", "/api/v1/entities/"+id+"/evidence", f.agentA, map[string]any{"verdict": "pass", "scope": "x"}); code != 403 {
		t.Fatalf("没有 evidence 权限得到 %d", code)
	}
}

func TestFindingsAppearOnceAndClearThemselves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 95<<30))
	for i := 0; i < 3; i++ {
		if err := f.eng.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		fs, _ := f.st.ListFindings(ctx, model.System(), store.FindingFilter{Open: true})
		n := 0
		for _, x := range fs {
			if x.Rule == "host.disk" {
				n++
			}
		}
		return n
	}
	if n := count(); n != 1 {
		t.Fatalf("持续的问题只应有一条，得到 %d", n)
	}
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now().Add(time.Second), 10<<30))
	if err := f.eng.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("恢复后应自动关闭，得到 %d", n)
	}
}

func TestSilentHostIsJudgedByOutsideEvidence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	host := f.id(model.KindDevice, "shared-host")
	svc := f.id(model.KindService, "svc-a")
	real := f.st.Now
	defer func() { f.st.Now = real }()
	old := time.Now().UTC().Add(-time.Hour)
	f.st.Now = func() time.Time { return old }
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", old, 10<<30))
	f.st.Now = real
	rule := func() string {
		if err := f.eng.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		fs, _ := f.st.ListFindings(ctx, model.System(), store.FindingFilter{Open: true, EntityID: host})
		for _, x := range fs {
			if strings.HasPrefix(x.Rule, "host.") {
				return x.Rule + "/" + x.Severity
			}
		}
		return ""
	}
	if got := rule(); got != "host.lost/critical" {
		t.Fatalf("没有旁证的失联应是紧急，得到 %s", got)
	}
	now := time.Now().UTC()
	f.st.PutSignal(ctx, model.Signal{EntityID: svc, Name: "check.http:https://a.example", State: model.StateOK, Source: "probe", ObservedAt: now, ReceivedAt: now, StaleAfterS: 300})
	if got := rule(); got != "host.feed_lost/warning" {
		t.Fatalf("外部探测还通时只算采集中断，得到 %s", got)
	}
}

func TestBigDiskWithRoomIsNotAnAlarm(t *testing.T) {
	cases := []struct {
		pct, free float64
		want      string
	}{
		{87, 177, model.StateOK}, {87, 13, model.StateWarn}, {94, 6.6, model.StateFail}, {95, 100, model.StateOK}, {50, 1, model.StateOK},
	}
	for _, c := range cases {
		if got := store.DiskState(c.pct, c.free); got != c.want {
			t.Errorf("%v%% / %vGB: %s", c.pct, c.free, got)
		}
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	exported, err := catalog.Export(ctx, f.st, model.System())
	if err != nil {
		t.Fatal(err)
	}
	st2, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := catalog.Import(ctx, st2, exported); err != nil {
		t.Fatal(err)
	}
	a, _ := f.st.AllEntities(ctx)
	b, _ := st2.AllEntities(ctx)
	ra, _ := f.st.AllRelations(ctx)
	rb, _ := st2.AllRelations(ctx)
	if len(a) != len(b) || len(ra) != len(rb) {
		t.Fatalf("实体 %d/%d 关系 %d/%d", len(a), len(b), len(ra), len(rb))
	}
	// 再导入一次不重复。
	if _, err := catalog.Import(ctx, st2, exported); err != nil {
		t.Fatal(err)
	}
	if b, _ = st2.AllEntities(ctx); len(a) != len(b) {
		t.Fatal("重复导入产生了重复对象")
	}
}

func TestExportOfScopedViewerStaysInScope(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.st.Authenticate(ctx, f.human)
	if err != nil {
		t.Fatal(err)
	}
	out, err := catalog.Export(ctx, f.st, v)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "绝密") || strings.Contains(string(b), "shared-host") {
		t.Fatalf("导出越界: %s", b)
	}
}

func TestUnauthenticatedGetsNothing(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/api/v1/overview", "/api/v1/entities", "/api/v1/me"} {
		if code, _ := f.do("GET", p, "", nil); code != 401 {
			t.Fatalf("%s 得到 %d", p, code)
		}
		if code, _ := f.do("GET", p, "wl_a_notarealtoken", nil); code != 401 {
			t.Fatalf("%s 伪造令牌得到 %d", p, code)
		}
	}
}

type bearer struct {
	tok  string
	base http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return b.base.RoundTrip(r)
}

func (f *fixture) mcp(tok string) *mcp.ClientSession {
	f.t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	tr := &mcp.StreamableClientTransport{Endpoint: f.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{tok, http.DefaultTransport}}}
	s, err := c.Connect(context.Background(), tr, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { s.Close() })
	return s
}

func (f *fixture) call(s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	f.t.Helper()
	r, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String(), r.IsError
}

func TestMCPToolsAreFilteredByTheSameRules(t *testing.T) {
	f := newFixture(t)
	s := f.mcp(f.agentA)
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
		for _, bad := range []string{"shell", "exec", "fetch_url", "secret", "password"} {
			if strings.Contains(tl.Name, bad) {
				t.Fatalf("不应有工具 %s", tl.Name)
			}
		}
	}
	for _, want := range []string{"overview", "search", "get_entity", "project_context", "device_context", "list_findings", "access_options", "submit_evidence"} {
		if !names[want] {
			t.Fatalf("缺工具 %s", want)
		}
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"overview", map[string]any{}},
		{"overview", map[string]any{"workspace": "b"}},
		{"search", map[string]any{"query": "绝密"}},
		{"search", map[string]any{"workspace": "b"}},
		{"list_findings", map[string]any{"workspace": "b"}},
	} {
		text, _ := f.call(s, c.tool, c.args)
		if strings.Contains(text, "绝密") {
			t.Fatalf("%s 泄露: %s", c.tool, text)
		}
	}
	text, isErr := f.call(s, "project_context", map[string]any{"slug": "proj-b"})
	if !isErr || strings.Contains(text, "绝密") {
		t.Fatalf("跨空间项目应报错且不泄露: %v %s", isErr, text)
	}
	text, isErr = f.call(s, "project_context", map[string]any{"slug": "proj-a"})
	if isErr || !strings.Contains(text, "甲项目") {
		t.Fatalf("自己空间的项目应可读: %v %s", isErr, text)
	}
}

func TestMCPAccessOptionsGiveReferencesOnly(t *testing.T) {
	f := newFixture(t)
	s := f.mcp(f.agentA)
	text, isErr := f.call(s, "access_options", map[string]any{"kind": "service", "slug": "svc-a"})
	if isErr || !strings.Contains(text, "cred-a") || !strings.Contains(text, "not_connected") {
		t.Fatalf("%v %s", isErr, text)
	}
	if strings.Contains(text, "mixia://") || strings.Contains(text, `"mode":"代理"`) {
		t.Fatalf("summary 空间不应给出凭据定位或使用方式: %s", text)
	}
}

func TestMCPEvidenceNeedsItsScope(t *testing.T) {
	f := newFixture(t)
	args := map[string]any{"project": "proj-a", "verdict": "pass", "scope": "接口"}
	if _, isErr := f.call(f.mcp(f.agentA), "submit_evidence", args); !isErr {
		t.Fatal("只读 Agent 不能写证据")
	}
	w := f.token(model.Principal{Kind: "agent", Name: "w", Scopes: []string{"read", "evidence"}, Workspaces: []string{"a"}})
	if text, isErr := f.call(f.mcp(w), "submit_evidence", args); isErr {
		t.Fatalf("%s", text)
	}
}

func TestMCPRejectsBadTokensAndAuditsCalls(t *testing.T) {
	f := newFixture(t)
	req, _ := http.NewRequest("POST", f.ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wl_a_nope")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("得到 %d", resp.StatusCode)
	}
	c := f.token(model.Principal{Kind: "collector", Name: "c", Scopes: []string{"ingest"}, Devices: []string{"*"}})
	req, _ = http.NewRequest("POST", f.ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+c)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("采集身份不能用 MCP，得到 %d", resp.StatusCode)
	}
	f.call(f.mcp(f.agentA), "overview", map[string]any{})
	events, _ := f.st.ListAudit(context.Background(), 20)
	found := false
	for _, e := range events {
		if e.Action == "mcp.overview" && e.Principal == "agent-a" {
			found = true
		}
	}
	if !found {
		t.Fatal("Agent 调用要留审计")
	}
}

func TestNoTokenHashesOrValuesInResponses(t *testing.T) {
	f := newFixture(t)
	_, b := f.do("GET", "/api/v1/principals", f.owner, nil)
	if strings.Contains(string(b), f.agentA) || strings.Contains(string(b), "token_hash") {
		t.Fatalf("响应包含令牌: %s", b)
	}
}
