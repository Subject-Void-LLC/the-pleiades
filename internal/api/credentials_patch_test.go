// This file holds PATCH /credentials/{id} to the partial-update rules the
// template, access and project PATCHes follow, through the real router and
// the real ent-backed store with the envelope encryption hooks installed.
//
// The route used to decode its create body, so a field the body left out was
// written back empty: a PATCH that did not name external erased every
// external secret reference the credential held, one that did not name the
// description blanked it, and one that did not name the name failed at the
// database with a 500.
//
// Every stored value is read back through the ent client, which decrypts
// through the same interceptor the Controller registers, never through the
// API, whose reads only ever show the redaction marker.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// The secrets these tests hunt for in raw bytes, each distinctive enough
// that finding one is a real result.
const (
	patchTokenSecret   = "sk-live-PATCH-token-1a2b3c4d5e6f"
	patchBackupSecret  = "sk-live-PATCH-backup-7a8b9c0d1e2f"
	patchRotatedSecret = "sk-live-PATCH-rotated-3a4b5c6d7e8f"
)

// patchSecrets lists every secret a credential in these tests ever holds.
var patchSecrets = []string{patchTokenSecret, patchBackupSecret, patchRotatedSecret}

// nulChar is a NUL, built from its code point rather than written as an
// escape a tool could turn into the real byte in this file.
var nulChar = string(rune(0))

// credentialPath is the URL of one credential.
func credentialPath(id int) string { return fmt.Sprintf("/api/v1/credentials/%d", id) }

// createPatchType creates a credential type in orgID with two secret inputs
// (token, required, and backup, optional) and two plain ones (url, and
// region, bounded to east or west), and returns its id.
func (f *credentialFixture) createPatchType(t *testing.T, orgID int, namespace string) int {
	t.Helper()
	status, body := f.do(t, http.MethodPost, "/api/v1/credential-types", map[string]any{
		"name":         "Patch API " + namespace,
		"description":  "An API reached with a token",
		"kind":         "cloud",
		"namespace":    namespace,
		"organization": orgID,
		"inputs": map[string]any{
			"fields": []map[string]any{
				{"id": "token", "label": "Token", "secret": true},
				{"id": "backup", "label": "Backup token", "secret": true},
				{"id": "url", "label": "URL"},
				{"id": "region", "label": "Region", "choices": []string{"east", "west"}},
			},
			"required": []string{"token"},
		},
		"injectors": map[string]any{
			"env": map[string]string{"API_TOKEN": "{{ token }}", "API_URL": "{{ url }}"},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating the credential type: %d %s", status, body)
	}
	return decodeID(t, body)
}

// createPatchCredential posts a credential that must be accepted and
// returns its id.
func (f *credentialFixture) createPatchCredential(t *testing.T, body map[string]any) int {
	t.Helper()
	status, resp := f.do(t, http.MethodPost, "/api/v1/credentials", body)
	if status != http.StatusCreated {
		t.Fatalf("creating the credential: %d %s", status, resp)
	}
	return decodeID(t, resp)
}

// decodeID reads the id of a created record.
func decodeID(t *testing.T, body []byte) int {
	t.Helper()
	var decoded struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decoding the created record: %v", err)
	}
	return decoded.ID
}

// edgeCredential creates the credential most tests here edit: both secrets
// stored, a region, and the backup also read from an external reference,
// which only a secret input may be.
func (f *credentialFixture) edgeCredential(t *testing.T, typeID int) int {
	t.Helper()
	return f.createPatchCredential(t, map[string]any{
		"name":            "edge api",
		"description":     "Edge cluster API",
		"organization":    f.orgA,
		"credential_type": typeID,
		"inputs":          map[string]string{"token": patchTokenSecret, "backup": patchBackupSecret, "region": "east"},
		"external":        map[string]string{"backup": "file:edge_backup"},
	})
}

// storedCredential is what the database holds for one credential, its
// inputs decrypted by the interceptor the Controller registers.
type storedCredential struct {
	Name, Description string
	Inputs, External  map[string]string
}

// stored reads one credential's row through the ent client.
func (f *credentialFixture) stored(t *testing.T, id int) storedCredential {
	t.Helper()
	row := f.client.Credential.GetX(context.Background(), id)
	return storedCredential{Name: row.Name, Description: row.Description, Inputs: row.Inputs, External: row.External}
}

// readCredential GETs a credential and returns its decoded body.
func (f *credentialFixture) readCredential(t *testing.T, id int) map[string]any {
	t.Helper()
	status, body := f.do(t, http.MethodGet, credentialPath(id), nil)
	if status != http.StatusOK {
		t.Fatalf("GET credential %d: status %d, body %s", id, status, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding credential %d: %v", id, err)
	}
	return got
}

// patchCredential sends a raw PATCH body and fails if any response carries
// a secret.
func (f *credentialFixture) patchCredential(t *testing.T, id int, body string) (int, []byte) {
	t.Helper()
	status, resp := f.do(t, http.MethodPatch, credentialPath(id), json.RawMessage(body))
	assertNoSecret(t, body, resp)
	return status, resp
}

// assertNoSecret fails when resp carries any secret these tests store.
func assertNoSecret(t *testing.T, request string, resp []byte) {
	t.Helper()
	for _, secret := range patchSecrets {
		if bytes.Contains(resp, []byte(secret)) {
			t.Errorf("the response to %s put a secret on the wire: %s", request, resp)
		}
	}
}

// patchCredentialOK sends a PATCH that must succeed.
func (f *credentialFixture) patchCredentialOK(t *testing.T, id int, body string) {
	t.Helper()
	if status, resp := f.patchCredential(t, id, body); status != http.StatusOK {
		t.Fatalf("PATCH %s: status %d, want 200; body %s", body, status, resp)
	}
}

// credentialRefusedUnchanged sends a PATCH that must be refused with want,
// with a response saying each of mentions, and leave both what GET shows and
// what the database holds as they were.
func (f *credentialFixture) credentialRefusedUnchanged(t *testing.T, id int, body string, want int, mentions ...string) {
	t.Helper()
	before, storedBefore := f.readCredential(t, id), f.stored(t, id)
	status, resp := f.patchCredential(t, id, body)
	if status != want {
		t.Fatalf("PATCH %s: status %d, want %d; body %s", body, status, want, resp)
	}
	for _, mention := range mentions {
		if !strings.Contains(string(resp), mention) {
			t.Errorf("PATCH %s: the refusal %s does not say %q", body, resp, mention)
		}
	}
	if after := f.readCredential(t, id); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused PATCH changed what GET shows:\n before %v\n after  %v", before, after)
	}
	if storedAfter := f.stored(t, id); !reflect.DeepEqual(storedAfter, storedBefore) {
		t.Errorf("a refused PATCH changed the stored credential:\n before %+v\n after  %+v", storedBefore, storedAfter)
	}
}

// TestCredentialPatch_KeepsWhatItDoesNotName is the partial update: a PATCH
// naming one field keeps every other, the external references and every
// stored secret among them.
func TestCredentialPatch_KeepsWhatItDoesNotName(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.edgeCredential(t, f.createPatchType(t, f.orgA, "patch_api"))
	want := storedCredential{
		Name: "edge api", Description: "Edge and core API",
		Inputs:   map[string]string{"token": patchTokenSecret, "backup": patchBackupSecret, "region": "east"},
		External: map[string]string{"backup": "file:edge_backup"},
	}

	f.patchCredentialOK(t, id, `{"description":"Edge and core API"}`)
	if got := f.stored(t, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("after naming only the description:\n got  %+v\n want %+v", got, want)
	}

	f.patchCredentialOK(t, id, `{"name":"edge api renamed"}`)
	want.Name = "edge api renamed"
	if got := f.stored(t, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("after naming only the name:\n got  %+v\n want %+v", got, want)
	}

	// inputs merges, as it always did: an input left out keeps its stored
	// value and the marker keeps a secret, so editing one input touches no
	// other.
	f.patchCredentialOK(t, id, `{"inputs":{"region":"west","token":"`+redact.Marker+`"}}`)
	want.Inputs = map[string]string{"token": patchTokenSecret, "backup": patchBackupSecret, "region": "west"}
	if got := f.stored(t, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("after naming one input:\n got  %+v\n want %+v", got, want)
	}

	// An empty value clears what can be empty: the description, and an
	// optional input, a secret one included.
	f.patchCredentialOK(t, id, `{"description":"","inputs":{"backup":""}}`)
	want.Description = ""
	want.Inputs = map[string]string{"token": patchTokenSecret, "backup": "", "region": "west"}
	if got := f.stored(t, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("after clearing the description and the backup:\n got  %+v\n want %+v", got, want)
	}

	// What GET shows is the redacted read: the stored secret as the marker,
	// the cleared one not at all, and the references as they are.
	got := f.readCredential(t, id)
	if inputs := got["inputs"].(map[string]any); inputs["token"] != redact.Marker || inputs["backup"] != nil {
		t.Errorf("GET shows inputs %v, want token as the marker and no backup", inputs)
	}
	if external := got["external"].(map[string]any); external["backup"] != "file:edge_backup" {
		t.Errorf("GET shows external %v, want the backup reference kept", external)
	}
}

// TestCredentialPatch_ReplacesExternalWholeWhenNamed is how a reference is
// removed: external, when named, is the whole new set, so an entry left out
// of it is removed and {} removes them all, while the credential as it would
// be saved must still supply every required input.
func TestCredentialPatch_ReplacesExternalWholeWhenNamed(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	// token, which is required, is supplied only by its reference.
	id := f.createPatchCredential(t, map[string]any{
		"name": "referenced", "organization": f.orgA, "credential_type": typeID,
		"inputs":   map[string]string{"backup": patchBackupSecret},
		"external": map[string]string{"token": "file:edge_token", "backup": "file:edge_backup"},
	})

	f.patchCredentialOK(t, id, `{"external":{"token":"file:edge_token"}}`)
	if got := f.stored(t, id).External; !reflect.DeepEqual(got, map[string]string{"token": "file:edge_token"}) {
		t.Fatalf("after leaving the backup out of external, it holds %v", got)
	}
	f.patchCredentialOK(t, id, `{"external":{"token":"file:rotated_token"}}`)
	if got := f.stored(t, id).External; !reflect.DeepEqual(got, map[string]string{"token": "file:rotated_token"}) {
		t.Fatalf("after replacing the token reference, external holds %v", got)
	}

	// Removing the only thing supplying a required input is refused, since
	// the merged credential is held to the type whole.
	f.credentialRefusedUnchanged(t, id, `{"external":{}}`, http.StatusBadRequest, "token", "required")

	// Storing the value in the same PATCH makes the removal valid.
	f.patchCredentialOK(t, id, `{"external":{},"inputs":{"token":"`+patchRotatedSecret+`"}}`)
	got := f.stored(t, id)
	if len(got.External) != 0 {
		t.Errorf("external {} left %v", got.External)
	}
	if got.Inputs["token"] != patchRotatedSecret || got.Inputs["backup"] != patchBackupSecret {
		t.Errorf("the stored secrets are %v, want the new token and the kept backup", got.Inputs)
	}
}

// TestCredentialPatch_RefusesNull holds the null rule on every field, and on
// a value inside inputs or external: refused, naming where it was, with the
// valid change beside it not applied either. A null inside inputs used to
// decode as "", so it silently cleared an optional secret.
func TestCredentialPatch_RefusesNull(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.edgeCredential(t, f.createPatchType(t, f.orgA, "patch_api"))

	for _, field := range []string{"name", "description", "credential_type", "organization", "inputs", "external"} {
		t.Run(field, func(t *testing.T) {
			change := `"description":"changed"`
			if field == "description" {
				change = `"name":"changed"`
			}
			f.credentialRefusedUnchanged(t, id, `{`+change+`,"`+field+`":null}`, http.StatusBadRequest,
				`\"`+field+`\"`, "may not be null")
		})
	}
	t.Run("a secret input", func(t *testing.T) {
		f.credentialRefusedUnchanged(t, id, `{"description":"changed","inputs":{"region":"west","backup":null}}`,
			http.StatusBadRequest, "inputs", "null")
	})
	t.Run("an external reference", func(t *testing.T) {
		f.credentialRefusedUnchanged(t, id, `{"description":"changed","external":{"url":null}}`,
			http.StatusBadRequest, "external", "null")
	})
}

// TestCredentialPatch_RefusesABodyThatIsNotAnObjectOfKnownFields holds the
// strict decode: a bare null, an array, an unknown key (a read-only one from
// GET among them) and a wrong type are each a malformed body.
func TestCredentialPatch_RefusesABodyThatIsNotAnObjectOfKnownFields(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.edgeCredential(t, f.createPatchType(t, f.orgA, "patch_api"))

	for name, body := range map[string]string{
		"a bare null":                  `null`,
		"an array":                     `[]`,
		"an unknown field":             `{"description":"changed","owner":"ada"}`,
		"a read-only field":            `{"description":"changed","kind":"cloud"}`,
		"the templates it is bound to": `{"description":"changed","templates":[1]}`,
		"a name that is not a string":  `{"name":5}`,
		"inputs that is not an object": `{"inputs":["token"]}`,
		"an input that is not text":    `{"inputs":{"region":5}}`,
		"external that is a string":    `{"external":"file:edge_url"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f.credentialRefusedUnchanged(t, id, body, http.StatusBadRequest, "malformed")
		})
	}
}

// TestCredentialPatch_RefusesAChangedTypeOrOrganization holds the fixed
// fields: a credential's type and organization are refused when they differ
// from the stored ones, where they used to be ignored with a 200, and
// accepted as an echo.
func TestCredentialPatch_RefusesAChangedTypeOrOrganization(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	other := f.createPatchType(t, f.orgA, "patch_api_other")
	id := f.edgeCredential(t, typeID)

	f.credentialRefusedUnchanged(t, id, fmt.Sprintf(`{"description":"changed","credential_type":%d}`, other),
		http.StatusBadRequest, "credential_type cannot be changed")
	f.credentialRefusedUnchanged(t, id, fmt.Sprintf(`{"description":"changed","organization":%d}`, f.orgB),
		http.StatusBadRequest, "organization cannot be changed")

	f.patchCredentialOK(t, id, fmt.Sprintf(`{"description":"echoed","credential_type":%d,"organization":%d}`, typeID, f.orgA))
	got := f.stored(t, id)
	if got.Description != "echoed" || got.External["backup"] != "file:edge_backup" || got.Inputs["token"] != patchTokenSecret {
		t.Errorf("an echo of the fixed fields stored %+v, want the description applied and everything else kept", got)
	}
}

// TestCredentialPatch_IsValidatedWhole holds the merged credential to every
// rule a write is held to, and refuses before the store what the store would
// answer 500 for: a blank name, and a NUL, which PostgreSQL cannot store.
func TestCredentialPatch_IsValidatedWhole(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	typeID := f.createPatchType(t, f.orgA, "patch_api")
	id := f.edgeCredential(t, typeID)
	f.createPatchCredential(t, map[string]any{
		"name": "taken", "organization": f.orgA, "credential_type": typeID,
		"inputs": map[string]string{"token": patchRotatedSecret},
	})

	f.credentialRefusedUnchanged(t, id, `{"name":""}`, http.StatusBadRequest, "name")
	f.credentialRefusedUnchanged(t, id, `{"inputs":{"undeclared":"x"}}`, http.StatusBadRequest, "undeclared")
	f.credentialRefusedUnchanged(t, id, `{"inputs":{"region":"north"}}`, http.StatusBadRequest, "region")
	f.credentialRefusedUnchanged(t, id, `{"inputs":{"token":""}}`, http.StatusBadRequest, "token", "required")
	f.credentialRefusedUnchanged(t, id, `{"name":"taken"}`, http.StatusConflict)

	for field, body := range map[string]map[string]any{
		"name":        {"name": "edge" + nulChar + "api"},
		"description": {"description": "Edge" + nulChar},
		"external":    {"external": map[string]string{"url": "file:edge" + nulChar + "url"}},
	} {
		t.Run("a NUL in "+field, func(t *testing.T) {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			f.credentialRefusedUnchanged(t, id, string(raw), http.StatusBadRequest, field, "NUL")
		})
	}
}

// TestCredentialPatch_NeverShowsASecret rotates a secret and drives every
// refusal with secrets in the body, and asserts by the exact plaintext that
// no response carries one, while the store holds exactly what was sent.
func TestCredentialPatch_NeverShowsASecret(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	id := f.edgeCredential(t, f.createPatchType(t, f.orgA, "patch_api"))
	secretBody := func(rest string) string {
		return `{"inputs":{"token":"` + patchRotatedSecret + `","backup":"` + patchTokenSecret + `"}` + rest + `}`
	}

	for _, body := range []string{
		`{"inputs":{"region":"` + patchRotatedSecret + `"}}`,
		`{"inputs":{"undeclared":"` + patchRotatedSecret + `"}}`,
		secretBody(`,"name":""`),
		secretBody(`,"name":null`),
		secretBody(fmt.Sprintf(`,"organization":%d`, f.orgB)),
		secretBody(`,"external":{"url":null}`),
		secretBody(`,"unknown":true`),
	} {
		if status, _ := f.patchCredential(t, id, body); status == http.StatusOK {
			t.Errorf("PATCH %s was accepted, want a refusal", body)
		}
	}
	if got := f.stored(t, id).Inputs; got["token"] != patchTokenSecret || got["backup"] != patchBackupSecret {
		t.Fatalf("a refused PATCH changed a stored secret: %v", got)
	}

	f.patchCredentialOK(t, id, secretBody(""))
	if got := f.stored(t, id).Inputs; got["token"] != patchRotatedSecret || got["backup"] != patchTokenSecret {
		t.Errorf("the rotated secrets were not stored: %v", got)
	}
	status, body := f.do(t, http.MethodGet, credentialPath(id), nil)
	assertNoSecret(t, "GET", body)
	if status != http.StatusOK || !bytes.Contains(body, []byte(redact.Marker)) {
		t.Errorf("GET after a rotation: %d %s, want the marker", status, body)
	}
}

// TestCredentialCreate_RefusesABlankNameAndNUL holds the create path to the
// same text rules: a blank name answered 500 and a NUL was stored on SQLite
// and failed on PostgreSQL.
func TestCredentialCreate_RefusesABlankNameAndNUL(t *testing.T) {
	t.Parallel()
	f := newCredentialFixture(t)
	typeID := f.createPatchType(t, f.orgA, "patch_api")

	cases := []struct {
		name, field string
		change      map[string]any
		mentions    []string
	}{
		{"a blank name", "name", map[string]any{"name": ""}, []string{"name"}},
		{"a NUL in name", "name", map[string]any{"name": "new" + nulChar + "one"}, []string{"name", "NUL"}},
		{"a NUL in description", "description", map[string]any{"description": "Edge" + nulChar}, []string{"description", "NUL"}},
		{"a NUL in external", "external", map[string]any{"external": map[string]string{"url": "file:edge" + nulChar + "url"}}, []string{"external", "NUL"}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each case has a name of its own, so a case the old code
			// accepted cannot fail the next as a duplicate.
			body := map[string]any{
				"name": fmt.Sprintf("new %d", i), "organization": f.orgA, "credential_type": typeID,
				"inputs": map[string]string{"token": patchTokenSecret},
			}
			for k, v := range tc.change {
				body[k] = v
			}
			status, resp := f.do(t, http.MethodPost, "/api/v1/credentials", body)
			assertNoSecret(t, "POST", resp)
			if status != http.StatusBadRequest {
				t.Fatalf("POST with %s: %d %s, want 400", tc.name, status, resp)
			}
			for _, mention := range tc.mentions {
				if !strings.Contains(string(resp), mention) {
					t.Errorf("POST with %s: the refusal %s does not say %q", tc.name, resp, mention)
				}
			}
		})
	}
}
