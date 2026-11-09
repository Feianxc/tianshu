// Package probe runs active checks. A probe answers one narrow question: did this
// endpoint respond as declared, from here, just now. It does not prove a business
// flow works, and the wording of its results never claims so.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// Result is one run.
type Result struct {
	OK       bool
	Latency  *int64
	Error    string
	Summary  string
	State    string
	Value    map[string]any
	Observed time.Time
}

var client = &http.Client{
	// Redirects are part of the answer; following them would hide a site that
	// now bounces every visitor somewhere else.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	},
}

// Run executes one check.
func Run(ctx context.Context, c model.Check) Result {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutS)*time.Second)
	defer cancel()
	start := time.Now()
	var r Result
	switch c.Kind {
	case "http":
		r = runHTTP(ctx, c)
	case "tcp":
		r = runTCP(ctx, c)
	case "tls":
		r = runTLS(ctx, c)
	default:
		r = Result{Error: "未知探测类型 " + c.Kind, State: model.StateUnknown, Summary: "未知探测类型"}
	}
	ms := time.Since(start).Milliseconds()
	r.Latency = &ms
	r.Observed = start.UTC()
	if r.Value == nil {
		r.Value = map[string]any{}
	}
	r.Value["latency_ms"] = ms
	return r
}

func failure(summary string, err error) Result {
	return Result{OK: false, State: model.StateFail, Summary: summary, Error: clean(err)}
}

// clean reduces a network error to its cause. Raw messages carry the prober's own
// addresses, which have no place in something an agent or a tenant may read.
func clean(err error) string {
	if err == nil {
		return ""
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return "DNS 解析失败"
	}
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(msg, "timeout"):
		return "超时"
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "actively refused"):
		return "连接被拒绝"
	case strings.Contains(msg, "reset by peer"), strings.Contains(msg, "forcibly closed"), strings.Contains(msg, "EOF"):
		return "连接被对端重置"
	case strings.Contains(msg, "x509"), strings.Contains(msg, "certificate"):
		return "证书无效"
	}
	return "连接失败"
}

func runHTTP(ctx context.Context, c model.Check) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Target, nil)
	if err != nil {
		return failure("探测地址无效", err)
	}
	req.Header.Set("User-Agent", "wanglou-probe/1")
	resp, err := client.Do(req)
	if err != nil {
		return failure("无法访问："+clean(err), err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	want := c.ExpectStatus
	if len(want) == 0 {
		want = []int{200, 201, 204, 301, 302, 303, 307, 308}
	}
	v := map[string]any{"status": resp.StatusCode, "url": c.Target}
	if !slices.Contains(want, resp.StatusCode) {
		return Result{State: model.StateFail, Value: v, Error: "HTTP " + strconv.Itoa(resp.StatusCode),
			Summary: fmt.Sprintf("返回 %d，预期 %v", resp.StatusCode, want)}
	}
	if c.ExpectBody != "" && !strings.Contains(string(body), c.ExpectBody) {
		return Result{State: model.StateFail, Value: v, Error: "内容不符", Summary: "返回内容与预期不符"}
	}
	return Result{OK: true, State: model.StateOK, Value: v, Summary: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

func runTCP(ctx context.Context, c model.Check) Result {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return failure("端口不可达："+clean(err), err)
	}
	conn.Close()
	return Result{OK: true, State: model.StateOK, Summary: "端口可达", Value: map[string]any{"target": c.Target}}
}

func runTLS(ctx context.Context, c model.Check) Result {
	host, _, err := net.SplitHostPort(c.Target)
	if err != nil {
		return failure("探测地址无效", err)
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return failure("证书检查连接失败："+clean(err), err)
	}
	defer raw.Close()
	conn := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err := conn.HandshakeContext(ctx); err != nil {
		return failure("TLS 握手失败："+clean(err), err)
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return failure("未收到证书", errors.New("no peer certificate"))
	}
	left := time.Until(certs[0].NotAfter)
	days := int(left.Hours() / 24)
	v := map[string]any{"host": host, "not_after": certs[0].NotAfter.UTC(), "days_left": days, "issuer": certs[0].Issuer.CommonName}
	state := model.StateOK
	switch {
	case days < 14:
		state = model.StateFail
	case days < 30:
		state = model.StateWarn
	}
	return Result{OK: true, State: state, Value: v, Summary: fmt.Sprintf("证书还剩 %d 天", days)}
}

// Scheduler runs due checks and records them as signals.
type Scheduler struct {
	Store *store.Store
	// Vantage names where these probes run from; a probe result only means
	// something together with where it was taken.
	Vantage string
}

// Tick runs everything that is due and waits for it.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	due, err := s.Store.DueChecks(ctx)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, c := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func(c model.Check) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOne(ctx, c)
		}(c)
	}
	wg.Wait()
	return len(due), nil
}

func (s *Scheduler) runOne(ctx context.Context, c model.Check) {
	r := Run(ctx, c)
	streak, err := s.Store.RecordCheck(ctx, c.ID, r.Observed, r.OK, r.Latency, r.Error)
	if err != nil {
		return
	}
	state := r.State
	// One failed attempt is a blip; a site is called failing after two in a row.
	// A certificate close to expiry is a successful probe with a bad answer, so
	// it is not subject to this.
	if !r.OK && streak < 2 {
		state = model.StateUnknown
	}
	r.Value["fail_streak"] = streak
	r.Value["vantage"] = s.Vantage
	_ = s.Store.PutSignal(ctx, model.Signal{
		EntityID: c.EntityID, Name: "check." + c.Kind + ":" + c.Target, State: state, Value: r.Value,
		Summary: r.Summary, Source: "probe:" + s.Vantage, ObservedAt: r.Observed, ReceivedAt: s.Store.Now(),
		StaleAfterS: max(c.IntervalS*3, 180), Error: r.Error,
	})
}
