// Package resources_test's project edit coverage: what the Projects form
// writes back when somebody edits one field and presses Save.
//
// The form shows scm_url redacted, so pressing Save sends the redacted form
// back. That used to replace the stored URL with it.
package resources_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// legacyProjectURL is a source URL carrying a password in its userinfo, the
// form an older release stored before the store refused it.
const legacyProjectURL = "https://svc:hunter2@git.example.test/team/legacy.git"

// TestProjectsView_SavingTheEditFormKeepsAStoredURLItShowsRedacted is the
// round trip: open the edit form of a project whose stored URL carries a
// password, change the description, press Save. The URL control holds the
// redacted form, which is an echo of the stored URL, so the stored URL is
// kept and the description changes.
func TestProjectsView_SavingTheEditFormKeepsAStoredURLItShowsRedacted(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// Seeded straight into the store, as a row from before the rule would be.
	// A manual project, because the store refuses to save a git one whose URL
	// carries a password at all, echo or not.
	seeded, err := conformanceProjectStore.Create(context.Background(), project.Project{
		Name: uniqueName(t, "legacy-manual"), OrganizationID: 1,
		SCMType: project.SCMManual, SCMURL: legacyProjectURL,
	})
	if err != nil {
		t.Fatalf("seeding the project: %v", err)
	}
	path := "/ui/projects/" + strconv.Itoa(seeded.ID)

	form := body(t, h, path+"/edit")
	if strings.Contains(form, "hunter2") {
		t.Fatal("the edit form shows the stored password")
	}
	values := renderedFormValues(t, form)
	if values["scm_url"] == "" || values["scm_url"] == legacyProjectURL {
		t.Fatalf("the URL control is prefilled with %q, want the redacted form", values["scm_url"])
	}
	values["description"] = "edited through the form"

	if w := h.post(t, path, values); w.Code != http.StatusSeeOther {
		t.Fatalf("saving the edit form = %d, want a redirect: %s", w.Code, formErrors(w.Body.String()))
	}
	stored, err := conformanceProjectStore.Get(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("reading the project back: %v", err)
	}
	if stored.SCMURL != legacyProjectURL {
		t.Errorf("stored scm_url = %q after saving the form unchanged, want it kept as stored", stored.SCMURL)
	}
	if stored.Description != "edited through the form" {
		t.Errorf("description = %q, want the edit applied", stored.Description)
	}

	// A URL typed into the control is a real change, and applies.
	values["scm_url"] = "https://git.example.test/team/fresh.git"
	if w := h.post(t, path, values); w.Code != http.StatusSeeOther {
		t.Fatalf("saving a new URL = %d, want a redirect: %s", w.Code, formErrors(w.Body.String()))
	}
	if stored, _ = conformanceProjectStore.Get(context.Background(), seeded.ID); stored.SCMURL != values["scm_url"] {
		t.Errorf("stored scm_url = %q, want the URL typed into the form", stored.SCMURL)
	}
}
