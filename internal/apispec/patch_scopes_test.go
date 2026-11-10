// This file holds the route table to the owner's decision of 2026-11-10:
// every PATCH and PUT endpoint, and every write-scoped POST that answers with
// a stored record or judges its body against one, requires the resource's
// read scope as well as its write scope (Endpoint.AlsoScope), and the route
// it builds carries it, so a route added later cannot quietly answer a
// write-only token with a stored record.
package apispec

import (
	"net/http"
	"strings"
	"testing"
)

// recordAnsweringPosts are the POST endpoints a write scope reaches that
// answer with the stored record they act on (a copy returns the template it
// copied, a sync the project it claimed, an attestation the organization or
// team, a type test the stored injectors, a saved configuration the
// template's survey refusals). Each needs its read scope, as a PATCH does.
var recordAnsweringPosts = map[string]bool{
	"copy_template":          true,
	"sync_project":           true,
	"cancel_project_sync":    true,
	"attest_organization":    true,
	"attest_team":            true,
	"test_credential_type":   true,
	"create_template_config": true,
}

// TestEndpoints_EveryPatchAndPutRequiresTheReadScope fails for a PATCH or
// PUT endpoint, or one of recordAnsweringPosts, whose AlsoScope is not the
// read scope of its write scope ("inventory:write" needs "inventory:read"),
// and for any other endpoint that declares one, so the list is kept honest.
func TestEndpoints_EveryPatchAndPutRequiresTheReadScope(t *testing.T) {
	for _, ep := range Endpoints {
		needsRead := ep.Method == http.MethodPatch || ep.Method == http.MethodPut || recordAnsweringPosts[ep.Name]
		switch {
		case needsRead:
			resource, ok := strings.CutSuffix(string(ep.Scope), ":write")
			if !ok {
				t.Errorf("%s %s needs %q, which is not a write scope", ep.Method, ep.Pattern, ep.Scope)
				continue
			}
			if want := resource + ":read"; string(ep.AlsoScope) != want {
				t.Errorf("%s %s AlsoScope = %q, want %q", ep.Method, ep.Pattern, ep.AlsoScope, want)
			}
			if got := ep.Route(func(http.ResponseWriter, *http.Request) {}).AlsoScope; got != ep.AlsoScope {
				t.Errorf("%s %s Route().AlsoScope = %q, want %q", ep.Method, ep.Pattern, got, ep.AlsoScope)
			}
		default:
			if ep.AlsoScope != "" {
				t.Errorf("%s %s declares AlsoScope %q but is not a PATCH, a PUT or one of recordAnsweringPosts", ep.Method, ep.Pattern, ep.AlsoScope)
			}
		}
	}
}
