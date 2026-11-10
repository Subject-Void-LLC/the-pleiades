// This file holds the web UI to the owner's decision of 2026-11-10, which
// the JSON API enforces on its router: a resource's write scope alone does
// not let a session read a stored record. The UI reads the same records in
// process, not through the API router, so it checks an endpoint's AlsoScopes
// itself (web's permitsEndpoint). Without that, a token holding only
// inventory:write, signed in at the UI, read any inventory through its edit
// form, the leak the API now refuses on PATCH.
package resources_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// writeOnlyInventoryIdentity holds inventory:write and not inventory:read.
// Operator, not admin: an admin passes every scope check whatever its token
// carries, so the assertion would pass with the gate deleted.
var writeOnlyInventoryIdentity = &auth.Identity{
	Subject: "conformance-writer",
	Role:    auth.RoleOperator,
	Scopes:  []auth.Scope{auth.ScopeInventoryWrite},
}

// TestUI_AWriteScopeAloneCannotOpenAnEditForm refuses the edit form, and the
// save it posts to, to a write-only session, with a page that shows nothing
// of the record, and keeps both open to a session holding read and write.
func TestUI_AWriteScopeAloneCannotOpenAnEditForm(t *testing.T) {
	writer := newHarness(t, writeOnlyInventoryIdentity)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var w interface {
			Result() *http.Response
		}
		switch method {
		case http.MethodGet:
			w = writer.get(t, "/ui/inventories/1/edit")
		default:
			w = writer.post(t, "/ui/inventories/1", map[string]string{"name": "production"})
		}
		resp := w.Result()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as a write-only session = %d, want 403", method, resp.StatusCode)
		}
	}
	if page := writer.get(t, "/ui/inventories/1/edit").Body.String(); strings.Contains(page, "The fleet that matters.") {
		t.Error("the refused edit form showed the stored description")
	}

	both := newHarness(t, &auth.Identity{
		Subject: "conformance-operator",
		Role:    auth.RoleOperator,
		Scopes:  []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite},
	})
	if w := both.get(t, "/ui/inventories/1/edit"); w.Code != http.StatusOK {
		t.Errorf("the edit form for read and write = %d, want 200", w.Code)
	}
}
