package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// patchPrincipal changes the scopes of an agent, so a connection made before a scope
// existed can be given it without reconnecting. Only agents, and only within their
// ceiling: read is required, evidence and logs are optional.
func (s *Server) patchPrincipal(w http.ResponseWriter, r *http.Request, v model.Viewer) {
	var in struct {
		Scopes []string `json:"scopes"`
	}
	if !decode(w, r, 4096, &in) {
		return
	}
	if !slices.Contains(in.Scopes, model.ScopeRead) {
		writeError(w, http.StatusBadRequest, "bad_request", "Agent 必须保留 read 权限")
		return
	}
	for _, sc := range in.Scopes {
		if sc != model.ScopeRead && sc != model.ScopeEvidence && sc != model.ScopeLogs {
			writeError(w, http.StatusBadRequest, "bad_request", "Agent 只能持有 read、evidence 和 logs 权限")
			return
		}
	}
	slices.Sort(in.Scopes)
	in.Scopes = slices.Compact(in.Scopes)
	p, err := s.st.SetAgentScopes(r.Context(), r.PathValue("id"), in.Scopes)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit1(r, v, "principal.scopes", p.Kind+"/"+p.Name, "ok", strings.Join(in.Scopes, ","))
	writeJSON(w, http.StatusOK, p)
}
