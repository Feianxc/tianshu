// Package oauth lets clients that insist on OAuth, such as ChatGPT and dots, reach
// the MCP endpoint. The hub is both authorization server and resource server, so
// access tokens are opaque rows rather than signed JWTs: there is no signing key to
// manage and revocation is immediate.
//
// It implements only what those clients need: authorization code with PKCE S256,
// public clients, refresh rotation, RFC 9728 and RFC 8414 metadata, resource
// indicators, client ID metadata documents from an allowlist, and dynamic
// registration for everything else. A person always approves in the console and
// chooses which workspaces the grant may read. Every grant becomes an ordinary
// agent principal, so the same workspace and cloud-policy rules apply.
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

const (
	accessTTL  = time.Hour
	refreshTTL = 30 * 24 * time.Hour
	codeTTL    = 2 * time.Minute
	requestTTL = 10 * time.Minute
	maxClients = 500
)

// DefaultMetadataHosts are the hosts client ID metadata documents may come from.
// Fetching a URL a stranger names is a server-side request forgery primitive, so
// the hub only does it for hosts on this list; other clients use registration.
var DefaultMetadataHosts = []string{"chatgpt.com", "openai.com", "claude.ai", "anthropic.com"}

// Server handles the OAuth endpoints.
type Server struct {
	Store         *store.Store
	Public        string // https://wanglou.example.com, no trailing slash
	MetadataHosts []string
	HTTP          *http.Client
	ClientIP      func(*http.Request) string
	limit         limiter
}

// limiter caps the unauthenticated endpoints that write rows, per client address.
// One shared counter would let anyone lock the owner out with a burst of junk.
type limiter struct {
	mu   sync.Mutex
	seen map[string]*bucket
}

type bucket struct {
	window time.Time
	n      int
}

func (l *limiter) allow(key string, perMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil || len(l.seen) > 10000 {
		l.seen = map[string]*bucket{}
	}
	now := time.Now()
	b := l.seen[key]
	if b == nil || now.Sub(b.window) > time.Minute {
		b = &bucket{window: now}
		l.seen[key] = b
	}
	b.n++
	return b.n <= perMinute
}

func (s *Server) client(r *http.Request) string {
	if s.ClientIP != nil {
		return s.ClientIP(r)
	}
	return r.RemoteAddr
}

// Resource is the canonical identifier of the MCP endpoint.
func (s *Server) Resource() string { return s.Public + "/mcp" }

// MetadataURL is advertised in WWW-Authenticate.
func (s *Server) MetadataURL() string { return s.Public + "/.well-known/oauth-protected-resource" }

// Mount registers the public endpoints.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMeta)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.resourceMeta)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.serverMeta)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.token)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, code int, kind, desc string) {
	writeJSON(w, code, map[string]string{"error": kind, "error_description": desc})
}

func (s *Server) resourceMeta(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.Resource(),
		"authorization_servers":    []string{s.Public},
		"scopes_supported":         []string{model.ScopeRead, model.ScopeEvidence},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "天枢",
	})
}

func (s *Server) serverMeta(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.Public,
		"authorization_endpoint":                         s.Public + "/oauth/authorize",
		"token_endpoint":                                 s.Public + "/oauth/token",
		"registration_endpoint":                          s.Public + "/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               []string{model.ScopeRead, model.ScopeEvidence},
		"client_id_metadata_document_supported":          len(s.MetadataHosts) > 0,
		"authorization_response_iss_parameter_supported": true,
	})
}

// validRedirect accepts https anywhere and http only on loopback, which is how
// native clients receive their callback.
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := u.Hostname()
	return u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

// redirectMatches compares exactly, except that a loopback port may differ, as
// RFC 8252 requires for native clients on ephemeral ports.
func redirectMatches(registered []string, got string) bool {
	if slices.Contains(registered, got) {
		return true
	}
	g, err := url.Parse(got)
	if err != nil || g.Scheme != "http" {
		return false
	}
	for _, r := range registered {
		u, err := url.Parse(r)
		if err == nil && u.Scheme == "http" && u.Hostname() == g.Hostname() && u.Path == g.Path && validRedirect(got) {
			return true
		}
	}
	return false
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.limit.allow(s.client(r), 30) {
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "注册太频繁")
		return
	}
	n, err := s.Store.CountOAuthClients(r.Context())
	if err == nil && n >= maxClients {
		// Full usually means junk. Clear what never got used before refusing anyone.
		if err = s.Store.PruneOAuthClients(r.Context(), time.Hour); err == nil {
			n, err = s.Store.CountOAuthClients(r.Context())
		}
	}
	if err != nil || n >= maxClients {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "注册已满，稍后再试")
		return
	}
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "请求不是合法 JSON")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "需要 1 到 10 个 redirect_uris")
		return
	}
	for _, u := range in.RedirectURIs {
		if !validRedirect(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri 必须是 https 或本机回环地址")
			return
		}
	}
	name := strings.TrimSpace(in.ClientName)
	if name == "" || len(name) > 80 {
		name = "未命名客户端"
	}
	id := store.NewID("cli")
	if err := s.Store.SaveOAuthClient(r.Context(), store.OAuthClient{ClientID: id, Name: name, RedirectURIs: in.RedirectURIs}); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "保存失败")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"client_id": id, "client_name": name, "redirect_uris": in.RedirectURIs,
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
}

// lookupClient resolves a registered client or fetches a metadata document.
func (s *Server) lookupClient(ctx context.Context, id string) (store.OAuthClient, error) {
	if !strings.HasPrefix(id, "https://") {
		return s.Store.OAuthClientByID(ctx, id)
	}
	// A hub that cannot reach the client's host (for example behind a firewall) turns
	// metadata documents off; the metadata then stops advertising them and clients register.
	if len(s.MetadataHosts) == 0 {
		return store.OAuthClient{}, errors.New("这台服务器没有开启 Client ID Metadata Document，请在客户端里删掉这个连接后重新添加，改用动态注册")
	}
	u, err := url.Parse(id)
	if err != nil || u.User != nil || u.Fragment != "" {
		return store.OAuthClient{}, errors.New("client_id 不是合法 URL")
	}
	host := strings.ToLower(u.Hostname())
	allowed := false
	for _, h := range s.MetadataHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			allowed = true
		}
	}
	if !allowed || net.ParseIP(host) != nil {
		return store.OAuthClient{}, errors.New("这个客户端不在允许列表里，请改用动态注册")
	}
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return store.OAuthClient{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return store.OAuthClient{}, errors.New("读取客户端元数据失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return store.OAuthClient{}, errors.New("读取客户端元数据失败")
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&doc); err != nil {
		return store.OAuthClient{}, errors.New("客户端元数据格式不对")
	}
	if doc.ClientID != id {
		return store.OAuthClient{}, errors.New("客户端元数据与 client_id 不一致")
	}
	return store.OAuthClient{ClientID: id, Name: doc.ClientName, RedirectURIs: doc.RedirectURIs}, nil
}

// authorize validates the request and parks it for the owner. Until redirect_uri
// is proven, errors stop here: bouncing them to an unverified address is exactly
// what an open redirect is.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, err := s.lookupClient(r.Context(), q.Get("client_id"))
	if err != nil {
		http.Error(w, "客户端无效："+err.Error(), http.StatusBadRequest)
		return
	}
	redirect := q.Get("redirect_uri")
	if !validRedirect(redirect) || !redirectMatches(client.RedirectURIs, redirect) {
		http.Error(w, "redirect_uri 与客户端登记的不一致", http.StatusBadRequest)
		return
	}
	bounce := func(kind string) {
		u, _ := url.Parse(redirect)
		v := u.Query()
		v.Set("error", kind)
		if st := q.Get("state"); st != "" {
			v.Set("state", st)
		}
		v.Set("iss", s.Public)
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		bounce("unsupported_response_type")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		bounce("invalid_request")
		return
	}
	resource := q.Get("resource")
	if resource == "" {
		resource = s.Resource()
	}
	if resource != s.Resource() {
		bounce("invalid_target")
		return
	}
	name := client.Name
	if name == "" {
		name = "未命名客户端"
	}
	// Only a well-formed request costs a row, so only that is counted.
	if !s.limit.allow(s.client(r), 60) {
		bounce("temporarily_unavailable")
		return
	}
	id, err := s.Store.SaveOAuthRequest(r.Context(), store.OAuthRequest{ClientID: client.ClientID, ClientName: name, RedirectURI: redirect,
		State: q.Get("state"), CodeChallenge: q.Get("code_challenge"), Scope: q.Get("scope"), Resource: resource, ExpiresAt: s.Store.Now().Add(requestTTL)})
	if err != nil {
		bounce("server_error")
		return
	}
	// A real path, not a #fragment: an identity-aware proxy in front of the console
	// (Cloudflare Access) sends the browser back to the URL it saw, and a fragment never
	// reaches it. The console turns /consent/<id> into its consent page on load.
	http.Redirect(w, r, "/consent/"+id, http.StatusFound)
}

// Approve turns the owner's decision into a code and returns where to send the
// browser. It creates a dedicated agent principal for this grant.
func (s *Server) Approve(ctx context.Context, req store.OAuthRequest, workspaces []string, evidence bool) (string, error) {
	scopes := []string{model.ScopeRead}
	if evidence {
		scopes = append(scopes, model.ScopeEvidence)
	}
	name := fmt.Sprintf("oauth:%s:%s", req.ClientName, req.ID[len(req.ID)-6:])
	p, _, err := s.Store.CreatePrincipal(ctx, model.Principal{Kind: "agent", Name: name, Scopes: scopes, Workspaces: workspaces})
	if err != nil {
		return "", err
	}
	code, err := s.Store.IssueOAuthCode(ctx, req, p.ID, codeTTL)
	if err != nil {
		return "", err
	}
	if err := s.Store.DeleteOAuthRequest(ctx, req.ID); err != nil {
		return "", err
	}
	return s.callback(req, map[string]string{"code": code})
}

// Deny answers no.
func (s *Server) Deny(ctx context.Context, req store.OAuthRequest) (string, error) {
	if err := s.Store.DeleteOAuthRequest(ctx, req.ID); err != nil {
		return "", err
	}
	return s.callback(req, map[string]string{"error": "access_denied"})
}

func (s *Server) callback(req store.OAuthRequest, kv map[string]string) (string, error) {
	u, err := url.Parse(req.RedirectURI)
	if err != nil {
		return "", err
	}
	v := u.Query()
	for k, val := range kv {
		v.Set(k, val)
	}
	if req.State != "" {
		v.Set("state", req.State)
	}
	v.Set("iss", s.Public)
	u.RawQuery = v.Encode()
	return u.String(), nil
}

func challengeOf(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "请求格式不对")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r)
	case "refresh_token":
		s.refresh(w, r)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "只支持 authorization_code 和 refresh_token")
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm
	c, err := s.Store.ConsumeOAuthCode(r.Context(), f.Get("code"))
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "授权码无效或已使用")
		return
	}
	bad := f.Get("client_id") != c.ClientID || f.Get("redirect_uri") != c.RedirectURI
	if res := f.Get("resource"); res != "" && res != c.Resource {
		bad = true
	}
	verifier := f.Get("code_verifier")
	if len(verifier) < 43 || subtle.ConstantTimeCompare([]byte(challengeOf(verifier)), []byte(c.CodeChallenge)) != 1 {
		bad = true
	}
	if bad {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "授权码与请求不匹配")
		return
	}
	s.issue(w, r, store.OAuthGrant{ClientID: c.ClientID, Resource: c.Resource, PrincipalID: c.PrincipalID})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	access, refresh, err := s.Store.RefreshOAuth(r.Context(), r.PostForm.Get("refresh_token"), r.PostForm.Get("client_id"), accessTTL, refreshTTL)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "刷新令牌无效")
		return
	}
	s.tokens(w, access, refresh)
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request, g store.OAuthGrant) {
	access, refresh, err := s.Store.IssueOAuthTokens(r.Context(), g, accessTTL, refreshTTL)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "签发失败")
		return
	}
	s.tokens(w, access, refresh)
}

func (s *Server) tokens(w http.ResponseWriter, access, refresh string) {
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(accessTTL.Seconds()), "refresh_token": refresh})
}
