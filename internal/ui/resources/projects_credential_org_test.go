// Package resources_test's project credential tenancy coverage: the Projects
// form binds only a credential of the project's own organization, on create
// and on edit, and says so beside the credential control.
//
// The credential check searched every organization's credentials and never
// compared the credential's organization with the project's, so a project of
// one tenant could bind another's source control credential.
package resources_test

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// assertCredentialTenancyError checks a refused page says, beside the
// credential control, to choose the project's own organization's credential,
// and names nothing of the other organization's credential.
func assertCredentialTenancyError(t *testing.T, page string) {
	t.Helper()
	m := regexp.MustCompile(`id="f-credential-error">([^<]*)`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page has no error beside the credential control: %s", formErrors(page))
	}
	if !strings.Contains(m[1], "organization") {
		t.Errorf("the error beside the credential %q does not say it must be the project's organization's", m[1])
	}
	for _, leak := range []string{"other tenant", "conformance-second"} {
		if strings.Contains(m[1], leak) {
			t.Errorf("the error beside the credential %q names %q", m[1], leak)
		}
	}
}

// TestProjectsView_BindsOnlyACredentialOfTheProjectsOwnOrganization submits
// the second organization's credential for a project of the first, on the
// create form and on the edit form, and expects each refused with nothing
// stored.
func TestProjectsView_BindsOnlyACredentialOfTheProjectsOwnOrganization(t *testing.T) {
	h := newHarness(t, adminIdentity)
	form := body(t, h, "/ui/projects/new")
	orgID := optionValue(t, form, "organization", "acme")
	foreign := strconv.Itoa(conformanceOtherTenantCredentialID)

	// The chooser names each credential's owner, since it cannot narrow
	// itself to an organization chosen in the same form.
	if got := optionValue(t, form, "credential", "other tenant scm credential (Source Control, conformance-second)"); got != foreign {
		t.Errorf("the chooser offers the other organization's credential as %q, want %q", got, foreign)
	}

	name := uniqueName(t, "borrowing")
	w := h.post(t, "/ui/projects", map[string]string{
		"name": name, "organization": orgID, "scm_type": "git",
		"scm_url": "https://git.example.test/team/borrowing.git", "credential": foreign,
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("creating a project with another organization's credential = %d, want 422", w.Code)
	}
	assertCredentialTenancyError(t, w.Body.String())
	if strings.Contains(body(t, h, "/ui/projects"), name) {
		t.Error("a refused create left a project behind")
	}

	before, err := conformanceProjectStore.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("reading the seeded project: %v", err)
	}
	edit := renderedFormValues(t, body(t, h, "/ui/projects/1/edit"))
	edit["credential"] = foreign
	w = h.post(t, "/ui/projects/1", edit)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("editing a project to another organization's credential = %d, want 422", w.Code)
	}
	assertCredentialTenancyError(t, w.Body.String())
	if after, _ := conformanceProjectStore.Get(context.Background(), 1); after.CredentialID != before.CredentialID {
		t.Errorf("a refused edit bound credential %d", after.CredentialID)
	}
}
