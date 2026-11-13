package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

const sample = `CPU 1000 800 2000 1600
NET 100 200 1100 1200
HOST host1
KERNEL Linux 6.6
ARCH x86_64
OS OpenCloudOS 9.4
UPTIME 12345.67 1
LOAD 0.10 0.20 0.30
NCPU 2
MEM MemTotal: 2000000
MEM MemAvailable: 500000
MEM SwapTotal: 1000
MEM SwapFree: 1000
DISK / 37000000000 50000000000
SYSTEMD degraded
UNIT mixia-server.service loaded active running Mixia
UNIT huara.service loaded failed failed Huara
UNIT ghost.service not-found inactive dead ghost
PORT LISTEN 0 128 127.0.0.1:18790 0.0.0.0:*
PORT LISTEN 0 128 [::]:2222 [::]:*
END
`

func TestParseSSH(t *testing.T) {
	r, err := parseSSH(sample, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Host.Hostname != "host1" || r.Host.OS != "OpenCloudOS 9.4" {
		t.Fatalf("host %+v", r.Host)
	}
	if got := *r.Metrics.CPUPct; got != 20 {
		t.Fatalf("cpu %v", got)
	}
	if got := *r.Metrics.MemUsed / *r.Metrics.MemTotal; got != 0.75 {
		t.Fatalf("mem %v", got)
	}
	if len(r.Units) != 2 || r.Units[1].Active != "failed" {
		t.Fatalf("units %+v", r.Units)
	}
	if len(r.Ports) != 2 || r.Disks[0].Mount != "/" {
		t.Fatalf("ports %v disks %v", r.Ports, r.Disks)
	}
}

func TestParseSSHReadsHowTheHostLetsPeopleIn(t *testing.T) {
	s := strings.Replace(sample, "END\n", `SSHD port 22
SSHD port 22000
SSHD permitrootlogin yes
SSHD passwordauthentication yes
SSHFAIL 1520
AKEY SHA256:abc
AKEY SHA256:def
F2B inactive
END
`, 1)
	r, err := parseSSH(s, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	x := r.SSH
	if x == nil || x.PasswordAuth != "yes" || x.RootLogin != "yes" || x.Fail2ban != "inactive" {
		t.Fatalf("ssh %+v", x)
	}
	if len(x.Ports) != 2 || x.Ports[1] != 22000 || len(x.Keys) != 2 || x.FailedLastHour == nil || *x.FailedLastHour != 1520 {
		t.Fatalf("ssh %+v", x)
	}
	// 不是 root 时读不到这些，不能写成“已关闭”。
	r, err = parseSSH(strings.Replace(sample, "END\n", "F2B inactive\nEND\n", 1), time.Now())
	if err != nil || r.SSH != nil {
		t.Fatalf("读不到时应该没有这一节: %+v %v", r.SSH, err)
	}
}

func TestParseSSHRejectsTruncatedOutput(t *testing.T) {
	if _, err := parseSSH(strings.TrimSuffix(sample, "END\n"), time.Now()); err == nil {
		t.Fatal("被截断的输出不能当成完整的主机状态")
	}
}

func TestMissingMetricStaysNil(t *testing.T) {
	r, err := parseSSH("HOST h\nEND\n", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Metrics.CPUPct != nil || r.Metrics.MemUsed != nil {
		t.Fatal("没测到的指标必须是空值，不能是 0")
	}
}

func TestParseStatus(t *testing.T) {
	var r model.ReportRepo
	parseStatus(`# branch.oid abc
# branch.head main
# branch.upstream origin/main
# branch.ab +3 -1
# stash 2
1 .M N... 100644 100644 100644 a b file.go
? new.txt`, &r)
	if r.Branch != "main" || !r.HasUpstream || r.Ahead != 3 || r.Behind != 1 || r.Stash != 2 || r.Dirty != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestSanitizeRemoteStripsCredentials(t *testing.T) {
	cases := map[string]string{
		"https://user:ghp_abcdefghijklmnopqrstuvwxyz0123456789@github.com/a/b.git": "https://github.com/a/b.git",
		"https://x-access-token:secret@github.com/a/b":                             "https://github.com/a/b",
		"git@github.com:a/b.git":                                                   "github.com:a/b.git",
		"https://github.com/a/b.git?token=abc":                                     "https://github.com/a/b.git",
	}
	for in, want := range cases {
		if got := SanitizeRemote(in); got != want {
			t.Errorf("%s => %s, want %s", in, got, want)
		}
	}
}

func TestJournalLinesAreParsedFilteredAndFolded(t *testing.T) {
	in := `{"__REALTIME_TIMESTAMP":"1762900000000000","PRIORITY":"3","_SYSTEMD_UNIT":"app.service","MESSAGE":"boom"}
{"__REALTIME_TIMESTAMP":"1762900001000000","PRIORITY":"3","_SYSTEMD_UNIT":"app.service","MESSAGE":"boom"}
{"__REALTIME_TIMESTAMP":"1762900002000000","PRIORITY":"4","SYSLOG_IDENTIFIER":"sshd","MESSAGE":"scanner hung up"}
{"__REALTIME_TIMESTAMP":"1762900002500000","PRIORITY":"4","SYSLOG_IDENTIFIER":"kernel","MESSAGE":"[UFW BLOCK] IN=eth0 SRC=192.0.2.4"}
{"__REALTIME_TIMESTAMP":"1762900003000000","PRIORITY":"6","UNIT":"app.service","_SYSTEMD_UNIT":"init.scope","MESSAGE":[104,105]}
not json`
	got := foldLogs(parseJournal(in, false))
	if len(got) != 2 || got[0].Repeat != 1 || got[0].Unit != "app.service" || got[1].Message != "hi" || got[1].Unit != "app.service" {
		t.Fatalf("%+v", got)
	}
	if len(parseJournal(in, true)) != 5 {
		t.Fatal("失败单元的日志不应过滤 sshd")
	}
}
