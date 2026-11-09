package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// 设备配对：新设备（手机）发起请求，已经登录的电脑批准，新设备拿到属于批准人的会话。
// 新设备从头到尾不接触令牌。两边显示同一个代码，批准前要核对一致，
// 防止把别人同时发起的请求当成自己的批准掉。
//
// 请求只放在内存里：5 分钟过期，用一次就作废，服务重启后未完成的请求全部失效。
// 批准时不发会话，等新设备凭自己手里的密钥来取，所以没人取的批准不会留下会话。

const (
	pairTTL        = 5 * time.Minute
	maxPairPending = 16
	maxPairPerIP   = 4
	pairAlphabet   = "23456789ABCDEFGHJKMNPQRSTUVWXYZ" // 去掉了容易看错的 0 1 I L O
)

type pairing struct {
	id, code, ua, ip string
	secret           [32]byte
	created, expires time.Time
	state            string // pending, approved, denied
	approver         model.Viewer
	ttl              time.Duration
}

type pairBook struct {
	mu sync.Mutex
	m  map[string]*pairing
}

func newPairBook() *pairBook { return &pairBook{m: map[string]*pairing{}} }

// sweep 删除过期的请求。调用方持有锁。
func (b *pairBook) sweep(now time.Time) {
	for k, p := range b.m {
		if now.After(p.expires) {
			delete(b.m, k)
		}
	}
}

func randomString(n int, alphabet string) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[k.Int64()]
	}
	return string(out), nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// pairRequest 由新设备调用，不需要登录。
func (s *Server) pairRequest(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limit.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "失败次数过多，请稍后再试")
		return
	}
	now := s.st.Now()
	id, err := randomToken(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	secret, err := randomToken(32)
	if err != nil {
		s.fail(w, err)
		return
	}
	code, err := randomString(8, pairAlphabet)
	if err != nil {
		s.fail(w, err)
		return
	}
	ua := r.UserAgent()
	if len(ua) > 240 {
		ua = ua[:240]
	}
	p := &pairing{id: id, code: code[:4] + "-" + code[4:], ua: ua, ip: ip, secret: sha256.Sum256([]byte(secret)),
		created: now, expires: now.Add(pairTTL), state: "pending"}

	b := s.pairs
	b.mu.Lock()
	b.sweep(now)
	same := 0
	for _, x := range b.m {
		if x.ip == ip {
			same++
		}
	}
	if len(b.m) >= maxPairPending || same >= maxPairPerIP {
		b.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "rate_limited", "等待批准的请求太多，请几分钟后再试")
		return
	}
	b.m[id] = p
	b.mu.Unlock()

	s.audit1(r, model.Viewer{Name: "未登录设备"}, "pairing.request", p.code, "ok", ua)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "code": p.code, "secret": secret, "expires_at": p.expires})
}

// pairPoll 由新设备轮询。批准后的第一次轮询拿到会话 Cookie，之后这个请求就不存在了。
func (s *Server) pairPoll(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limit.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "失败次数过多，请稍后再试")
		return
	}
	var in struct {
		Secret string `json:"secret"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	now := s.st.Now()
	b := s.pairs
	b.mu.Lock()
	b.sweep(now)
	p := b.m[r.PathValue("id")]
	sum := sha256.Sum256([]byte(in.Secret))
	if p == nil || subtle.ConstantTimeCompare(sum[:], p.secret[:]) != 1 {
		b.mu.Unlock()
		s.limit.fail(ip)
		writeError(w, http.StatusNotFound, "not_found", "这个配对请求不存在或已过期，请重新发起")
		return
	}
	state, approver, ttl, code := p.state, p.approver, p.ttl, p.code
	if state != "pending" {
		delete(b.m, p.id)
	}
	b.mu.Unlock()

	switch state {
	case "pending":
		writeJSON(w, http.StatusOK, map[string]any{"state": "pending", "expires_at": p.expires})
	case "denied":
		writeJSON(w, http.StatusOK, map[string]any{"state": "denied"})
	case "approved":
		tok, err := s.st.CreateSession(r.Context(), approver.PrincipalID, ttl)
		if err != nil {
			s.fail(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: s.cfg.Secure,
			SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
		s.audit1(r, approver, "session.pair", code, "ok", "")
		writeJSON(w, http.StatusOK, map[string]any{"state": "approved"})
	}
}

// 只有能管理全部空间的人才能看和批准配对请求：批准等于把自己的身份交给那台设备。
func pairApprover(w http.ResponseWriter, v model.Viewer) bool {
	if v.Kind != "human" || !v.All {
		writeError(w, http.StatusForbidden, "forbidden", "只有管理员本人能批准设备登录")
		return false
	}
	return true
}

func (s *Server) pairList(w http.ResponseWriter, _ *http.Request, v model.Viewer) {
	if !pairApprover(w, v) {
		return
	}
	now := s.st.Now()
	b := s.pairs
	b.mu.Lock()
	b.sweep(now)
	items := []map[string]any{}
	for _, p := range b.m {
		if p.state == "pending" {
			items = append(items, map[string]any{"id": p.id, "code": p.code, "user_agent": p.ua, "ip": p.ip, "created_at": p.created, "expires_at": p.expires})
		}
	}
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) pairDecide(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	if !pairApprover(w, v) {
		return
	}
	var in struct {
		Approve bool `json:"approve"`
		Hours   int  `json:"hours"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	ttl := sessionTTL
	switch in.Hours {
	case 0, 12:
	case 24 * 7:
		ttl = 7 * 24 * time.Hour
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "有效期只能是 12 小时或 7 天")
		return
	}
	now := s.st.Now()
	b := s.pairs
	b.mu.Lock()
	b.sweep(now)
	p := b.m[r.PathValue("id")]
	if p == nil || p.state != "pending" {
		b.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", "这个配对请求不存在、已过期或已处理")
		return
	}
	action := "pairing.deny"
	if in.Approve {
		p.state, p.approver, p.ttl, action = "approved", v, ttl, "pairing.approve"
	} else {
		p.state = "denied"
	}
	code, ua := p.code, p.ua
	b.mu.Unlock()
	s.audit1(r, v, action, code, "ok", ua)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
