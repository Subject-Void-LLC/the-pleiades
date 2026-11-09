// Package resources_test's project edit coverage: what the Projects form
// writes back when somebody edits one field and presses Save, and what it
// does with a URL carrying a credential.
//
// The form shows scm_url redacted, so pressing Save sends the redacted form
// back. That used to replace the stored URL with it. And a URL carrying a
// credential is refused for a project of every type, as the store refuses
// it, where a manual or archive project used to store one in the plain
// scm_url column.
//
// The harness's project store is in memory. It applies the store's own
// credential rule by calling project.RefuseEmbeddedSecret, the function the
// ent store calls, so what is under test here is the form and its writer;
// internal/project's own tests prove the ent store refuses.
package resources_test

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// legacyProjectURL is a source URL carrying a password in its userinfo, the
// form an older release stored before the store refused it.
const legacyProjectURL = "https://svc:hunter2@git.example.test/team/legacy.git"

// legacyWithheldProjectURL is an scp-like URL carrying a password, which
// net/url cannot read, so the form withholds it whole as a placeholder.
const legacyWithheldProjectURL = "git:hunter2@git.example.test:team/legacy.git"

// TestProjectsView_SavingTheEditFormNeverReplacesAStoredURLItShowsRedacted
// is the round trip: open the edit form of a project whose stored URL carries
// a password, change the description, press Save. The URL control holds the
// redacted form, or the placeholder for a URL withheld whole, an echo of the
// stored URL, so the stored URL is kept, and a stored URL carrying a
// credential is refused on save until it is changed, beside the URL control.
// A clean URL typed in is a real change and saves.
//
// The withheld case is the one the echo decides. The redacted form
// "https://redacted@..." carries a user, so it is refused even read as a new
// URL; the placeholder carries nothing, so read as a new URL it would be
// saved over the real one, and only the echo keeps the stored URL and its
// refusal.
func TestProjectsView_SavingTheEditFormNeverReplacesAStoredURLItShowsRedacted(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for name, url := range map[string]string{"redacted": legacyProjectURL, "withheld": legacyWithheldProjectURL} {
		t.Run(name, func(t *testing.T) {
			// Seeded past the write rules, as a row from before the rule
			// would be.
			seeded := conformanceProjectStore.seedRaw(project.Project{
				Name: uniqueName(t, "legacy-manual"), OrganizationID: 1,
				SCMType: project.SCMManual, SCMURL: url,
			})
			path := "/ui/projects/" + strconv.Itoa(seeded.ID)

			form := body(t, h, path+"/edit")
			if strings.Contains(form, "hunter2") {
				t.Fatal("the edit form shows the stored password")
			}
			values := renderedFormValues(t, form)
			if values["scm_url"] == "" || values["scm_url"] == url {
				t.Fatalf("the URL control is prefilled with %q, want the redacted or withheld form", values["scm_url"])
			}
			values["description"] = "edited through the form"

			w := h.post(t, path, values)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("saving the form over a stored URL carrying a password = %d, want 422", w.Code)
			}
			assertURLCredentialError(t, w.Body.String())
			stored, err := conformanceProjectStore.Get(context.Background(), seeded.ID)
			if err != nil {
				t.Fatalf("reading the project back: %v", err)
			}
			if stored.SCMURL != url || stored.Description == "edited through the form" {
				t.Errorf("a refused save changed the project: %+v", stored)
			}

			// A URL typed into the control is a real change, and applies.
			values["scm_url"] = "https://git.example.test/team/fresh.git"
			if w := h.post(t, path, values); w.Code != http.StatusSeeOther {
				t.Fatalf("saving a clean URL = %d, want a redirect: %s", w.Code, formErrors(w.Body.String()))
			}
			if stored, _ = conformanceProjectStore.Get(context.Background(), seeded.ID); stored.SCMURL != values["scm_url"] {
				t.Errorf("stored scm_url = %q, want the URL typed into the form", stored.SCMURL)
			}
		})
	}
}

// TestProjectsView_RefusesACredentialInTheURLForEveryType holds the refusal
// on the create form for each type, beside the URL control.
func TestProjectsView_RefusesACredentialInTheURLForEveryType(t *testing.T) {
	h := newHarness(t, adminIdentity)
	orgID := optionValue(t, body(t, h, "/ui/projects/new"), "organization", "acme")

	for _, scmType := range []string{"git", "manual", "archive"} {
		t.Run(scmType, func(t *testing.T) {
			w := h.post(t, "/ui/projects", map[string]string{
				"name": uniqueName(t, "secret-"+scmType), "organization": orgID,
				"scm_type": scmType, "scm_url": legacyProjectURL,
			})
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("creating a %s project with a password in its URL = %d, want 422", scmType, w.Code)
			}
			assertURLCredentialError(t, w.Body.String())
		})
	}
}

// assertURLCredentialError checks a refused page says, beside the URL
// control, to use a credential, in a message repeating no part of the
// password.
//
// Only the message is checked for the password here; the page as a whole is
// held to it by TestProjectsView_ARefusedFormNeverShowsATypedCredentialURL,
// since a refused form redisplays the URL control redacted or withheld.
func assertURLCredentialError(t *testing.T, page string) {
	t.Helper()
	m := regexp.MustCompile(`id="f-scm_url-error">([^<]*)`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page has no error beside the URL control: %s", formErrors(page))
	}
	if !strings.Contains(m[1], "give the credential to this project") {
		t.Errorf("the error beside the URL %q does not say to use a credential", m[1])
	}
	if strings.Contains(m[1], "hunter2") || strings.Contains(m[1], "git.example.test") {
		t.Errorf("the error beside the URL %q repeats the URL or its password", m[1])
	}
}
