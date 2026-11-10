// This file holds the route table to the owner's decisions of 2026-11-10: a
// route that answers with a stored record, or judges its request against
// one, requires the read scope of every record it reads, beside its own
// write or action scope (Endpoint.AlsoScopes), and the route it builds
// carries them, so a route added later cannot quietly answer a token that
// may not read with a stored record.
//
// The table is derived from what each handler reads and returns, not from
// its HTTP method (LESSONS 435): every PATCH and PUT, the write-scoped POSTs
// that return the record they act on, and the action routes (launch,
// relaunch, cancel, rollback, check, onboard, capability grants, kubeconfig
// import, and a project's create and edit, which read the credential they
// bind). A plain delete answers 204 (its existence answer was left as it is,
// by the owner's decision). Creates that name another resource (a template
// its inventory, a binding its target, a schedule its target's name, a
// credential its type) still answer with that resource's details without its
// read scope; that is open, recorded on FAILURE_PATTERNS 1120, and awaits
// the owner's decision. Launch and rollback also show one bit about the
// runbook (whether it declares serial, whether it changed) without
// runbook:read, recorded there too.
package apispec

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// requiredReads names, for each endpoint that reads a record its own scope
// does not cover, every read scope it must also require. A PATCH or PUT not
// named here needs its resource's read scope, derived from its write scope.
var requiredReads = map[string][]auth.Scope{
	// Write-scoped POSTs that return the record they act on.
	"copy_template":          {auth.ScopeTemplateRead},
	"sync_project":           {auth.ScopeProjectRead},
	"cancel_project_sync":    {auth.ScopeProjectRead},
	"attest_organization":    {auth.ScopeAccessRead},
	"attest_team":            {auth.ScopeAccessRead},
	"test_credential_type":   {auth.ScopeCredentialRead},
	"create_template_config": {auth.ScopeTemplateRead},

	// A PUT that reads a second resource: it binds credentials (credential
	// scopes) to a template it reads first (its kind decides what binds).
	"set_template_credentials": {auth.ScopeCredentialRead, auth.ScopeTemplateRead},

	// Action routes: each reads the records named.
	"cancel_job":        {auth.ScopeJobRead},
	"rollback_job":      {auth.ScopeJobRead, auth.ScopeTemplateRead},
	"relaunch_job":      {auth.ScopeJobRead, auth.ScopeTemplateRead},
	"launch_template":   {auth.ScopeTemplateRead},
	"check_template":    {auth.ScopeTemplateRead},
	"onboard_device":    {auth.ScopeInventoryRead},
	"grant_capability":  {auth.ScopeInventoryRead},
	"revoke_capability": {auth.ScopeInventoryRead},
	"import_kubeconfig": {auth.ScopeInventoryWrite, auth.ScopeInventoryRead},

	// A project binds a credential it reads first (its organization and
	// kind decide whether it may), on create and on edit.
	"create_project": {auth.ScopeCredentialRead},
	"update_project": {auth.ScopeProjectRead, auth.ScopeCredentialRead},
}

// TestEndpoints_EveryRecordReadingRouteRequiresItsReads fails for an endpoint
// whose AlsoScopes are not exactly what requiredReads names (or, for a PATCH
// or PUT it does not name, its resource's read scope), for any other
// endpoint that declares AlsoScopes, and for a route that does not carry
// them, so the list is kept honest in both directions.
func TestEndpoints_EveryRecordReadingRouteRequiresItsReads(t *testing.T) {
	seen := map[string]bool{}
	for _, ep := range Endpoints {
		want, named := requiredReads[ep.Name]
		if named {
			seen[ep.Name] = true
		} else if ep.Method == http.MethodPatch || ep.Method == http.MethodPut {
			resource, ok := strings.CutSuffix(string(ep.Scope), ":write")
			if !ok {
				t.Errorf("%s %s needs %q, which is not a write scope; name its reads in requiredReads", ep.Method, ep.Pattern, ep.Scope)
				continue
			}
			want = []auth.Scope{auth.Scope(resource + ":read")}
		}
		if !slices.Equal(ep.AlsoScopes, want) {
			t.Errorf("%s %s (%s) AlsoScopes = %v, want %v", ep.Method, ep.Pattern, ep.Name, ep.AlsoScopes, want)
		}
		if got := ep.Route(func(http.ResponseWriter, *http.Request) {}).AlsoScopes; !slices.Equal(got, ep.AlsoScopes) {
			t.Errorf("%s %s Route().AlsoScopes = %v, want %v", ep.Method, ep.Pattern, got, ep.AlsoScopes)
		}
	}
	for name := range requiredReads {
		if !seen[name] {
			t.Errorf("requiredReads names %q, which is not an endpoint", name)
		}
	}
}
