// This file holds PATCH /credential-types/{id} to the partial-update rules
// the other PATCH routes follow, through the real router and the real
// ent-backed store.
//
// The route used to decode its create body, so a field the body left out was
// written back empty: a PATCH naming only the description was refused for
// the name it blanked, one naming only the name was refused for the kind,
// and one naming the name and kind erased the input schema and the injector
// document. The namespace and the organization were decoded and ignored,
// answering 200 for a change that was never made.
package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// credentialTypePath is the URL of one credential type.
func credentialTypePath(id int) string { return fmt.Sprintf("/api/v1/credential-types/%d", id) }

// readCredentialType GETs a credential type and returns its decoded body.
func (f *credentialFixture) readCredentialType(t *testing.T, id int) map[string]any {
	t.Helper()
	status, body := f.do(t, http.MethodGet, credentialTypePath(id), nil)
	if status != http.StatusOK {
		t.Fatalf("GET credential type %d: status %d, body %s", id, status, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding credential type %d: %v", id, err)
	}
	return got
}

// patchCredentialTypeOK sends a PATCH that must succeed and returns the type
// as GET then shows it.
func (f *credentialFixture) patchCredentialTypeOK(t *testing.T, id int, body string) map[string]any {
	t.Helper()
	if status, resp := f.do(t, http.MethodPatch, credentialTypePath(id), json.RawMessage(body)); status != http.StatusOK {
		t.Fatalf("PATCH %s: status %d, want 200; body %s", body, status, resp)
	}
	return f.readCredentialType(t, id)
}

// credentialTypeRefusedUnchanged sends a PATCH that must be refused with
// want, with a response saying each of mentions, and leave the type as it
// was.
func (f *credentialFixture) credentialTypeRefusedUnchanged(t *testing.T, id int, body string, want int, mentions ...string) {
	t.Helper()
	before := f.readCredentialType(t, id)
	status, resp := f.do(t, http.MethodPatch, credentialTypePath(id), json.RawMessage(body))
	if status != want {
		t.Fatalf("PATCH %s: status %d, want %d; body %s", body, status, want, resp)
	}
	for _, mention := range mentions {
		if !strings.Contains(string(resp), mention) {
			t.Errorf("PATCH %s: the refusal %s does not say %q", body, resp, mention)
		}
	}
	if after := f.readCredentialType(t, id); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused PATCH changed the credential type:\n before %v\n after  %v", before, after)
	}
}

// assertTypeFields fails for each key of want whose value differs.
func assertTypeFields(t *testing.T, got, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
}

// TestCredentialTypePatch_KeepsWhatItDoesNotName is the partial update: a
// PATCH naming one field keeps the name, the kind, the input schema and the
// injector document.
func TestCredentialTypePatch_KeepsWhatItDoesNotName(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchType(t, f.orgA, "patch_api")
	original := f.readCredentialType(t, id)

	got := f.patchCredentialTypeOK(t, id, `{"description":"A token API"}`)
	assertTypeFields(t, got, map[string]any{
		"name": original["name"], "description": "A token API", "kind": "cloud", "namespace": "patch_api",
		"organization": float64(f.orgA), "inputs": original["inputs"], "injectors": original["injectors"],
	})

	got = f.patchCredentialTypeOK(t, id, `{"name":"Patch API renamed"}`)
	assertTypeFields(t, got, map[string]any{
		"name": "Patch API renamed", "description": "A token API", "kind": "cloud",
		"inputs": original["inputs"], "injectors": original["injectors"],
	})

	got = f.patchCredentialTypeOK(t, id, `{"kind":"token"}`)
	assertTypeFields(t, got, map[string]any{"name": "Patch API renamed", "kind": "token", "inputs": original["inputs"]})

	// An empty description clears it; the wire then leaves it out.
	got = f.patchCredentialTypeOK(t, id, `{"description":""}`)
	if _, present := got["description"]; present {
		t.Errorf("description = %v, want it cleared", got["description"])
	}
}

// TestCredentialTypePatch_ReplacesInputsAndInjectorsWhole is what naming a
// document means: inputs or injectors, when named, is the whole new document
// and the other is kept, as PUT .../inputs and PUT .../injectors do.
func TestCredentialTypePatch_ReplacesInputsAndInjectorsWhole(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchType(t, f.orgA, "patch_api")
	original := f.readCredentialType(t, id)

	got := f.patchCredentialTypeOK(t, id, `{"inputs":{"fields":[
		{"id":"token","label":"Token","secret":true},
		{"id":"url","label":"URL"}],"required":["token"]}}`)
	inputs, _ := json.Marshal(got["inputs"])
	if string(inputs) != `{"fields":[{"id":"token","label":"Token","secret":true},{"id":"url","label":"URL"}],"required":["token"]}` {
		t.Errorf("inputs = %s, want exactly the document sent", inputs)
	}
	assertTypeFields(t, got, map[string]any{"injectors": original["injectors"]})

	got = f.patchCredentialTypeOK(t, id, `{"injectors":{"env":{"API_TOKEN":"{{ token }}"}}}`)
	injectors, _ := json.Marshal(got["injectors"])
	if string(injectors) != `{"env":{"API_TOKEN":"{{ token }}"}}` {
		t.Errorf("injectors = %s, want exactly the document sent, API_URL gone", injectors)
	}
	if kept := mustJSON(t, got["inputs"]); string(kept) != string(inputs) {
		t.Errorf("replacing the injectors changed the inputs to %s", kept)
	}
}

// mustJSON marshals v or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCredentialTypePatch_IsValidatedWhole holds the merged type to every
// rule a write is held to: an inputs document dropping an input an injector
// kept still reads is refused, as is an injector naming an input the kept
// schema does not declare, and a managed type still answers 403.
func TestCredentialTypePatch_IsValidatedWhole(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchType(t, f.orgA, "patch_api")
	f.createPatchType(t, f.orgA, "patch_api_taken")

	f.credentialTypeRefusedUnchanged(t, id, `{"inputs":{"fields":[{"id":"url","label":"URL"}]}}`,
		http.StatusBadRequest, "token")
	f.credentialTypeRefusedUnchanged(t, id, `{"injectors":{"env":{"API_ZONE":"{{ zone }}"}}}`,
		http.StatusBadRequest, "zone")
	f.credentialTypeRefusedUnchanged(t, id, `{"injectors":{"env":{"LD_PRELOAD":"{{ token }}"}}}`,
		http.StatusBadRequest, "LD_PRELOAD")
	f.credentialTypeRefusedUnchanged(t, id, `{"kind":"nonsense"}`, http.StatusBadRequest, "kind")
	f.credentialTypeRefusedUnchanged(t, id, `{"name":""}`, http.StatusBadRequest, "name")
	f.credentialTypeRefusedUnchanged(t, id, `{"name":"Patch API patch_api_taken"}`, http.StatusConflict)

	for field, body := range map[string]map[string]any{
		"name":        {"name": "Patch" + nulChar + "API"},
		"description": {"description": "A token" + nulChar},
	} {
		t.Run("a NUL in "+field, func(t *testing.T) {
			f.credentialTypeRefusedUnchanged(t, id, string(mustJSON(t, body)), http.StatusBadRequest, field, "NUL")
		})
	}

	managed, err := f.store.EnsureManagedType(t.Context(), managedTypeForTest())
	if err != nil {
		t.Fatalf("EnsureManagedType: %v", err)
	}
	for _, body := range []string{`{"description":"edited"}`, `{"description":"edited","namespace":"ssh"}`} {
		f.credentialTypeRefusedUnchanged(t, managed.ID, body, http.StatusForbidden)
	}
}

// TestCredentialTypePatch_RefusesNull holds the null rule on every field:
// refused, naming the field, with the valid change beside it not applied.
func TestCredentialTypePatch_RefusesNull(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchType(t, f.orgA, "patch_api")

	for _, field := range []string{"name", "description", "kind", "namespace", "organization", "inputs", "injectors"} {
		t.Run(field, func(t *testing.T) {
			change := `"description":"changed"`
			if field == "description" {
				change = `"name":"changed"`
			}
			f.credentialTypeRefusedUnchanged(t, id, `{`+change+`,"`+field+`":null}`, http.StatusBadRequest,
				`\"`+field+`\"`, "may not be null")
		})
	}
}

// TestCredentialTypePatch_RefusesAChangedNamespaceOrOrganization holds the
// fixed fields: refused when they differ from the stored ones, where they
// used to be ignored with a 200, and accepted as an echo.
func TestCredentialTypePatch_RefusesAChangedNamespaceOrOrganization(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchType(t, f.orgA, "patch_api")

	f.credentialTypeRefusedUnchanged(t, id, `{"description":"changed","namespace":"another_namespace"}`,
		http.StatusBadRequest, "namespace cannot be changed")
	f.credentialTypeRefusedUnchanged(t, id, fmt.Sprintf(`{"description":"changed","organization":%d}`, f.orgB),
		http.StatusBadRequest, "organization cannot be changed")

	got := f.patchCredentialTypeOK(t, id, fmt.Sprintf(`{"description":"echoed","namespace":"patch_api","organization":%d}`, f.orgA))
	assertTypeFields(t, got, map[string]any{"description": "echoed", "namespace": "patch_api", "organization": float64(f.orgA)})
}

// TestCredentialTypePatch_RefusesABodyThatIsNotAnObjectOfKnownFields holds
// the strict decode, inside the documents too.
func TestCredentialTypePatch_RefusesABodyThatIsNotAnObjectOfKnownFields(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.createPatchType(t, f.orgA, "patch_api")

	for name, body := range map[string]string{
		"a bare null":                  `null`,
		"an array":                     `[]`,
		"managed, which no write sets": `{"description":"changed","managed":false}`,
		"a read-only id":               `{"description":"changed","id":1}`,
		"inputs that is an array":      `{"inputs":[]}`,
		"an unknown key in an input":   `{"inputs":{"fields":[{"id":"token","label":"Token","colour":"red"}]}}`,
		"a kind that is not a string":  `{"kind":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			f.credentialTypeRefusedUnchanged(t, id, body, http.StatusBadRequest, "malformed")
		})
	}
}

// TestCredentialTypeCreate_RefusesNUL holds the create path to the same text
// rule: a NUL was stored on SQLite and failed on PostgreSQL.
func TestCredentialTypeCreate_RefusesNUL(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)

	for field, value := range map[string]string{"name": "Patch" + nulChar + "API", "description": "A token" + nulChar} {
		t.Run(field, func(t *testing.T) {
			// Each case has a name and namespace of its own, so a case the
			// old code accepted cannot fail the next as a duplicate.
			body := map[string]any{
				"name": "Patch API " + field, "kind": "cloud", "namespace": "patch_nul_" + field, "organization": f.orgA,
			}
			body[field] = value
			status, resp := f.do(t, http.MethodPost, "/api/v1/credential-types", body)
			if status != http.StatusBadRequest || !strings.Contains(string(resp), field) || !strings.Contains(string(resp), "NUL") {
				t.Errorf("POST with a NUL in %s: %d %s, want 400 naming it", field, status, resp)
			}
		})
	}
}
