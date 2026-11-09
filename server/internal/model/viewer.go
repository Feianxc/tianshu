package model

// Scopes a principal can hold.
const (
	ScopeRead     = "read"     // catalog and status
	ScopeWrite    = "write"    // edit catalog objects
	ScopeEvidence = "evidence" // submit verification evidence
	ScopeIngest   = "ingest"   // report as a collector
	ScopeAdmin    = "admin"    // tokens, audit, export
)

// AllScopes is every valid scope.
var AllScopes = []string{ScopeRead, ScopeWrite, ScopeEvidence, ScopeIngest, ScopeAdmin}

// Viewer is the resolved authorization context of one request. It is built only
// from the credential presented, never from a client-supplied workspace id, and
// every store query that returns catalog data takes one.
type Viewer struct {
	PrincipalID string
	Name        string
	Kind        string
	Scopes      map[string]bool
	All         bool            // every workspace
	Workspaces  map[string]bool // workspace ids
	AllDevices  bool
	Devices     map[string]bool // device slugs
}

// System is the viewer of background jobs, which see everything but never serve
// a response directly.
func System() Viewer {
	return Viewer{PrincipalID: "system", Name: "system", Kind: "system", All: true, AllDevices: true,
		Scopes: map[string]bool{ScopeRead: true, ScopeWrite: true, ScopeAdmin: true, ScopeIngest: true, ScopeEvidence: true}}
}

// Has reports whether the viewer holds a scope.
func (v Viewer) Has(scope string) bool { return v.Scopes[scope] }

// CanSee reports whether the viewer may read a workspace.
func (v Viewer) CanSee(workspaceID string) bool { return v.All || v.Workspaces[workspaceID] }

// CanReport reports whether a collector may report for a device slug.
func (v Viewer) CanReport(deviceSlug string) bool {
	return v.Has(ScopeIngest) && (v.AllDevices || v.Devices[deviceSlug])
}
