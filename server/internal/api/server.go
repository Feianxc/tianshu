// Package api is the REST surface. Authorization is decided here from the
// credential alone and then carried as a model.Viewer into every store and view
// call; no handler reads a workspace from the client and trusts it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/oauth"
	"github.com/Feianxc/tianshu/server/internal/store"
	"github.com/Feianxc/tianshu/server/internal/view"
)

const (
	cookieName  = "wl_session"
	sessionTTL  = 12 * time.Hour
	maxJSONBody = 1 << 20
	maxReport   = 4 << 20
)

// Config configures a Server.
type Config struct {
	Store   *store.Store
	View    *view.Service
	MCP     http.Handler // mounted at /mcp; handles its own authentication
	OAuth   *oauth.Server
	Web     http.Handler // the console; nil serves only the API
	TrustCF bool         // take the client address from CF-Connecting-IP
	Secure  bool         // mark the session cookie Secure
	Log     *log.Logger
}

// Server is the HTTP application.
type Server struct {
	cfg    Config
	st     *store.Store
	vw     *view.Service
	limit  *failLimiter
	pairs  *pairBook
	logger *log.Logger
}

// New builds a Server.
func New(cfg Config) *Server {
	l := cfg.Log
	if l == nil {
		l = log.New(discard{}, "", 0)
	}
	s := &Server{cfg: cfg, st: cfg.Store, vw: cfg.View, limit: newFailLimiter(), pairs: newPairBook(), logger: l}
	if cfg.OAuth != nil {
		cfg.OAuth.ClientIP = s.clientIP
	}
	return s
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Handler returns the routed application.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/session", s.login)
	mux.HandleFunc("DELETE /api/v1/session", s.logout)
	// 新设备发起和轮询配对不需要登录；看和批准配对需要管理员本人。
	mux.HandleFunc("POST /api/v1/pairing", s.pairRequest)
	mux.HandleFunc("POST /api/v1/pairing/{id}/poll", s.pairPoll)

	r := func(pattern, scope string, h func(http.ResponseWriter, *http.Request, model.Viewer)) {
		mux.HandleFunc(pattern, s.guard(scope, h))
	}
	// Tokens, audit, feeds and the inbox span every workspace, so holding the admin
	// scope is not enough: the caller has to be able to see all of them.
	ra := func(pattern string, h func(http.ResponseWriter, *http.Request, model.Viewer)) {
		r(pattern, model.ScopeAdmin, func(w http.ResponseWriter, req *http.Request, v model.Viewer) {
			if !v.All {
				writeError(w, http.StatusForbidden, "forbidden", "这项操作需要全部空间的管理权限")
				return
			}
			h(w, req, v)
		})
	}
	r("GET /api/v1/me", "", s.me)
	r("GET /api/v1/overview", model.ScopeRead, s.overview)
	r("GET /api/v1/workspaces", model.ScopeRead, s.workspaces)
	r("GET /api/v1/entities", model.ScopeRead, s.entities)
	r("POST /api/v1/entities", model.ScopeWrite, s.createEntity)
	r("GET /api/v1/entities/{id}", model.ScopeRead, s.entity)
	r("PATCH /api/v1/entities/{id}", model.ScopeWrite, s.patchEntity)
	r("GET /api/v1/entities/{id}/metrics", model.ScopeRead, s.metrics)
	r("POST /api/v1/entities/{id}/evidence", model.ScopeEvidence, s.addEvidence)
	r("POST /api/v1/relations", model.ScopeWrite, s.addRelation)
	r("DELETE /api/v1/relations", model.ScopeWrite, s.removeRelation)
	r("GET /api/v1/findings", model.ScopeRead, s.findings)
	r("POST /api/v1/findings/{id}/ack", model.ScopeWrite, s.ackFinding)
	ra("GET /api/v1/discoveries", s.discoveries)
	ra("POST /api/v1/discoveries/{id}", s.decideDiscovery)
	ra("POST /api/v1/discoveries/{id}/adopt", s.adoptDiscovery)
	ra("GET /api/v1/sources", s.sources)
	ra("GET /api/v1/audit", s.audit)
	ra("GET /api/v1/principals", s.principals)
	ra("POST /api/v1/principals", s.createPrincipal)
	ra("DELETE /api/v1/principals/{id}", s.revokePrincipal)
	r("GET /api/v1/pairings", model.ScopeAdmin, s.pairList)
	r("POST /api/v1/pairings/{id}", model.ScopeAdmin, s.pairDecide)
	r("GET /api/v1/export", model.ScopeAdmin, s.export)
	r("POST /api/v1/ingest/report", model.ScopeIngest, s.ingestReport)
	r("POST /api/v1/ingest/heartbeat", model.ScopeIngest, s.ingestHeartbeat)

	if s.cfg.MCP != nil {
		mux.Handle("/mcp", s.cfg.MCP)
	}
	if s.cfg.OAuth != nil {
		s.cfg.OAuth.Mount(mux)
		ra("GET /api/v1/oauth/requests/{id}", s.oauthRequest)
		ra("POST /api/v1/oauth/requests/{id}", s.oauthDecide)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "没有这个接口")
	})
	if s.cfg.Web != nil {
		mux.Handle("/", s.cfg.Web)
	}
	return s.headers(mux)
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if s.cfg.Secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the address used for rate limits and audit.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustCF {
		if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// authenticate resolves the caller. A bearer token wins over a cookie. Cookie
// sessions must also send a custom header on writes, which a cross-site form
// cannot do.
func (s *Server) authenticate(r *http.Request) (model.Viewer, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return model.Viewer{}, store.ErrNotFound
		}
		return s.st.Authenticate(r.Context(), strings.TrimSpace(tok))
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return model.Viewer{}, store.ErrNotFound
	}
	v, err := s.st.SessionViewer(r.Context(), c.Value)
	if err != nil {
		return v, err
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Wanglou") != "1" {
		return model.Viewer{}, errCSRF
	}
	return v, nil
}

var errCSRF = errors.New("missing anti-forgery header")

func (s *Server) guard(scope string, h func(http.ResponseWriter, *http.Request, model.Viewer)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := s.authenticate(r)
		switch {
		case errors.Is(err, errCSRF):
			writeError(w, http.StatusForbidden, "forbidden", "缺少请求标记")
			return
		case err != nil:
			writeError(w, http.StatusUnauthorized, "unauthorized", "需要登录")
			return
		}
		if scope != "" && !v.Has(scope) {
			writeError(w, http.StatusForbidden, "forbidden", "当前身份没有这项权限")
			return
		}
		h(w, r, v)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": kind, "message": msg}})
}

// fail maps a store error to the wire without leaking internals.
func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "没有这个对象")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "对象已被修改或已存在，请刷新后重试")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, context.Canceled):
	default:
		s.logger.Printf("internal: %v", err)
		writeError(w, http.StatusInternalServerError, "internal", "服务内部错误")
	}
}

func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "请求过大")
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", "请求不是合法 JSON")
		}
		return false
	}
	return true
}

func (s *Server) audit1(r *http.Request, v model.Viewer, action, target, result, detail string) {
	if err := s.st.Audit(r.Context(), v, action, target, result, s.clientIP(r), detail); err != nil {
		s.logger.Printf("audit: %v", err)
	}
}

// failLimiter slows repeated failed logins per address. Tokens are unguessable,
// so this exists to keep noise out of the logs rather than to stop guessing.
type failLimiter struct {
	mu sync.Mutex
	m  map[string]*failCount
}

type failCount struct {
	n     int
	reset time.Time
}

func newFailLimiter() *failLimiter { return &failLimiter{m: map[string]*failCount{}} }

func (l *failLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.m[ip]
	return c != nil && c.n >= 10 && time.Now().Before(c.reset)
}

func (l *failLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	c := l.m[ip]
	if c == nil || now.After(c.reset) {
		c = &failCount{reset: now.Add(5 * time.Minute)}
		l.m[ip] = c
	}
	c.n++
	if len(l.m) > 4096 {
		for k, v := range l.m {
			if now.After(v.reset) {
				delete(l.m, k)
			}
		}
	}
}
