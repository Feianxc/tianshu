package api

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Feianxc/tianshu/server/internal/catalog"
	"github.com/Feianxc/tianshu/server/internal/model"
)

func (s *Server) oauthRequest(w http.ResponseWriter, r *http.Request, _ model.Viewer) {
	req, err := s.st.OAuthRequestByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	host := req.RedirectURI
	if u, err := url.Parse(req.RedirectURI); err == nil {
		host = u.Host
	}
	writeJSON(w, http.StatusOK, map[string]any{"client_name": req.ClientName, "client_id": req.ClientID, "redirect_host": host, "scope": req.Scope})
}

// oauthDecide records the answer of the owner. The workspaces come from the owner
// here, never from the client that asked.
func (s *Server) oauthDecide(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		Approve    bool     `json:"approve"`
		Workspaces []string `json:"workspaces"`
		Evidence   bool     `json:"evidence"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	req, err := s.st.OAuthRequestByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	var target string
	if in.Approve {
		if len(in.Workspaces) == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "至少选择一个空间")
			return
		}
		target, err = s.cfg.OAuth.Approve(r.Context(), req, in.Workspaces, in.Evidence)
	} else {
		target, err = s.cfg.OAuth.Deny(r.Context(), req)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	result := "denied"
	if in.Approve {
		result = "approved"
	}
	s.audit1(r, v, "oauth.decide", req.ClientName, result, strings.Join(in.Workspaces, ","))
	writeJSON(w, http.StatusOK, map[string]any{"redirect": target})
}

// adoptDiscovery turns an inbox item into a catalog object in the workspace of its
// device. It stays unconfirmed until a person fills in the rest.
func (s *Server) adoptDiscovery(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	d, err := s.st.DiscoveryByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	dev, err := s.st.GetEntity(r.Context(), v, d.DeviceID)
	if err != nil {
		s.fail(w, err)
		return
	}
	e := model.Entity{WorkspaceID: dev.WorkspaceID, Origin: "discovered", Confirmed: false}
	switch d.Kind {
	case "unit":
		name := strings.TrimSuffix(d.Key, ".service")
		e.Kind, e.Name, e.Slug = model.KindService, name, dev.Slug+"-"+slugify(name)
		e.Attrs = map[string]any{"unit": d.Key, "service_kind": "other"}
	case "repo":
		remote, _ := d.Payload["remote"].(string)
		if remote == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "这个仓库没有远端，先推送到云端再收录")
			return
		}
		name := remote[strings.LastIndex(remote, "/")+1:]
		e.Kind, e.Name, e.Slug = model.KindRepository, name, slugify(remote)
		e.Attrs = map[string]any{"url": "https://" + remote}
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "不能收录这类对象")
		return
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
	if d.Kind == "unit" {
		if err := s.st.AddRelation(r.Context(), model.Relation{FromID: created.ID, Type: model.RelRunsOn, ToID: dev.ID, Origin: "discovered"}); err != nil {
			s.fail(w, err)
			return
		}
	}
	if err := s.st.SetDiscoveryState(r.Context(), d.ID, "adopted"); err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "discovery.adopt", d.Key, "ok", created.ID)
	writeJSON(w, http.StatusCreated, created)
}

var slugBad = regexp.MustCompile(`[^a-z0-9._-]+`)

func slugify(s string) string {
	out := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(s), "-"), "-._")
	if len(out) > 70 {
		out = out[:70]
	}
	return out
}
