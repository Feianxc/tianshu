package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

func check(kind, target string) model.Check {
	return model.Check{Kind: kind, Target: target, TimeoutS: 3, IntervalS: 60, Enabled: true}
}

func TestHTTPExpectations(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("healthy"))
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			w.WriteHeader(503)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	if r := Run(ctx, check("http", ts.URL+"/ok")); !r.OK || r.Latency == nil {
		t.Fatalf("%+v", r)
	}
	if r := Run(ctx, check("http", ts.URL+"/bad")); r.OK || r.State != model.StateFail {
		t.Fatalf("503 应失败 %+v", r)
	}
	c := check("http", ts.URL+"/ok")
	c.ExpectBody = "nope"
	if r := Run(ctx, c); r.OK {
		t.Fatal("内容不符应失败")
	}
	c = check("http", ts.URL+"/redirect")
	c.ExpectStatus = []int{200}
	if r := Run(ctx, c); r.OK {
		t.Fatal("跳转不能被自动跟随后当成 200")
	}
}

func TestClosedPortFailsWithoutLeakingAddresses(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	ctx := context.Background()
	if r := Run(ctx, check("tcp", addr)); !r.OK {
		t.Fatalf("开放端口应成功 %+v", r)
	}
	l.Close()
	r := Run(ctx, check("tcp", addr))
	if r.OK {
		t.Fatal("关闭的端口应失败")
	}
	if strings.Contains(r.Error, "127.0.0.1") || strings.Contains(r.Summary, "127.0.0.1") {
		t.Fatalf("错误信息带出了地址: %s / %s", r.Error, r.Summary)
	}
}

func TestOneFailureIsABlipTwoAreAnOutage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ws, _ := st.UpsertWorkspace(ctx, model.Workspace{Slug: "p", Name: "p", Kind: "personal", CloudPolicy: "allow"})
	e, err := st.CreateEntity(ctx, model.Entity{Kind: model.KindService, WorkspaceID: ws, Slug: "s", Name: "s"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer ts.Close()
	c := check("http", ts.URL)
	c.EntityID = e.ID
	if c.ID, err = st.UpsertCheck(ctx, c); err != nil {
		t.Fatal(err)
	}
	s := &Scheduler{Store: st, Vantage: "test"}
	state := func() string {
		sigs, _ := st.AllSignals(ctx)
		return sigs[e.ID][0].State
	}
	s.runOne(ctx, c)
	if got := state(); got != model.StateUnknown {
		t.Fatalf("第一次失败应是未知，得到 %s", got)
	}
	time.Sleep(2 * time.Millisecond)
	s.runOne(ctx, c)
	if got := state(); got != model.StateFail {
		t.Fatalf("连续两次失败应是故障，得到 %s", got)
	}
}
