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

	"github.com/Feianxc/tianshu/server/internal/mcpsrv"
	"github.com/Feianxc/tianshu/server/internal/view"
)

type pairReq struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Secret string `json:"secret"`
}

// anon 发一个不带任何身份的 POST，返回状态、正文和会话 Cookie。
func (f *fixture) anon(path string, body any) (int, string, *http.Cookie) {
	f.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(f.ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	for _, c := range resp.Cookies() {
		if c.Name == "wl_session" && c.Value != "" {
			return resp.StatusCode, string(out), c
		}
	}
	return resp.StatusCode, string(out), nil
}

func (f *fixture) newPairing() pairReq {
	f.t.Helper()
	code, body, _ := f.anon("/api/v1/pairing", map[string]any{})
	if code != 201 {
		f.t.Fatalf("发起配对得到 %d %s", code, body)
	}
	var p pairReq
	if err := json.Unmarshal([]byte(body), &p); err != nil || p.ID == "" || p.Secret == "" || len(p.Code) != 9 {
		f.t.Fatalf("配对请求 %s", body)
	}
	return p
}

func (f *fixture) poll(p pairReq, secret string) (int, string, *http.Cookie) {
	return f.anon("/api/v1/pairing/"+p.ID+"/poll", map[string]any{"secret": secret})
}

func TestPairingNeedsApprovalAndWorksOnce(t *testing.T) {
	f := newFixture(t)
	p := f.newPairing()

	if code, body, c := f.poll(p, p.Secret); code != 200 || !strings.Contains(body, "pending") || c != nil {
		t.Fatalf("未批准前只能等待: %d %s %v", code, body, c)
	}
	if code, b := f.do("GET", "/api/v1/pairings", f.owner, nil); code != 200 || !strings.Contains(string(b), p.Code) || strings.Contains(string(b), p.Secret) {
		t.Fatalf("管理员应看到代码、看不到密钥: %d %s", code, b)
	}
	for _, tok := range []string{f.agentA, f.human} {
		if code, _ := f.do("POST", "/api/v1/pairings/"+p.ID, tok, map[string]any{"approve": true}); code != 403 {
			t.Fatalf("Agent 和非管理员不能批准，得到 %d", code)
		}
	}
	if code, b := f.do("POST", "/api/v1/pairings/"+p.ID, f.owner, map[string]any{"approve": true, "hours": 5}); code != 400 {
		t.Fatalf("有效期只能是 12 小时或 7 天: %d %s", code, b)
	}
	if code, b := f.do("POST", "/api/v1/pairings/"+p.ID, f.owner, map[string]any{"approve": true, "hours": 168}); code != 200 {
		t.Fatalf("批准失败 %d %s", code, b)
	}

	if code, _, c := f.poll(p, p.Secret+"x"); code != 404 || c != nil {
		t.Fatalf("密钥不对不能拿到会话: %d %v", code, c)
	}
	code, body, cookie := f.poll(p, p.Secret)
	if code != 200 || !strings.Contains(body, "approved") || cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("批准后应拿到会话: %d %s %+v", code, body, cookie)
	}
	if cookie.MaxAge < 6*24*3600 {
		t.Fatalf("选了 7 天，Cookie 只有 %d 秒", cookie.MaxAge)
	}
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/me", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	me, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(me), "owner") {
		t.Fatalf("新设备应以批准人的身份登录: %d %s", resp.StatusCode, me)
	}
	if code, _, c := f.poll(p, p.Secret); code != 404 || c != nil {
		t.Fatalf("同一个请求只能换一次会话: %d %v", code, c)
	}
	if _, b := f.do("GET", "/api/v1/audit?limit=10", f.owner, nil); !strings.Contains(string(b), "pairing.approve") || !strings.Contains(string(b), "session.pair") {
		t.Fatalf("批准和换取会话都要审计: %s", b)
	}
}

func TestDeniedOrExpiredPairingGetsNoSession(t *testing.T) {
	f := newFixture(t)
	p := f.newPairing()
	if code, _ := f.do("POST", "/api/v1/pairings/"+p.ID, f.owner, map[string]any{"approve": false}); code != 200 {
		t.Fatalf("拒绝失败 %d", code)
	}
	if code, body, c := f.poll(p, p.Secret); code != 200 || !strings.Contains(body, "denied") || c != nil {
		t.Fatalf("被拒绝的请求不能拿到会话: %d %s %v", code, body, c)
	}

	q := f.newPairing()
	real := f.st.Now
	f.st.Now = func() time.Time { return real().Add(6 * time.Minute) }
	if code, _ := f.do("POST", "/api/v1/pairings/"+q.ID, f.owner, map[string]any{"approve": true}); code != 404 {
		t.Fatalf("过期的请求不能批准，得到 %d", code)
	}
	if code, _, c := f.poll(q, q.Secret); code != 404 || c != nil {
		t.Fatalf("过期的请求不能拿到会话: %d %v", code, c)
	}
}

func TestPairingRequestsAreCapped(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 4; i++ {
		f.newPairing()
	}
	if code, body, _ := f.anon("/api/v1/pairing", map[string]any{}); code != 429 {
		t.Fatalf("同一地址等待中的请求应有上限，得到 %d %s", code, body)
	}
	// 管理员看到的列表里不会混进已处理的请求
	_, b := f.do("GET", "/api/v1/pairings", f.owner, nil)
	var list struct{ Items []map[string]any }
	_ = json.Unmarshal(b, &list)
	if len(list.Items) != 4 {
		t.Fatalf("应有 4 个等待中的请求，得到 %d", len(list.Items))
	}
}

func TestSnoozeIsBounded(t *testing.T) {
	f := newFixture(t)
	f.do("POST", "/api/v1/ingest/report", f.owner, report("shared-host", time.Now(), 95<<30))
	f.must(f.eng.Evaluate(context.Background()))
	var list struct{ Items []struct{ ID, Rule string } }
	_, b := f.do("GET", "/api/v1/findings?open=true", f.owner, nil)
	_ = json.Unmarshal(b, &list)
	id := ""
	for _, x := range list.Items {
		if x.Rule == "host.disk" {
			id = x.ID
		}
	}
	if id == "" {
		t.Fatalf("没有磁盘告警: %s", b)
	}
	for _, h := range []int{-1, 30*24 + 1, 1 << 40} {
		if code, _ := f.do("POST", "/api/v1/findings/"+id+"/ack", f.owner, map[string]any{"snooze_hours": h}); code != 400 {
			t.Fatalf("snooze %d 应被拒绝，得到 %d", h, code)
		}
	}
	if code, b := f.do("POST", "/api/v1/findings/"+id+"/ack", f.owner, map[string]any{"snooze_hours": 30 * 24}); code != 200 {
		t.Fatalf("30 天应允许: %d %s", code, b)
	}
}

// Behind a tunnel the hub listens on loopback but is reached by its public name.
// That name must work; any other non-loopback Host (DNS rebinding) must not.
func TestMCPAcceptsItsPublicHostOnly(t *testing.T) {
	f := newFixture(t)
	h := mcpsrv.Handler(f.st, &view.Service{Store: f.st}, "https://hub.example.com/mcp", "https://hub.example.com/.well-known/oauth-protected-resource", false)
	ts := httptest.NewServer(h)
	defer ts.Close()
	send := func(host string) int {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
		req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(body))
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+f.agentA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	for host, want := range map[string]int{"hub.example.com": 200, "HUB.example.com": 200, "127.0.0.1:8787": 200, "localhost": 200, "evil.example": 403, "hub.example.com.evil.example": 403} {
		if got := send(host); got != want {
			t.Fatalf("Host %s: 得到 %d，应为 %d", host, got, want)
		}
	}
}
