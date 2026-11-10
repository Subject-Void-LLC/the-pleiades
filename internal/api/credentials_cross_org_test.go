// This file holds the credential routes' cross-organization refusals to the
// rule PATCH /projects/{id} keeps (projects_credential_org_test.go): a write
// naming another organization's record is refused, and the refusal names
// nothing of that record but the id the caller sent.
//
// Each used to quote the other organization's credential type or credential
// by name. credential:write does not imply credential:read, so a token
// holding only the write scope learned names it had no scope to read.
package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// assertNamesNothingOfTheirs checks a write naming another organization's
// record was refused with 403, saying want (which names the id the caller
// sent) and neither the record's name nor its organization's.
func assertNamesNothingOfTheirs(t *testing.T, what string, status int, resp []byte, want string, theirs ...string) {
	t.Helper()
	if status != http.StatusForbidden || !strings.Contains(string(resp), want) {
		t.Errorf("%s: status %d, want 403 saying %q; body %s", what, status, want, resp)
	}
	for _, leak := range append(theirs, "servers") {
		if strings.Contains(string(resp), leak) {
			t.Errorf("%s: the refusal names %q: %s", what, leak, resp)
		}
	}
}

// TestCredentials_ACrossOrganizationRefusalNamesNothingOfTheirs drives each
// of the three refusals through its own route.
func TestCredentials_ACrossOrganizationRefusalNamesNothingOfTheirs(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	theirType := f.createPatchType(t, f.orgB, "their_api")
	theirCredential := f.createPatchCredential(t, map[string]any{
		"name": "their vault login", "organization": f.orgB, "credential_type": theirType,
		"inputs": map[string]string{"token": patchTokenSecret},
	})

	// A credential of the other organization's type.
	status, resp := f.do(t, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "borrowed", "organization": f.orgA, "credential_type": theirType,
		"inputs": map[string]string{"token": patchRotatedSecret},
	})
	assertNamesNothingOfTheirs(t, "a credential of another organization's type", status, resp,
		fmt.Sprintf("the credential type %d belongs", theirType), "Patch API their_api")

	// A template binding the other organization's credential.
	ctx := context.Background()
	inv := f.client.Inventory.Create().SetName("edge").SetOrganizationID(f.orgA).SaveX(ctx)
	tmpl := f.client.Template.Create().
		SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(f.orgA).SetInventoryID(inv.ID).SaveX(ctx)
	status, resp = f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/templates/%d/credentials", tmpl.ID),
		map[string]any{"credentials": []int{theirCredential}})
	assertNamesNothingOfTheirs(t, "binding another organization's credential", status, resp,
		fmt.Sprintf("the credential %d belongs", theirCredential), "their vault login")

	// A credential reading an input through the other organization's one.
	ours := f.createCredential(t, "our api")
	status, resp = f.do(t, http.MethodPut, fmt.Sprintf("/api/v1/credentials/%d/input-sources", ours),
		map[string]any{"input_sources": []map[string]any{{"input_id": "api_token", "source_credential": theirCredential}}})
	assertNamesNothingOfTheirs(t, "a source credential of another organization", status, resp,
		fmt.Sprintf("the source credential %d belongs", theirCredential), "their vault login")
}
