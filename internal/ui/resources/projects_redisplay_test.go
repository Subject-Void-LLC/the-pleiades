// Package resources_test's project redisplay coverage: a refused Projects
// form never shows back a source URL that may carry a credential.
//
// A refused form is re-rendered with what was submitted, so somebody does not
// have to retype it to learn what was wrong. For a URL carrying a password
// that meant the 422 page held the password: on the screen, in the browser's
// history and cache, and in anything that records responses.
package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestProjectsView_ARefusedFormNeverShowsATypedCredentialURL submits a URL
// carrying a password on the create form and on the edit form, and expects
// each refused beside the URL control with no trace of the password on the
// page.
func TestProjectsView_ARefusedFormNeverShowsATypedCredentialURL(t *testing.T) {
	h := newHarness(t, adminIdentity)
	orgID := optionValue(t, body(t, h, "/ui/projects/new"), "organization", "acme")
	typed := "https://user:hunter2@git.example.test/team/r.git"

	for _, scmType := range []string{"git", "manual"} {
		w := h.post(t, "/ui/projects", map[string]string{
			"name": uniqueName(t, "typed-"+scmType), "organization": orgID,
			"scm_type": scmType, "scm_url": typed,
		})
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("creating a %s project with a password in its URL = %d, want 422", scmType, w.Code)
		}
		if page := w.Body.String(); strings.Contains(page, "hunter2") {
			t.Errorf("the refused %s create page shows the typed password", scmType)
		}
		assertURLCredentialError(t, w.Body.String())
	}

	edit := renderedFormValues(t, body(t, h, "/ui/projects/1/edit"))
	edit["scm_url"] = typed
	w := h.post(t, "/ui/projects/1", edit)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("editing to a URL with a password = %d, want 422", w.Code)
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Error("the refused edit page shows the typed password")
	}
	assertURLCredentialError(t, w.Body.String())

	// A refusal for some other reason redisplays a clean URL as typed, so
	// nobody retypes what was right.
	w = h.post(t, "/ui/projects", map[string]string{
		"name": "", "organization": orgID, "scm_type": "git", "scm_url": "https://git.example.test/team/kept.git",
	})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "https://git.example.test/team/kept.git") {
		t.Errorf("a refused form did not redisplay a clean URL as typed (status %d)", w.Code)
	}
}
