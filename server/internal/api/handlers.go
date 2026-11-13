package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Feianxc/tianshu/server/internal/catalog"
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
	"github.com/Feianxc/tianshu/server/internal/view"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.limit.blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "失败次数过多，请稍后再试")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	v, err := s.st.Authenticate(r.Context(), strings.TrimSpace(in.Token))
	if err != nil || v.Kind != "human" {
		s.limit.fail(ip)
		writeError(w, http.StatusUnauthorized, "unauthorized", "令牌无效")
		return
	}
	tok, err := s.st.CreateSession(r.Context(), v.PrincipalID, sessionTTL)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: s.cfg.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	s.audit1(r, v, "session.login", v.Name, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = s.st.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, v model.Viewer) {
	scopes := []string{}
	for _, sc := range model.AllScopes {
		if v.Has(sc) {
			scopes = append(scopes, sc)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": v.Name, "kind": v.Kind, "scopes": scopes, "all_workspaces": v.All})
}

// workspaceParam resolves a slug to an id. An unknown slug and one outside the
// viewer's reach behave the same: a narrowing that matches nothing.
func (s *Server) workspaceParam(r *http.Request) (string, bool, error) {
	slug := r.URL.Query().Get("workspace")
	if slug == "" {
		return "", false, nil
	}
	ids, err := s.st.WorkspaceIDsBySlug(r.Context())
	if err != nil {
		return "", false, err
	}
	id, ok := ids[slug]
	if !ok {
		return "ws_none", true, nil
	}
	return id, true, nil
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	ws, _, err := s.workspaceParam(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	o, err := s.vw.Overview(r.Context(), v, ws)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) workspaces(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	list, err := s.st.ListWorkspaces(r.Context(), v)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": list})
}

func (s *Server) entities(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	q := r.URL.Query()
	ws, _, err := s.workspaceParam(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := store.EntityFilter{Kind: q.Get("kind"), Workspace: ws, Lifecycle: q.Get("lifecycle"), Tag: q.Get("tag"),
		Q: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit, Attr: map[string]string{}}
	for _, k := range []string{"stage", "device_type", "priority", "environment"} {
		if val := q.Get(k); val != "" {
			f.Attr[k] = val
		}
	}
	cards, page, err := s.vw.Cards(r.Context(), v, f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": cards,
		"next_cursor": page.NextCursor, "has_more": page.HasMore})
}

func (s *Server) entity(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	d, err := s.vw.Get(r.Context(), v, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) createEntity(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		Kind      string         `json:"kind"`
		Workspace string         `json:"workspace"`
		Slug      string         `json:"slug"`
		Name      string         `json:"name"`
		Summary   string         `json:"summary"`
		Lifecycle string         `json:"lifecycle"`
		Attrs     map[string]any `json:"attrs"`
		Tags      []string       `json:"tags"`
	}
	if !decode(w, r, maxJSONBody, &in) {
		return
	}
	ids, err := s.st.WorkspaceIDsBySlug(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	wsID, ok := ids[in.Workspace]
	if !ok || !v.CanSee(wsID) {
		writeError(w, http.StatusNotFound, "not_found", "没有这个空间")
		return
	}
	// Slugs are unique across workspaces. Someone who cannot see all of them gets a
	// suffix, so a conflict can never confirm what exists elsewhere.
	if !v.All && in.Slug != "" {
		in.Slug += "-" + store.NewID("x")[2:8]
	}
	e := model.Entity{Kind: in.Kind, WorkspaceID: wsID, Slug: in.Slug, Name: in.Name, Summary: in.Summary,
		Lifecycle: in.Lifecycle, Attrs: in.Attrs, Tags: in.Tags, Origin: "manual", Confirmed: true}
	if e.Attrs == nil {
		e.Attrs = map[string]any{}
	}
	if err := catalog.Validate(&e); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	created, err := s.st.CreateEntity(r.Context(), e)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "entity.create", created.Kind+"/"+created.Slug, "ok", "")
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) patchEntity(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var p store.EntityPatch
	if !decode(w, r, maxJSONBody, &p) {
		return
	}
	id := r.PathValue("id")
	cur, err := s.st.GetEntity(r.Context(), v, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	merged := cur
	merged.Attrs = map[string]any{}
	for k, val := range cur.Attrs {
		merged.Attrs[k] = val
	}
	for k, val := range p.Attrs {
		if val == nil {
			delete(merged.Attrs, k)
		} else {
			merged.Attrs[k] = val
		}
	}
	if p.Name != nil {
		merged.Name = *p.Name
	}
	if p.Summary != nil {
		merged.Summary = *p.Summary
	}
	if p.Lifecycle != nil {
		merged.Lifecycle = *p.Lifecycle
	}
	if p.Tags != nil {
		merged.Tags = *p.Tags
	}
	if err := catalog.Validate(&merged); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	e, err := s.st.PatchEntity(r.Context(), v, id, p)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "entity.patch", e.Kind+"/"+e.Slug, "ok", "")
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	e, err := s.st.GetEntity(r.Context(), v, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	span := 24 * time.Hour
	switch r.URL.Query().Get("range") {
	case "1h":
		span = time.Hour
	case "6h":
		span = 6 * time.Hour
	case "7d":
		span = 7 * 24 * time.Hour
	case "30d":
		span = 30 * 24 * time.Hour
	}
	now := s.st.Now()
	series, err := s.st.Series(r.Context(), e.ID, now.Add(-span), now, 180)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "from": now.Add(-span), "to": now, "items": series})
}

func (s *Server) addEvidence(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	e, err := s.st.GetEntity(r.Context(), v, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	var x model.Evidence
	if !decode(w, r, maxJSONBody, &x) {
		return
	}
	saved, err := AddEvidence(r.Context(), s.st, v, e, x)
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "evidence.add", e.Kind+"/"+e.Slug, saved.Verdict, "")
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) addRelation(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in model.Relation
	if !decode(w, r, maxJSONBody, &in) {
		return
	}
	if !slices.Contains(model.Relations, in.Type) {
		writeError(w, http.StatusBadRequest, "bad_request", "未知关系类型")
		return
	}
	if err := catalog.CheckText("关系备注", in.Note); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	for _, id := range []string{in.FromID, in.ToID} {
		if _, err := s.st.GetEntity(r.Context(), v, id); err != nil {
			s.fail(w, err)
			return
		}
	}
	in.Origin = "manual"
	if err := s.st.AddRelation(r.Context(), in); err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "relation.add", in.FromID+" "+in.Type+" "+in.ToID, "ok", "")
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) removeRelation(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	q := r.URL.Query()
	from, typ, to := q.Get("from"), q.Get("type"), q.Get("to")
	for _, id := range []string{from, to} {
		if _, err := s.st.GetEntity(r.Context(), v, id); err != nil {
			s.fail(w, err)
			return
		}
	}
	if err := s.st.RemoveRelation(r.Context(), from, typ, to); err != nil {
		s.fail(w, err)
		return
	}
	// A service moved off a host must stop carrying that host's unit results at once,
	// not an hour later when pruning catches up.
	if typ == model.RelRunsOn {
		if dev, err := s.st.GetEntity(r.Context(), model.System(), to); err == nil {
			if err := s.st.DeleteSignals(r.Context(), from, "unit:"+dev.Slug+":", nil); err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	s.audit1(r, v, "relation.remove", from+" "+typ+" "+to, "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) findings(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	q := r.URL.Query()
	fs, err := s.vw.Findings(r.Context(), v, store.FindingFilter{Open: q.Get("all") == "", EntityID: q.Get("entity")})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": fs})
}

func (s *Server) ackFinding(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		SnoozeHours int `json:"snooze_hours"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	// 最长 30 天：再长就等于忘掉它了；不设上限时超大的数还会让时间溢出成过去。
	if in.SnoozeHours < 0 || in.SnoozeHours > 30*24 {
		writeError(w, http.StatusBadRequest, "bad_request", "稍后提醒只能是 1 小时到 30 天")
		return
	}
	var until *time.Time
	if in.SnoozeHours > 0 {
		t := s.st.Now().Add(time.Duration(in.SnoozeHours) * time.Hour)
		until = &t
	}
	if err := s.st.AckFinding(r.Context(), v, r.PathValue("id"), until); err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "finding.ack", r.PathValue("id"), "ok", strconv.Itoa(in.SnoozeHours)+"h")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) discoveries(w http.ResponseWriter, r *http.Request, _ model.Viewer) {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "new"
	}
	list, err := s.st.ListDiscoveries(r.Context(), state)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": list})
}

func (s *Server) decideDiscovery(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		State string `json:"state"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	if in.State != "ignored" && in.State != "adopted" && in.State != "new" {
		writeError(w, http.StatusBadRequest, "bad_request", "未知状态")
		return
	}
	if err := s.st.SetDiscoveryState(r.Context(), r.PathValue("id"), in.State); err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "discovery.decide", r.PathValue("id"), in.State, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) sources(w http.ResponseWriter, r *http.Request, _ model.Viewer) {
	list, err := s.vw.Sources(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": list})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request, _ model.Viewer) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.st.ListAudit(r.Context(), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": list})
}

func (s *Server) principals(w http.ResponseWriter, r *http.Request, _ model.Viewer) {
	list, err := s.st.ListPrincipals(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": view.SchemaVersion, "items": list})
}

func (s *Server) createPrincipal(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		Kind       string   `json:"kind"`
		Name       string   `json:"name"`
		Scopes     []string `json:"scopes"`
		Workspaces []string `json:"workspaces"`
		Devices    []string `json:"devices"`
		TTLDays    int      `json:"ttl_days"`
	}
	if !decode(w, r, 16384, &in) {
		return
	}
	if !slices.Contains([]string{"human", "agent", "collector"}, in.Kind) || strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "身份类型或名称不合法")
		return
	}
	for _, sc := range in.Scopes {
		if !slices.Contains(model.AllScopes, sc) {
			writeError(w, http.StatusBadRequest, "bad_request", "未知权限 "+sc)
			return
		}
		// The store enforces this ceiling on every path; refusing here tells the
		// person asking instead of quietly handing back a weaker token.
		if in.Kind == "agent" && sc != model.ScopeRead && sc != model.ScopeEvidence && sc != model.ScopeLogs {
			writeError(w, http.StatusBadRequest, "bad_request", "Agent 身份只能持有 read、evidence 和 logs 权限")
			return
		}
	}
	p := model.Principal{Kind: in.Kind, Name: in.Name, Scopes: in.Scopes, Workspaces: in.Workspaces, Devices: in.Devices}
	if in.TTLDays > 0 {
		t := s.st.Now().Add(time.Duration(in.TTLDays) * 24 * time.Hour)
		p.ExpiresAt = &t
	}
	created, tok, err := s.st.CreatePrincipal(r.Context(), p)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "principal.create", created.Kind+"/"+created.Name, "ok", strings.Join(in.Scopes, ","))
	writeJSON(w, http.StatusCreated, map[string]any{"principal": created, "token": tok})
}

func (s *Server) revokePrincipal(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	if err := s.st.RevokePrincipal(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "principal.revoke", r.PathValue("id"), "ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) export(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	f, err := catalog.Export(r.Context(), s.st, v)
	if err != nil {
		s.fail(w, err)
		return
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "catalog.export", "", "ok", "")
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="wanglou-catalog.yaml"`)
	_, _ = w.Write(b)
}

func (s *Server) ingestReport(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var rep model.Report
	if !decode(w, r, maxReport, &rep) {
		return
	}
	s.applyReport(w, r, v, rep)
}

func (s *Server) applyReport(w http.ResponseWriter, r *http.Request, v model.Viewer, rep model.Report) {
	if !v.CanReport(rep.Device) {
		writeError(w, http.StatusForbidden, "forbidden", "这个采集身份不能为该设备上报")
		return
	}
	dev, err := s.st.GetEntityBySlug(r.Context(), model.System(), model.KindDevice, rep.Device)
	if err != nil {
		s.fail(w, err)
		return
	}
	stats, err := s.st.Ingest(r.Context(), dev, rep)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": stats})
}

// ingestHeartbeat is the small form for phones and watches, simple enough to send
// from an automation app with no code.
func (s *Server) ingestHeartbeat(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		Device     string   `json:"device"`
		Collector  string   `json:"collector"`
		IntervalS  int64    `json:"interval_s"`
		BatteryPct *float64 `json:"battery_pct"`
		Charging   *bool    `json:"charging"`
		Network    string   `json:"network"`
	}
	if !decode(w, r, 8192, &in) {
		return
	}
	if in.Collector == "" {
		in.Collector = "phone"
	}
	if in.IntervalS == 0 {
		in.IntervalS = 900
	}
	s.applyReport(w, r, v, model.Report{Device: in.Device, Collector: in.Collector, IntervalS: in.IntervalS, ObservedAt: s.st.Now(),
		Metrics: &model.ReportMetric{BatteryPct: in.BatteryPct, Charging: in.Charging, Network: in.Network}})
}
