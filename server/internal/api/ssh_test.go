package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

func fingerprint(c string) string { return "SHA256:" + strings.Repeat(c, 43) }

func loginReport(device string, at time.Time, password string, keys ...string) model.Report {
	r := report(device, at, 10<<30)
	n := 1500
	r.SSH = &model.ReportSSH{PasswordAuth: password, RootLogin: "yes", Ports: []int{22}, Fail2ban: "inactive", FailedLastHour: &n, Keys: keys}
	return r
}

func (f *fixture) findings(token string) string {
	f.t.Helper()
	f.must(f.eng.Evaluate(context.Background()))
	_, b := f.do("GET", "/api/v1/findings?open=true", token, nil)
	return string(b)
}

func TestPasswordLoginIsAFindingNotAFault(t *testing.T) {
	f := newFixture(t)
	id := f.id(model.KindDevice, "a-host")
	f.do("POST", "/api/v1/ingest/report", f.owner, report("a-host", time.Now(), 10<<30))
	f.must(f.eng.Evaluate(context.Background()))
	before := f.get("/api/v1/entities/"+id, f.owner)["health"].(map[string]any)["state"]

	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", time.Now().Add(time.Second), "yes", fingerprint("a")))
	got := f.findings(f.owner)
	if !strings.Contains(got, "host.ssh_password") || !strings.Contains(got, "1500 次失败登录") {
		t.Fatalf("应提示密码登录: %s", got)
	}
	if after := f.get("/api/v1/entities/"+id, f.owner)["health"].(map[string]any)["state"]; after != before {
		t.Fatalf("安全配置不该改变健康状态: %v -> %v", before, after)
	}

	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", time.Now().Add(2*time.Second), "no", fingerprint("a")))
	if got := f.findings(f.owner); strings.Contains(got, "host.ssh_password") {
		t.Fatalf("改成只用密钥后应自动关闭: %s", got)
	}
}

func TestOnlyKeysAddedAfterTheBaselineAreNew(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	a, b := fingerprint("a"), fingerprint("b")
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", now, "no", a))
	if got := f.findings(f.owner); strings.Contains(got, "host.ssh_new_key") {
		t.Fatalf("开始观察时已有的钥匙不算新增: %s", got)
	}
	// 一次读不到钥匙文件不能清空基线，否则下一次所有钥匙都成了新钥匙。
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", now.Add(time.Second), "no"))
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", now.Add(2*time.Second), "no", a))
	if got := f.findings(f.owner); strings.Contains(got, "host.ssh_new_key") {
		t.Fatalf("基线被一次失败的读取清掉了: %s", got)
	}
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", now.Add(3*time.Second), "no", a, b))
	got := f.findings(f.owner)
	if !strings.Contains(got, "host.ssh_new_key") || !strings.Contains(got, "critical") {
		t.Fatalf("新增钥匙应是紧急事项: %s", got)
	}
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", now.Add(4*time.Second), "no", a))
	if got := f.findings(f.owner); strings.Contains(got, "host.ssh_new_key") {
		t.Fatalf("钥匙移除后应自动关闭: %s", got)
	}
}

func TestSharedKeyStaysOutOfAnAgentsSight(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	k := fingerprint("c")
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("a-host", now, "no", k))
	f.do("POST", "/api/v1/ingest/report", f.owner, loginReport("shared-host", now, "no", k))
	got := f.findings(f.owner)
	if !strings.Contains(got, "host.ssh_shared_key") {
		t.Fatalf("主人应看到跨空间共用: %s", got)
	}
	if !strings.Contains(got, "另外 1 台其他空间的主机") {
		t.Fatalf("应说明还能登录几台其他空间的主机: %s", got)
	}
	id := f.id(model.KindDevice, "a-host")
	for _, p := range []string{"/api/v1/overview", "/api/v1/entities/" + id, "/api/v1/findings?open=true"} {
		_, b := f.do("GET", p, f.agentA, nil)
		if strings.Contains(string(b), "host.ssh_shared_key") || strings.Contains(string(b), "其他空间") || strings.Contains(string(b), k) {
			t.Fatalf("%s 让 Agent 看到了别的空间或钥匙指纹: %s", p, b)
		}
	}
	if _, b := f.do("GET", "/api/v1/entities/"+id, f.owner, nil); !strings.Contains(string(b), k) {
		t.Fatal("主人应能看到指纹")
	}
}

func TestLoginReportAcceptsOnlyKnownValues(t *testing.T) {
	f := newFixture(t)
	r := report("a-host", time.Now(), 10<<30)
	bad := "wl_stolen_secret_value_1234567890"
	n := -5
	r.SSH = &model.ReportSSH{PasswordAuth: "yes", RootLogin: bad, Fail2ban: bad, FailedLastHour: &n, Ports: []int{-1, 70000, 22}, Keys: []string{bad, "SHA256:short", fingerprint("d")}}
	f.do("POST", "/api/v1/ingest/report", f.owner, r)
	id := f.id(model.KindDevice, "a-host")
	_, b := f.do("GET", "/api/v1/entities/"+id, f.owner, nil)
	s := string(b)
	if strings.Contains(s, "stolen") || strings.Contains(s, "SHA256:short") || strings.Contains(s, "70000") {
		t.Fatalf("不在白名单的内容进了数据库: %s", s)
	}
	if !strings.Contains(s, fingerprint("d")) {
		t.Fatalf("合法指纹应保留: %s", s)
	}
}
