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

// legacyWithheldURL is an scp-like URL carrying a password, which net/url
// cannot read, so GET withholds it whole rather than redacting its userinfo.
const legacyWithheldURL = "git:hunter2@example.invalid:team/legacy.git"

// seedLegacyURL creates a project of the given type and then writes url into
// its row directly through the ent client, as a row stored before the rule
// would hold it, and returns the project's id.
func (f *projectFixture) seedLegacyURL(t *testing.T, name, scmType, url string) int {
	t.Helper()
	id := f.create(t, fmt.Sprintf(`{"name":%q,"organization":%d,"scm_type":%q,"scm_url":"https://example.invalid/clean.git"}`,
		name, f.orgID, scmType))
	f.client.Project.UpdateOneID(id).SetScmURL(url).ExecX(context.Background())
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

// assertCredentialRefusal checks a write was refused for the credential in
// its URL, saying to use a credential and repeating no part of the secret.
func assertCredentialRefusal(t *testing.T, what string, status int, resp []byte) {
	t.Helper()
	if status != http.StatusBadRequest || !strings.Contains(string(resp), "credential") {
		t.Errorf("%s: status %d, want 400 saying to use a credential; body %s", what, status, resp)
	}
	if strings.Contains(string(resp), "hunter2") {
		t.Errorf("%s: the response repeats the password: %s", what, resp)
	}
}

// TestProjectPatch_AnEchoedRedactedURLNeverReplacesTheStoredOne covers the
// ordinary client flow the fixed-field rule invites: GET a project, change one
// field, PATCH the create fields back. GET shows a URL carrying a credential
// redacted (or withheld whole), so the scm_url sent back is that form. It is
// an echo of the stored URL, never a new value: the stored URL is kept, and a
// stored URL carrying a credential is refused on save for every type until it
// is changed. It used to be read as a new URL, which for a manual project
// replaced the real one with the redacted form or the placeholder.
func TestProjectPatch_AnEchoedRedactedURLNeverReplacesTheStoredOne(t *testing.T) {
	for _, scmType := range []string{"git", "manual"} {
		for name, url := range map[string]string{
			"a password":     legacySecretURL,
			"a withheld URL": legacyWithheldURL,
			// Shapes a parser misreads, which GET used to show raw.
			"a bad port":                 "https://svc:hunter2@example.invalid:badport/legacy.git",
			"git+https with a token":     "git+https://hunter2@example.invalid/legacy.git",
			"a scheme-less authority":    "//bot:hunter2@example.invalid/legacy.git",
			"scp-like with a host space": "git:hunter2@example invalid:team/legacy.git",
			"a password holding a slash": "https://svc:pa/hunter2@example.invalid/legacy.git",
			"a token in the query":       "https://example.invalid/legacy.git?access_token=hunter2",
		} {
			t.Run(scmType+", "+name, func(t *testing.T) {
				f := newProjectFixture(t)
				id := f.seedLegacyURL(t, "legacy", scmType, url)

				shown, _ := f.readProject(t, id)["scm_url"].(string)
				if shown == url || strings.Contains(shown, "hunter2") {
					t.Fatalf("GET showed the stored password: %q", shown)
				}

				body, err := json.Marshal(map[string]any{"description": "changed", "scm_url": shown})
				if err != nil {
					t.Fatal(err)
				}
				status, resp := f.do(t, http.MethodPatch, projectPath(id), json.RawMessage(body))
				assertCredentialRefusal(t, "PATCH echoing the shown URL", status, resp)
				if got := f.storedSourceURL(t, id); got != url {
					t.Errorf("stored scm_url = %q after an echo of the shown one, want it kept as stored", got)
				}

				// A clean URL is a real change, and applies.
				f.patchOK(t, id, `{"scm_url":"https://example.invalid/fresh.git"}`)
				if got := f.storedSourceURL(t, id); got != "https://example.invalid/fresh.git" {
					t.Errorf("stored scm_url = %q, want the new URL", got)
				}
			})
		}
	}
}

// TestProjects_ACredentialInTheURLIsRefusedForEveryType holds the store's
// refusal on both writes for a manual and an archive project, as for a git
// one: it used to sit behind the git-only check, so a manual project stored a
// password in the plain scm_url column. A row that already holds one stays
// readable, redacted, and must be changed before its next edit.
func TestProjects_ACredentialInTheURLIsRefusedForEveryType(t *testing.T) {
	f := newProjectFixture(t)

	for _, scmType := range []string{"manual", "archive"} {
		status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(fmt.Sprintf(
			`{"name":"secret-%s","organization":%d,"scm_type":%q,"scm_url":%q}`, scmType, f.orgID, scmType, legacySecretURL)))
		assertCredentialRefusal(t, "creating a "+scmType+" project", status, resp)
	}

	manual := f.create(t, fmt.Sprintf(`{"name":"hand-placed","organization":%d,"scm_type":"manual",
		"scm_url":"https://example.invalid/clean.git"}`, f.orgID))
	body, err := json.Marshal(map[string]string{"scm_url": legacySecretURL})
	if err != nil {
		t.Fatal(err)
	}
	status, resp := f.do(t, http.MethodPatch, projectPath(manual), json.RawMessage(body))
	assertCredentialRefusal(t, "PATCH of a manual project to a URL carrying a password", status, resp)
	if got := f.storedSourceURL(t, manual); got != "https://example.invalid/clean.git" {
		t.Errorf("stored scm_url = %q after a refused PATCH", got)
	}

	legacy := f.seedLegacyURL(t, "legacy-manual", "manual", legacySecretURL)
	for _, path := range []string{projectPath(legacy), "/api/v1/projects"} {
		status, resp := f.do(t, http.MethodGet, path, nil)
		if status != http.StatusOK || strings.Contains(string(resp), "hunter2") || !strings.Contains(string(resp), "redacted@") {
			t.Errorf("GET %s with a legacy row: status %d, want 200 with the URL redacted; body %s", path, status, resp)
		}
	}
	status, resp = f.do(t, http.MethodPatch, projectPath(legacy), json.RawMessage(`{"description":"changed"}`))
	assertCredentialRefusal(t, "PATCH of a legacy row changing only the description", status, resp)
	f.patchOK(t, legacy, `{"scm_url":"https://example.invalid/fresh.git","description":"changed"}`)
	if got := f.storedSourceURL(t, legacy); got != "https://example.invalid/fresh.git" {
		t.Errorf("stored scm_url = %q, want the clean URL", got)
	}
}

// TestProjects_ASourceURLMayCarryNoQueryOrFragment refuses a query or a
// fragment in a source URL for every type, whatever its names: no list of
// secret names covers an Azure SAS sig or an S3 signature, and a repository
// source needs neither. A stored URL holding one is withheld whole.
func TestProjects_ASourceURLMayCarryNoQueryOrFragment(t *testing.T) {
	f := newProjectFixture(t)
	for name, url := range map[string]string{
		"an Azure SAS signature":  "https://example.invalid/c/repo.tar?sv=2024&sig=hunter2",
		"a token in the fragment": "https://example.invalid/r.git#token=hunter2",
		"an ordinary query":       "https://example.invalid/r.git?ref=main",
	} {
		for _, scmType := range []string{"git", "manual", "archive"} {
			t.Run(name+", "+scmType, func(t *testing.T) {
				body, err := json.Marshal(map[string]any{
					"name": name + " " + scmType, "organization": f.orgID, "scm_type": scmType, "scm_url": url,
				})
				if err != nil {
					t.Fatal(err)
				}
				status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(body))
				if status != http.StatusBadRequest || !strings.Contains(string(resp), "query or a fragment") {
					t.Errorf("status %d, want 400 saying a source URL may not carry a query or a fragment; body %s", status, resp)
				}
				if strings.Contains(string(resp), "hunter2") {
					t.Errorf("the refusal repeats the URL: %s", resp)
				}
			})
		}
	}

	legacy := f.seedLegacyURL(t, "legacy-query", "manual", "https://example.invalid/repo.tar?sig=hunter2")
	if shown, _ := f.readProject(t, legacy)["scm_url"].(string); shown != "(withheld: the URL carries a credential)" {
		t.Errorf("GET shows a stored URL with a query as %q, want it withheld whole", shown)
	}
}
