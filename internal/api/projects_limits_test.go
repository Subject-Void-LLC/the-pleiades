// This file holds both project writes to the database's length bounds,
// through the real router and the real ent-backed store. A value longer
// than its column used to reach ent's validator and answer 500 "internal
// error" on create and on update (FAILURE_PATTERNS 1076); it is now a 400
// naming the field and the bound, read from project.CheckLengths.
package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// projectBound is one length bound a project write is held to.
type projectBound struct {
	field string
	max   int
	// value builds a value of n bytes for the field that is otherwise valid.
	value func(n int) string
}

// projectBounds are the four bounds, each with a valid value of any length.
var projectBounds = []projectBound{
	{"name", 253, func(n int) string { return strings.Repeat("n", n) }},
	{"description", 1024, func(n int) string { return strings.Repeat("d", n) }},
	{"scm_url", 2048, func(n int) string {
		const prefix = "https://example.invalid/"
		return prefix + strings.Repeat("u", n-len(prefix))
	}},
	{"scm_branch", 255, func(n int) string { return strings.Repeat("b", n) }},
}

// TestProjects_RefuseAFieldLongerThanTheDatabaseHolds is FAILURE_PATTERNS
// 1076: each bound on create and on PATCH, at the boundary. The bound is
// accepted and one byte more is a 400 naming the field and the bound, where
// it used to reach ent's validator and answer 500.
func TestProjects_RefuseAFieldLongerThanTheDatabaseHolds(t *testing.T) {
	for _, bound := range projectBounds {
		// createBody is a valid create body whose bound.field is n bytes.
		createBody := func(t *testing.T, orgID, n int) string {
			t.Helper()
			body := map[string]any{"name": "bounded", "organization": orgID, "scm_url": "https://example.invalid/a.git"}
			body[bound.field] = bound.value(n)
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			return string(encoded)
		}

		t.Run("create "+bound.field, func(t *testing.T) {
			f := newProjectFixture(t)
			at := createBody(t, f.orgID, bound.max)
			over := createBody(t, f.orgID, bound.max+1)

			status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(over))
			if status != http.StatusBadRequest {
				t.Fatalf("a %s of %d bytes: status %d, want 400; body %s", bound.field, bound.max+1, status, resp)
			}
			if !strings.Contains(string(resp), bound.field) || !strings.Contains(string(resp), fmt.Sprint(bound.max)) {
				t.Errorf("the refusal %s does not name %s and its bound %d", resp, bound.field, bound.max)
			}
			if status, resp := f.do(t, http.MethodGet, "/api/v1/projects", nil); status != http.StatusOK || strings.Contains(string(resp), `"bounded"`) {
				t.Errorf("a refused create left a project behind: %s", resp)
			}

			f.create(t, at)
		})

		t.Run("PATCH "+bound.field, func(t *testing.T) {
			f := newProjectFixture(t)
			id := f.createProject(t, "automation")
			over, err := json.Marshal(map[string]string{bound.field: bound.value(bound.max + 1)})
			if err != nil {
				t.Fatal(err)
			}
			f.refusedUnchanged(t, id, string(over), http.StatusBadRequest, bound.field, fmt.Sprint(bound.max))

			// The same value in a body naming every other field too, which
			// is the body that reached the store's update, and its 500,
			// before PATCH was partial.
			full := map[string]any{"name": "automation", "organization": f.orgID, "scm_url": "https://example.invalid/automation.git"}
			full[bound.field] = bound.value(bound.max + 1)
			fullOver, err := json.Marshal(full)
			if err != nil {
				t.Fatal(err)
			}
			f.refusedUnchanged(t, id, string(fullOver), http.StatusBadRequest, bound.field, fmt.Sprint(bound.max))

			at, err := json.Marshal(map[string]string{bound.field: bound.value(bound.max)})
			if err != nil {
				t.Fatal(err)
			}
			f.patchOK(t, id, string(at))
		})
	}

	// Bytes, as the database measures: 127 two-byte characters are 254
	// bytes. And the bound is on the value as saved, which is trimmed.
	f := newProjectFixture(t)
	status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(fmt.Sprintf(
		`{"name":%q,"organization":%d,"scm_url":"https://example.invalid/a.git"}`, strings.Repeat("\xc3\xa9", 127), f.orgID)))
	if status != http.StatusBadRequest {
		t.Errorf("a 254-byte name of two-byte characters: status %d, want 400; body %s", status, resp)
	}
	f.create(t, fmt.Sprintf(`{"name":%q,"organization":%d,"scm_url":"https://example.invalid/a.git"}`,
		"  "+strings.Repeat("n", 253)+"  ", f.orgID))
}

// TestProjects_RefuseANULInATextField refuses a NUL in each text field on
// create and on PATCH with a 400 naming the field, before the store.
// PostgreSQL cannot store a NUL in a text column, so one reaching the store
// answered 500 there (FAILURE_PATTERNS 371, 1079), while SQLite stored it.
func TestProjects_RefuseANULInATextField(t *testing.T) {
	for _, field := range []string{"name", "description", "scm_url", "scm_branch"} {
		t.Run(field, func(t *testing.T) {
			f := newProjectFixture(t)
			withNUL := "a\x00b"
			if field == "scm_url" {
				withNUL = "https://example.invalid/a\x00b.git"
			}

			create := map[string]any{"name": "nul", "organization": f.orgID, "scm_url": "https://example.invalid/a.git"}
			create[field] = withNUL
			body, err := json.Marshal(create)
			if err != nil {
				t.Fatal(err)
			}
			status, resp := f.do(t, http.MethodPost, "/api/v1/projects", json.RawMessage(body))
			if status != http.StatusBadRequest || !strings.Contains(string(resp), field) || !strings.Contains(string(resp), "NUL") {
				t.Errorf("creating with a NUL in %s: status %d, want 400 naming the field and the NUL; body %s", field, status, resp)
			}

			id := f.createProject(t, "automation")
			patch, err := json.Marshal(map[string]string{field: withNUL})
			if err != nil {
				t.Fatal(err)
			}
			f.refusedUnchanged(t, id, string(patch), http.StatusBadRequest, field, "NUL")
		})
	}
}
