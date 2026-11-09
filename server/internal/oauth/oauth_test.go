package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Feianxc/tianshu/server/internal/api"
	"github.com/Feianxc/tianshu/server/internal/mcpsrv"
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/oauth"
	"github.com/Feianxc/tianshu/server/internal/store"
	"github.com/Feianxc/tianshu/server/internal/view"
)

const verifier = "abcdefghijklmnopqrstuvwxyz0123456789-abcdefghijklmnop"

type env struct {
	t     *testing.T
	st    *store.Store
	ts    *httptest.Server
	as    *oauth.Server
	owner string
}

func setup(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	ws, err := st.UpsertWorkspace(ctx, model.Workspace{Slug: "a", Name: "甲", Kind: "company", CloudPolicy: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	wb, _ := st.UpsertWorkspace(ctx, model.Workspace{Slug: "b", Name: "乙", Kind: "company", CloudPolicy: "allow"})
	st.CreateEntity(ctx, model.Entity{Kind: "project", WorkspaceID: ws, Slug: "pa", Name: "甲项目", Confirmed: true})
	st.CreateEntity(ctx, model.Entity{Kind: "project", WorkspaceID: wb, Slug: "pb", Name: "乙绝密", Confirmed: true})

	e := &env{t: t, st: st}
	vw := &view.Service{Store: st}
	mux := http.NewServeMux()
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)
	e.as = &oauth.Server{Store: st, Public: e.ts.URL, MetadataHosts: []string{"allowed.example"}}
	srv := api.New(api.Config{Store: st, View: vw, MCP: mcpsrv.Handler(st, vw, e.as.Resource(), e.as.MetadataURL(), false), OAuth: e.as})
	mux.Handle("/", srv.Handler())
	_, e.owner, err = st.CreatePrincipal(ctx, model.Principal{Kind: "human", Name: "owner", Scopes: model.AllScopes, Workspaces: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func noFollow() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func challenge(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func (e *env) register(redirect string) string {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"client_name": "ChatGPT", "redirect_uris": []string{redirect}})
	r, err := http.Post(e.ts.URL+"/oauth/register", "application/json", strings.NewReader(string(b)))
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	if r.StatusCode != 201 {
		e.t.Fatalf("register: %d %v", r.StatusCode, out)
	}
	return out["client_id"].(string)
}

func (e *env) authorize(client, redirect, resource string) *http.Response {
	e.t.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "state": {"xyz"},
		"code_challenge": {challenge(verifier)}, "code_challenge_method": {"S256"}, "scope": {"read"}, "resource": {resource}}
	r, err := noFollow().Get(e.ts.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		e.t.Fatal(err)
	}
	r.Body.Close()
	return r
}

func (e *env) decide(id string, approve bool, spaces []string) string {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"approve": approve, "workspaces": spaces})
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/oauth/requests/"+id, strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+e.owner)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	if r.StatusCode != 200 {
		e.t.Fatalf("decide: %d %v", r.StatusCode, out)
	}
	return out["redirect"].(string)
}

func (e *env) tokenCall(form url.Values) (int, map[string]any) {
	e.t.Helper()
	r, err := http.PostForm(e.ts.URL+"/oauth/token", form)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	return r.StatusCode, out
}

// grant walks the whole flow and returns the code and client.
func (e *env) grant(spaces []string) (client, redirect, code string) {
	e.t.Helper()
	redirect = "https://client.example/cb"
	client = e.register(redirect)
	r := e.authorize(client, redirect, e.as.Resource())
	loc := r.Header.Get("Location")
	if r.StatusCode != 302 || !strings.HasPrefix(loc, "/#/oauth/") {
		e.t.Fatalf("authorize: %d %s", r.StatusCode, loc)
	}
	back, err := url.Parse(e.decide(strings.TrimPrefix(loc, "/#/oauth/"), true, spaces))
	if err != nil {
		e.t.Fatal(err)
	}
	if back.Query().Get("state") != "xyz" || back.Query().Get("iss") != e.ts.URL {
		e.t.Fatalf("回调缺 state 或 iss: %s", back)
	}
	return client, redirect, back.Query().Get("code")
}

func (e *env) exchange(client, redirect, code string) (int, map[string]any) {
	return e.tokenCall(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {e.as.Resource()}})
}

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

func (e *env) search(tok, q string) string {
	e.t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	s, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{tok}}}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	defer s.Close()
	r, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": q}})
	if err != nil {
		e.t.Fatal(err)
	}
	var sb strings.Builder
	for _, x := range r.Content {
		if t, ok := x.(*mcp.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

func TestMetadataAdvertisesWhatClientsRequire(t *testing.T) {
	e := setup(t)
	r, err := http.Get(e.ts.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var prm map[string]any
	json.NewDecoder(r.Body).Decode(&prm)
	if prm["resource"] != e.as.Resource() {
		t.Fatalf("%v", prm)
	}
	r2, err := http.Get(e.ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var asm map[string]any
	json.NewDecoder(r2.Body).Decode(&asm)
	for _, k := range []string{"code_challenge_methods_supported", "client_id_metadata_document_supported", "authorization_response_iss_parameter_supported", "registration_endpoint"} {
		if asm[k] == nil {
			t.Fatalf("缺 %s: %v", k, asm)
		}
	}
	if m := asm["code_challenge_methods_supported"].([]any); m[0] != "S256" {
		t.Fatalf("%v", m)
	}
}

func TestUnauthenticatedMCPPointsAtMetadata(t *testing.T) {
	e := setup(t)
	r, err := http.Post(e.ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 401 || !strings.Contains(r.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("%d %q", r.StatusCode, r.Header.Get("WWW-Authenticate"))
	}
}

func TestFullFlowGivesAScopedAgent(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	status, tok := e.exchange(client, redirect, code)
	if status != 200 {
		t.Fatalf("%d %v", status, tok)
	}
	access := tok["access_token"].(string)
	if got := e.search(access, "项目"); !strings.Contains(got, "甲项目") || strings.Contains(got, "绝密") {
		t.Fatalf("授权范围不对: %s", got)
	}
	// 一个 OAuth 访问令牌只能用于 MCP，不能当普通 API 令牌。
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("OAuth 令牌不应通过 REST，得到 %d", r.StatusCode)
	}
}

func TestCodeIsOneTimeAndBound(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	if s, _ := e.tokenCall(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client}, "redirect_uri": {redirect}, "code_verifier": {strings.Repeat("x", 50)}}); s != 400 {
		t.Fatalf("错误的 verifier 得到 %d", s)
	}
	// 失败的尝试也消耗了授权码。
	if s, _ := e.exchange(client, redirect, code); s != 400 {
		t.Fatalf("授权码应已作废，得到 %d", s)
	}
	client, redirect, code = e.grant([]string{"a"})
	if s, _ := e.exchange("cli_other", redirect, code); s != 400 {
		t.Fatalf("错误的 client 得到 %d", s)
	}
	client, redirect, code = e.grant([]string{"a"})
	if s, _ := e.exchange(client, redirect, code); s != 200 {
		t.Fatalf("正常兑换得到 %d", s)
	}
	if s, _ := e.exchange(client, redirect, code); s != 400 {
		t.Fatalf("授权码不能用两次，得到 %d", s)
	}
}

func TestAuthorizeRefusesBadRequests(t *testing.T) {
	e := setup(t)
	redirect := "https://client.example/cb"
	client := e.register(redirect)
	if r := e.authorize(client, "https://evil.example/cb", e.as.Resource()); r.StatusCode != 400 {
		t.Fatalf("陌生回调不能跳转，得到 %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r := e.authorize("cli_nope", redirect, e.as.Resource()); r.StatusCode != 400 {
		t.Fatalf("未知客户端得到 %d", r.StatusCode)
	}
	r := e.authorize(client, redirect, "https://other.example/mcp")
	if loc := r.Header.Get("Location"); !strings.Contains(loc, "error=invalid_target") {
		t.Fatalf("错误的 resource 得到 %s", loc)
	}
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}}
	r2, err := noFollow().Get(e.ts.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if !strings.Contains(r2.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("没有 PKCE 得到 %s", r2.Header.Get("Location"))
	}
}

func TestRegistrationRefusesPlainHTTP(t *testing.T) {
	e := setup(t)
	b := `{"client_name":"x","redirect_uris":["http://evil.example/cb"]}`
	r, _ := http.Post(e.ts.URL+"/oauth/register", "application/json", strings.NewReader(b))
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatalf("得到 %d", r.StatusCode)
	}
	if c := e.register("http://127.0.0.1:9999/callback"); c == "" {
		t.Fatal("本机回环应允许")
	}
}

func TestMetadataFetchIsLimitedToTheAllowlist(t *testing.T) {
	e := setup(t)
	for _, id := range []string{"https://evil.example/client.json", "https://127.0.0.1/x", "https://169.254.169.254/latest"} {
		if r := e.authorize(id, "https://x.example/cb", e.as.Resource()); r.StatusCode != 400 {
			t.Fatalf("%s 得到 %d", id, r.StatusCode)
		}
	}
}

func TestDenyTellsTheClient(t *testing.T) {
	e := setup(t)
	redirect := "https://client.example/cb"
	client := e.register(redirect)
	r := e.authorize(client, redirect, e.as.Resource())
	back := e.decide(strings.TrimPrefix(r.Header.Get("Location"), "/#/oauth/"), false, nil)
	if !strings.Contains(back, "error=access_denied") {
		t.Fatal(back)
	}
}

func TestRefreshRotatesAndReuseKillsTheFamily(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	_, tok := e.exchange(client, redirect, code)
	first := tok["refresh_token"].(string)
	s, tok2 := e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first}, "client_id": {client}})
	if s != 200 {
		t.Fatalf("%d %v", s, tok2)
	}
	second := tok2["refresh_token"].(string)
	if s, _ := e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first}}); s != 400 {
		t.Fatalf("重放旧刷新令牌得到 %d", s)
	}
	if s, _ := e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {second}}); s != 400 {
		t.Fatalf("重放后整个家族应失效，得到 %d", s)
	}
	if _, err := e.st.AuthenticateOAuth(context.Background(), tok2["access_token"].(string), e.as.Resource()); err == nil {
		t.Fatal("访问令牌也应失效")
	}
}

func TestTokenIsBoundToItsResource(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	_, tok := e.exchange(client, redirect, code)
	access := tok["access_token"].(string)
	if _, err := e.st.AuthenticateOAuth(context.Background(), access, "https://other.example/mcp"); err == nil {
		t.Fatal("发给别的资源的令牌不能用")
	}
}

func TestRevokingTheGrantStopsTheAgentAtOnce(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	_, tok := e.exchange(client, redirect, code)
	access := tok["access_token"].(string)
	list, _ := e.st.ListPrincipals(context.Background())
	for _, p := range list {
		if strings.HasPrefix(p.Name, "oauth:") {
			if err := e.st.RevokePrincipal(context.Background(), p.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := e.st.AuthenticateOAuth(context.Background(), access, e.as.Resource()); err == nil {
		t.Fatal("吊销后令牌必须立即失效")
	}
}

func TestExpiredTokenIsRefused(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	_, tok := e.exchange(client, redirect, code)
	real := e.st.Now
	e.st.Now = func() time.Time { return real().Add(2 * time.Hour) }
	defer func() { e.st.Now = real }()
	if _, err := e.st.AuthenticateOAuth(context.Background(), tok["access_token"].(string), e.as.Resource()); err == nil {
		t.Fatal("过期令牌必须失效")
	}
}

func TestConcurrentRefreshNeverLeavesTwoLiveBranches(t *testing.T) {
	e := setup(t)
	for round := range 15 {
		client, redirect, code := e.grant([]string{"a"})
		_, tok := e.exchange(client, redirect, code)
		first := tok["refresh_token"].(string)
		var wg sync.WaitGroup
		results := make([]map[string]any, 2)
		codes := make([]int, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], results[i] = e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first}})
			}()
		}
		wg.Wait()
		live := 0
		for i := range 2 {
			if codes[i] != 200 {
				continue
			}
			if _, err := e.st.AuthenticateOAuth(context.Background(), results[i]["access_token"].(string), e.as.Resource()); err == nil {
				live++
			}
		}
		if live > 1 {
			t.Fatalf("第 %d 轮：同一刷新令牌产生了两支活令牌", round)
		}
	}
}

func TestRevokedGrantCannotRefresh(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	_, tok := e.exchange(client, redirect, code)
	list, _ := e.st.ListPrincipals(context.Background())
	for _, p := range list {
		if strings.HasPrefix(p.Name, "oauth:") {
			e.st.RevokePrincipal(context.Background(), p.ID)
		}
	}
	if s, _ := e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}}); s != 400 {
		t.Fatalf("吊销后刷新得到 %d", s)
	}
}

func TestRefreshBoundToItsClient(t *testing.T) {
	e := setup(t)
	client, redirect, code := e.grant([]string{"a"})
	_, tok := e.exchange(client, redirect, code)
	refresh := tok["refresh_token"].(string)
	if s, _ := e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"cli_other"}}); s != 400 {
		t.Fatalf("别的客户端得到 %d", s)
	}
	// 错误的客户端不能把令牌消耗掉。
	if s, _ := e.tokenCall(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {client}}); s != 200 {
		t.Fatalf("真实客户端得到 %d", s)
	}
}

func TestRegistrationIsBoundedPerClient(t *testing.T) {
	e := setup(t)
	addr := "1.1.1.1"
	e.as.ClientIP = func(*http.Request) string { return addr }
	reg := func() int {
		r, err := http.Post(e.ts.URL+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"x","redirect_uris":["https://x.example/cb"]}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	limited := 0
	for range 40 {
		if reg() == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("匿名注册没有上限")
	}
	// 一个地址刷满不能把别人挡在外面。
	addr = "2.2.2.2"
	if c := reg(); c != 201 {
		t.Fatalf("其他客户端被连累，得到 %d", c)
	}
}

func TestJunkRequestsDoNotBlockARealAuthorization(t *testing.T) {
	e := setup(t)
	for range 100 {
		r := e.authorize("cli_nope", "https://x.example/cb", e.as.Resource())
		if r.StatusCode != 400 {
			t.Fatalf("得到 %d", r.StatusCode)
		}
	}
	redirect := "https://client.example/cb"
	client := e.register(redirect)
	if r := e.authorize(client, redirect, e.as.Resource()); !strings.HasPrefix(r.Header.Get("Location"), "/#/oauth/") {
		t.Fatalf("垃圾请求挡住了真实授权: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
}

func TestFullRegistryRecoversByItself(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	real := e.st.Now
	defer func() { e.st.Now = real }()
	e.st.Now = func() time.Time { return real().Add(-2 * time.Hour) }
	for i := range 500 {
		if err := e.st.SaveOAuthClient(ctx, store.OAuthClient{ClientID: fmt.Sprintf("cli_%d", i), Name: "junk", RedirectURIs: []string{"https://x.example/cb"}}); err != nil {
			t.Fatal(err)
		}
	}
	e.st.Now = real
	if c := e.register("https://client.example/cb"); c == "" {
		t.Fatal("注册表满了之后应该清掉没用过的客户端")
	}
	if n, _ := e.st.CountOAuthClients(ctx); n >= 500 {
		t.Fatalf("没有清理: %d", n)
	}
}

func TestPruneKeepsClientsThatAreInUse(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	client, redirect, code := e.grant([]string{"a"})
	e.exchange(client, redirect, code)
	real := e.st.Now
	defer func() { e.st.Now = real }()
	e.st.Now = func() time.Time { return real().Add(48 * time.Hour) }
	if err := e.st.PruneOAuthClients(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.OAuthClientByID(ctx, client); err != nil {
		t.Fatal("有令牌的客户端不能被清掉")
	}
}
