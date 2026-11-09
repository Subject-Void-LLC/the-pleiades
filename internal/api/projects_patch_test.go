// This file holds PATCH /projects/{id} to the partial-update rules the
// template and access PATCHes follow, through the real router and the real
// ent-backed store.
//
// The route used to decode its create body, so a field the body left out
// was written back empty: an absent credential unbound the project's
// credential, an absent scm_type turned a manual or archive project into
// git, and a PATCH naming only a description was refused for the name it
// blanked.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// projectPath is the URL of one project.
func projectPath(id int) string { return fmt.Sprintf("/api/v1/projects/%d", id) }

// readProject GETs a project and returns its decoded body.
func (f *projectFixture) readProject(t *testing.T, id int) map[string]any {
	t.Helper()
	status, body := f.do(t, http.MethodGet, projectPath(id), nil)
	if status != http.StatusOK {
		t.Fatalf("GET project %d: status %d, body %s", id, status, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding project %d: %v", id, err)
	}
	return got
}

// create posts a project body that must be accepted and returns the id.
func (f *projectFixture) create(t *testing.T, body string) int {
	t.Helper()
	status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(body))
	if status != http.StatusCreated {
		t.Fatalf("POST %s: status %d, body %s", body, status, resp)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		t.Fatalf("decoding the created project: %v", err)
	}
	return created.ID
}

// patchOK sends a PATCH that must succeed.
func (f *projectFixture) patchOK(t *testing.T, id int, body string) {
	t.Helper()
	if status, resp := f.do(t, http.MethodPatch, projectPath(id), json.RawMessage(body)); status != http.StatusOK {
		t.Fatalf("PATCH %s: status %d, want 200; body %s", body, status, resp)
	}
}

// refusedUnchanged sends a PATCH that must be refused with want, with a
// response saying each of mentions, and leave the project as it was.
func (f *projectFixture) refusedUnchanged(t *testing.T, id int, body string, want int, mentions ...string) {
	t.Helper()
	before := f.readProject(t, id)
	status, resp := f.do(t, http.MethodPatch, projectPath(id), json.RawMessage(body))
	if status != want {
		t.Fatalf("PATCH %s: status %d, want %d; body %s", body, status, want, resp)
	}
	for _, mention := range mentions {
		if !strings.Contains(string(resp), mention) {
			t.Errorf("PATCH %s: the refusal %s does not say %q", body, resp, mention)
		}
	}
	if after := f.readProject(t, id); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused PATCH changed the project:\n before %v\n after  %v", before, after)
	}
}

// assertProjectFields fails for each key of want whose value differs, and
// for each key of absent that is present (the wire omits an empty value).
func assertProjectFields(t *testing.T, got map[string]any, want map[string]any, absent ...string) {
	t.Helper()
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
	for _, key := range absent {
		if value, present := got[key]; present {
			t.Errorf("%s = %#v, want it cleared", key, value)
		}
	}
}

// boundCredential makes a real source control credential the fixture's
// credential projection accepts, and returns its id.
func (f *projectFixture) boundCredential(t *testing.T, name string) int {
	t.Helper()
	id := realCredential(t, f.client, f.orgID, name)
	f.creds.creds = append(f.creds.creds, scmCredential(id))
	return id
}

// TestProjectPatch_KeepsWhatItDoesNotName is the partial update: a PATCH
// naming one field leaves the credential binding, the source type and every
// other field as stored.
func TestProjectPatch_KeepsWhatItDoesNotName(t *testing.T) {
	f := newProjectFixture(t)
	credID := f.boundCredential(t, "forge")
	id := f.create(t, fmt.Sprintf(`{"name":"automation","description":"Edge playbooks","organization":%d,
		"scm_type":"git","scm_url":"https://example.invalid/a.git","scm_branch":"main","credential":%d}`, f.orgID, credID))

	f.patchOK(t, id, `{"description":"Edge and core playbooks"}`)
	assertProjectFields(t, f.readProject(t, id), map[string]any{
		"name": "automation", "description": "Edge and core playbooks", "organization": float64(f.orgID),
		"scm_type": "git", "scm_url": "https://example.invalid/a.git", "scm_branch": "main",
		"credential": float64(credID),
	})

	f.patchOK(t, id, `{"name":"automation-renamed"}`)
	assertProjectFields(t, f.readProject(t, id), map[string]any{
		"name": "automation-renamed", "description": "Edge and core playbooks", "credential": float64(credID),
	})

	// An empty value clears what can be empty, and "credential": 0 unbinds,
	// as it binds nothing on create.
	f.patchOK(t, id, `{"description":"","scm_branch":""}`)
	assertProjectFields(t, f.readProject(t, id), map[string]any{"credential": float64(credID)}, "description", "scm_branch")
	f.patchOK(t, id, `{"credential":0}`)
	assertProjectFields(t, f.readProject(t, id), map[string]any{"name": "automation-renamed"}, "credential")

	// A non-git project keeps its type: an absent scm_type used to default
	// to git and turn it into one.
	manual := f.create(t, fmt.Sprintf(`{"name":"hand-placed","organization":%d,"scm_type":"manual",
		"scm_url":"https://example.invalid/recorded.git"}`, f.orgID))
	f.patchOK(t, manual, `{"name":"hand-placed-renamed"}`)
	assertProjectFields(t, f.readProject(t, manual), map[string]any{
		"name": "hand-placed-renamed", "scm_type": "manual", "scm_url": "https://example.invalid/recorded.git",
	})
}

// TestProjectPatch_IsValidatedWhole holds the merged project to every check
// a full write runs: a git project needs a URL, a name may not be blank, a
// credential must authenticate a clone, and a taken name is a conflict.
func TestProjectPatch_IsValidatedWhole(t *testing.T) {
	f := newProjectFixture(t)
	f.createProject(t, "taken")
	manual := f.create(t, fmt.Sprintf(`{"name":"hand-placed","organization":%d,"scm_type":"manual"}`, f.orgID))

	machine := realCredential(t, f.client, f.orgID, "device-access")
	f.creds.creds = []credstore.Credential{{
		ID: machine, Name: "device access", Kind: credtype.KindSSH,
		Inputs: map[string]string{"username": "root", "ssh_key_data": "$encrypted$"},
	}}

	f.refusedUnchanged(t, manual, `{"scm_type":"git"}`, http.StatusBadRequest, "scm_url")
	f.refusedUnchanged(t, manual, `{"scm_type":"subversion"}`, http.StatusBadRequest, "scm_type")
	f.refusedUnchanged(t, manual, `{"name":"   "}`, http.StatusBadRequest, "name")
	f.refusedUnchanged(t, manual, fmt.Sprintf(`{"credential":%d}`, machine), http.StatusBadRequest, "credential")
	f.refusedUnchanged(t, manual, `{"name":"taken"}`, http.StatusConflict)
}

// TestProjectPatch_RefusesNull holds the null rule on every field: refused,
// naming the field, with the valid change beside it not applied either.
func TestProjectPatch_RefusesNull(t *testing.T) {
	f := newProjectFixture(t)
	credID := f.boundCredential(t, "forge")
	id := f.create(t, fmt.Sprintf(`{"name":"automation","description":"Edge","organization":%d,
		"scm_url":"https://example.invalid/a.git","scm_branch":"main","credential":%d}`, f.orgID, credID))

	for _, field := range []string{"name", "description", "organization", "scm_type", "scm_url", "scm_branch", "credential"} {
		t.Run(field, func(t *testing.T) {
			change := `"description":"changed"`
			if field == "description" {
				change = `"scm_branch":"dev"`
			}
			f.refusedUnchanged(t, id, `{`+change+`,"`+field+`":null}`, http.StatusBadRequest,
				`\"`+field+`\"`, "may not be null")
		})
	}
}

// TestProjectPatch_RefusesABodyThatIsNotAnObjectOfKnownFields holds the
// strict decode: a bare null, an array, an unknown key (a read-only one
// among them) and a wrong type are each a malformed body.
func TestProjectPatch_RefusesABodyThatIsNotAnObjectOfKnownFields(t *testing.T) {
	f := newProjectFixture(t)
	id := f.createProject(t, "automation")

	for name, body := range map[string]string{
		"a bare null":               `null`,
		"an array":                  `[]`,
		"an unknown field":          `{"description":"changed","owner":"ada"}`,
		"a read-only field":         `{"description":"changed","sync_status":"succeeded"}`,
		"a field of the wrong type": `{"description":"changed","credential":"forge"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f.refusedUnchanged(t, id, body, http.StatusBadRequest, "malformed JSON body")
		})
	}
}

// TestProjectPatch_TheOrganizationIsFixed refuses a move between tenants,
// naming the field, where it used to be ignored with a 200, and accepts the
// stored organization as an echo.
func TestProjectPatch_TheOrganizationIsFixed(t *testing.T) {
	f := newProjectFixture(t)
	other := f.client.Organization.Create().SetName("elsewhere").SaveX(context.Background())
	id := f.createProject(t, "automation")

	f.refusedUnchanged(t, id, fmt.Sprintf(`{"name":"moved","organization":%d}`, other.ID), http.StatusBadRequest,
		"organization cannot be changed", "fixed")

	f.patchOK(t, id, fmt.Sprintf(`{"name":"renamed","organization":%d}`, f.orgID))
	assertProjectFields(t, f.readProject(t, id), map[string]any{"name": "renamed", "organization": float64(f.orgID)})
}

// legacySecretURL is a source URL carrying a password in its userinfo, the
// form an older release stored before the store began refusing it.
const legacySecretURL = "https://svc:hunter2@example.invalid/legacy.git"

// seedLegacySecretURL creates a project of the given type and then writes
// legacySecretURL into its row directly through the ent client, as a row
// stored before the rule would hold it, and returns the project's id.
func (f *projectFixture) seedLegacySecretURL(t *testing.T, name, scmType string) int {
	t.Helper()
	id := f.create(t, fmt.Sprintf(`{"name":%q,"organization":%d,"scm_type":%q,"scm_url":"https://example.invalid/clean.git"}`,
		name, f.orgID, scmType))
	f.client.Project.UpdateOneID(id).SetScmURL(legacySecretURL).ExecX(context.Background())
	return id
}

// storedSourceURL reads a project's real URL through the store, which GET
// never shows.
func (f *projectFixture) storedSourceURL(t *testing.T, id int) string {
	t.Helper()
	p, err := f.store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("reading project %d through the store: %v", id, err)
	}
	return p.SCMURL
}

// TestProjectPatch_AnEchoedRedactedURLKeepsTheStoredOne covers the ordinary
// client flow the fixed-field rule invites: GET a project, change one field,
// PATCH the create fields back. GET shows scm_url redacted, so the scm_url
// sent back is the redacted form, and it used to replace the stored URL.
// It is an echo, and keeps the stored URL; a real change still applies.
func TestProjectPatch_AnEchoedRedactedURLKeepsTheStoredOne(t *testing.T) {
	f := newProjectFixture(t)
	id := f.seedLegacySecretURL(t, "hand-placed", "manual")

	shown, _ := f.readProject(t, id)["scm_url"].(string)
	if shown == legacySecretURL || strings.Contains(shown, "hunter2") {
		t.Fatalf("GET showed the stored password: %q", shown)
	}

	body, err := json.Marshal(map[string]any{"description": "changed", "scm_url": shown})
	if err != nil {
		t.Fatal(err)
	}
	status, resp := f.do(t, http.MethodPatch, projectPath(id), json.RawMessage(body))
	if status != http.StatusOK {
		t.Fatalf("PATCH echoing the shown URL: status %d, want 200; body %s", status, resp)
	}
	if strings.Contains(string(resp), "hunter2") {
		t.Errorf("the response repeats the stored password: %s", resp)
	}
	if got := f.storedSourceURL(t, id); got != legacySecretURL {
		t.Errorf("stored scm_url = %q after an echo of the shown one, want it kept as stored", got)
	}
	assertProjectFields(t, f.readProject(t, id), map[string]any{"description": "changed"})

	// A different URL is a real change, and applies.
	f.patchOK(t, id, `{"scm_url":"https://example.invalid/fresh.git"}`)
	if got := f.storedSourceURL(t, id); got != "https://example.invalid/fresh.git" {
		t.Errorf("stored scm_url = %q, want the new URL", got)
	}
}

// TestProjectPatch_AGitProjectWithAStoredPasswordIsStillRefused keeps the
// store's rule for a git row: the echo keeps the stored URL, which carries a
// password, so the save is refused until the URL is changed. The refusal and
// the stored row hold no trace of the password changing hands.
func TestProjectPatch_AGitProjectWithAStoredPasswordIsStillRefused(t *testing.T) {
	f := newProjectFixture(t)
	id := f.seedLegacySecretURL(t, "legacy-git", "git")
	shown, _ := f.readProject(t, id)["scm_url"].(string)

	body, err := json.Marshal(map[string]any{"description": "changed", "scm_url": shown})
	if err != nil {
		t.Fatal(err)
	}
	status, resp := f.do(t, http.MethodPatch, projectPath(id), json.RawMessage(body))
	if status != http.StatusBadRequest || !strings.Contains(string(resp), "credential") {
		t.Fatalf("PATCH of a git row whose URL carries a password: status %d, want 400 saying to use a credential; body %s", status, resp)
	}
	if strings.Contains(string(resp), "hunter2") {
		t.Errorf("the refusal repeats the stored password: %s", resp)
	}
	if got := f.storedSourceURL(t, id); got != legacySecretURL {
		t.Errorf("stored scm_url = %q, want it unchanged by a refused PATCH", got)
	}
}
