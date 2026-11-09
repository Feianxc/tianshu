package api

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Feianxc/tianshu/server/internal/catalog"
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/store"
)

// AddEvidence records a verification result on a project. It is shared with the
// MCP tool so both paths enforce the same rules. Evidence is a claim with an
// author: recording an agent's "pass" never changes the project's stage, and the
// record keeps who said it.
func AddEvidence(ctx context.Context, st *store.Store, v model.Viewer, project model.Entity, x model.Evidence) (model.Evidence, error) {
	if project.Kind != model.KindProject {
		return x, fmt.Errorf("%w: 证据只能记录在项目上", store.ErrInvalid)
	}
	if !slices.Contains(model.Verdicts, x.Verdict) {
		return x, fmt.Errorf("%w: 结论只能是 %s", store.ErrInvalid, strings.Join(model.Verdicts, "、"))
	}
	if strings.TrimSpace(x.Scope) == "" {
		return x, fmt.Errorf("%w: 必须写明验证范围", store.ErrInvalid)
	}
	if (x.Verdict == "blocked" || x.Verdict == "not run") && strings.TrimSpace(x.Note) == "" {
		return x, fmt.Errorf("%w: blocked 或 not run 必须写明原因", store.ErrInvalid)
	}
	for _, t := range []string{x.Scope, x.Note, x.Method, x.SourceURL, x.Environment} {
		if err := catalog.CheckText("evidence", t); err != nil {
			return x, fmt.Errorf("%w: %s", store.ErrInvalid, err.Error())
		}
	}
	x.ProjectID = project.ID
	x.ByKind, x.ByName = v.Kind, v.Name
	return st.AddEvidence(ctx, x)
}
