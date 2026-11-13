package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

type logsResp struct {
	TextShown bool   `json:"text_shown"`
	Withheld  string `json:"withheld"`
	Units     []model.LogStat
	Lines     []model.LogLine
}

func (f *fixture) logs(tok, id, query string) logsResp {
	f.t.Helper()
	code, b := f.do("GET", "/api/v1/entities/"+id+"/logs?"+query, tok, nil)
	if code != 200 {
		f.t.Fatalf("logs %s: %d %s", id, code, b)
	}
	var out logsResp
	if err := json.Unmarshal(b, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func unitsOf(r logsResp) string {
	var s []string
	for _, u := range r.Units {
		s = append(s, u.Unit)
	}
	for _, l := range r.Lines {
		s = append(s, l.Unit)
	}
	return strings.Join(s, ",")
}

func seedLogs(f *fixture) {
	now := time.Now().UTC()
	r := report("shared-host", now, 10<<30)
	r.Logs = []model.ReportLog{
		{TS: now.Add(-time.Minute), Unit: "a.service", Priority: 3, Message: "a crashed: connection refused"},
		{TS: now.Add(-50 * time.Second), Unit: "b.service", Priority: 3, Message: "b 绝密服务 failed"},
		{TS: now.Add(-40 * time.Second), Unit: "", Ident: "kernel", Priority: 4, Message: "login password=hunter2 token: abcdefghijklmnop from ops@example.com"},
	}
	if code, b := f.do("POST", "/api/v1/ingest/report", f.owner, r); code != 200 {
		f.t.Fatalf("上报失败 %d %s", code, b)
	}
}

func TestLogsAreGatedByScopeAndCloudPolicy(t *testing.T) {
	f := newFixture(t)
	seedLogs(f)
	host, svcA := f.id(model.KindDevice, "shared-host"), f.id(model.KindService, "svc-a")

	all := f.logs(f.owner, host, "level=all")
	if !all.TextShown || len(all.Lines) != 3 {
		t.Fatalf("主人应看到全部 3 行: %+v", all)
	}
	for _, l := range all.Lines {
		if strings.Contains(l.Message, "hunter2") || strings.Contains(l.Message, "abcdefghijklmnop") || strings.Contains(l.Message, "ops@example.com") {
			t.Fatalf("疑似密钥和邮箱应在入库前隐去: %q", l.Message)
		}
	}
	if errs := f.logs(f.owner, host, ""); len(errs.Lines) != 2 {
		t.Fatalf("默认只返回错误级别，应为 2 行，得到 %d", len(errs.Lines))
	}

	reader := f.token(model.Principal{Kind: "agent", Name: "log-reader", Scopes: []string{"read", "logs"}, Workspaces: []string{"personal", "a"}})
	got := f.logs(reader, host, "level=all")
	if !got.TextShown || strings.Contains(unitsOf(got), "b.service") || len(got.Lines) != 2 {
		t.Fatalf("Agent 看主机日志时不应看到其他空间服务的单元: %+v", got)
	}
	if s := f.logs(reader, svcA, "level=all"); s.TextShown || len(s.Lines) != 0 || unitsOf(s) != "a.service" || s.Withheld == "" {
		t.Fatalf("只给摘要的空间不出原文，只给 a.service 的计数: %+v", s)
	}
	if s := f.logs(f.agentA, svcA, "level=all"); s.TextShown || unitsOf(s) != "a.service" {
		t.Fatalf("没有日志权限只给计数: %+v", s)
	}
	if code, _ := f.do("GET", "/api/v1/entities/"+f.id(model.KindService, "svc-b")+"/logs", reader, nil); code != 404 {
		t.Fatalf("禁止空间的服务对 Agent 不存在，得到 %d", code)
	}
}

func TestDiagnoseExplainsTheProblem(t *testing.T) {
	f := newFixture(t)
	seedLogs(f)
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now().Add(time.Second), 99<<30))
	f.must(f.eng.Evaluate(t.Context()))
	code, b := f.do("GET", "/api/v1/entities/"+f.id(model.KindDevice, "shared-host")+"/diagnose", f.owner, nil)
	if code != 200 || !strings.Contains(string(b), "host.disk") || !strings.Contains(string(b), `"playbook"`) || !strings.Contains(string(b), "du -xh") {
		t.Fatalf("诊断应带出磁盘问题和处理步骤: %d %s", code, b)
	}
}

func TestSummaryFindingsTellAgentsOnlyWhatTheRuleMeans(t *testing.T) {
	f := newFixture(t)
	f.do("POST", "/api/v1/ingest/report", f.owner, report("a-host", time.Now(), 99<<30))
	f.must(f.eng.Evaluate(t.Context()))
	_, b := f.do("GET", "/api/v1/findings?open=true", f.agentA, nil)
	if !strings.Contains(string(b), "host.disk") || strings.Contains(string(b), "已用") || strings.Contains(string(b), `"/"`) {
		t.Fatalf("摘要空间的待处理只给含义，不给挂载点和用量: %s", b)
	}
}

func TestMovingAServiceOffAHostDropsItsUnitResults(t *testing.T) {
	f := newFixture(t)
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 10<<30))
	svc, host := f.id(model.KindService, "svc-a"), f.id(model.KindDevice, "shared-host")
	has := func() bool {
		_, b := f.do("GET", "/api/v1/entities/"+svc, f.owner, nil)
		return strings.Contains(string(b), "unit:shared-host:a.service")
	}
	if !has() {
		t.Fatal("上报后服务应有主机上的单元信号")
	}
	if code, b := f.do("DELETE", "/api/v1/relations?from="+svc+"&type=runs_on&to="+host, f.owner, nil); code != 200 {
		t.Fatalf("删除关系失败 %d %s", code, b)
	}
	if has() {
		t.Fatal("服务离开主机后，那台主机的单元结果应立即清掉")
	}
}

func TestAnAgentCanBeGivenLogsWithoutReconnecting(t *testing.T) {
	f := newFixture(t)
	seedLogs(f)
	p, tok, err := f.st.CreatePrincipal(t.Context(), model.Principal{Kind: "agent", Name: "late", Scopes: []string{"read"}, Workspaces: []string{"personal"}})
	if err != nil {
		t.Fatal(err)
	}
	host := f.id(model.KindDevice, "shared-host")
	if f.logs(tok, host, "level=all").TextShown {
		t.Fatal("没有 logs 权限不应看到原文")
	}
	for _, bad := range [][]string{{"read", "admin"}, {"logs"}, {"read", "write"}} {
		if code, _ := f.do("PATCH", "/api/v1/principals/"+p.ID, f.owner, map[string]any{"scopes": bad}); code != 400 {
			t.Fatalf("%v 应被拒绝，得到 %d", bad, code)
		}
	}
	if code, b := f.do("PATCH", "/api/v1/principals/"+p.ID, f.owner, map[string]any{"scopes": []string{"read", "logs"}}); code != 200 {
		t.Fatalf("授予失败 %d %s", code, b)
	}
	if !f.logs(tok, host, "level=all").TextShown {
		t.Fatal("授予后同一个令牌应立即能读日志")
	}
	if code, _ := f.do("PATCH", "/api/v1/principals/"+p.ID, f.agentA, map[string]any{"scopes": []string{"read", "logs"}}); code != 403 {
		t.Fatalf("Agent 不能给自己加权限，得到 %d", code)
	}
}
