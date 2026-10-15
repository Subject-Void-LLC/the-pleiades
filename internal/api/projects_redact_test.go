// Tests that a project URL carrying a credential, stored before the write
// rule refused one, never leaves the API with it.
package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	entproject "github.com/Subject-Void-LLC/the-pleiades/internal/ent/project"
)

// TestProjects_AStoredCredentialInTheURLIsWithheld writes a row the way one
// stored before 2026-10-14 could be, with a token in its URL's user position,
// straight into the table past the store's refusal, and reads it back through
// the read and list routes: each shows the URL with its userinfo withheld.
func TestProjects_AStoredCredentialInTheURLIsWithheld(t *testing.T) {
	f := newProjectFixture(t)
	const token = "ghp_0123456789abcdefTOKEN"
	row := f.client.Project.Create().SetName("legacy").SetScmType(entproject.ScmTypeGit).
		SetScmURL("https://" + token + "@github.com/org/repo.git").SetOrganizationID(f.orgID).SaveX(context.Background())

	for _, path := range []string{fmt.Sprintf("/api/v1/projects/%d", row.ID), "/api/v1/projects"} {
		status, body := f.do(t, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d, body %s", path, status, body)
		}
		if strings.Contains(string(body), token) {
			t.Errorf("GET %s returned the token: %s", path, body)
		}
		if !strings.Contains(string(body), "https://redacted@github.com/org/repo.git") {
			t.Errorf("GET %s does not show the withheld URL: %s", path, body)
		}
	}
}
