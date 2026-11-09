// Package api_test holds, in this file, the template writes the database
// would refuse: a survey question with no label, which the survey_question
// schema declares NotEmpty, and a name longer than the 253 bytes the
// launchable row that copies it allows. Each used to reach the store's
// insert, fail there, and answer 500 "internal error" on every path that
// writes a template (create, PUT /templates/{id}/survey and PATCH), where a
// caller's own mistake deserves a 400 that says what to fix.
package api_test

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// unlabelledQuestion is a survey with one question that has no label, or
// only spaces for one.
func unlabelledQuestion(label string) string {
	return fmt.Sprintf(`{"enabled": true, "questions": [
		{"variable": "version", "label": "VERSION", "type": "text"},
		{"variable": "reason", "label": %q, "type": "textarea"}
	]}`, label)
}

// TestTemplateAPI_RefusesAQuestionWithNoLabelOnEveryWritePath holds the
// label rule on all three paths that write a survey, each a 400 naming the
// question by its variable.
func TestTemplateAPI_RefusesAQuestionWithNoLabelOnEveryWritePath(t *testing.T) {
	for _, label := range []string{"", "   "} {
		t.Run(fmt.Sprintf("create, label %q", label), func(t *testing.T) {
			f := newTemplateFixture(t)
			rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates", fmt.Sprintf(`{
				"name": "patch", "kind": "runbook", "definition": "patch-edge", "inventory": %d,
				"survey": %s
			}`, f.invA, unlabelledQuestion(label)))
			assertNoLabelRefusal(t, rec.Code, rec.Body.String())

			// Nothing was created under the name.
			list := doJSON(t, f.router, http.MethodGet, "/api/v1/templates", "")
			if strings.Contains(list.Body.String(), `"patch"`) {
				t.Errorf("a refused create left a template behind: %s", list.Body.String())
			}
		})

		t.Run(fmt.Sprintf("PUT survey, label %q", label), func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			before := f.readTemplate(t, created.ID)

			rec := doJSON(t, f.router, http.MethodPut, "/api/v1/templates/"+strconv.Itoa(created.ID)+"/survey",
				`{"survey": `+unlabelledQuestion(label)+`}`)
			assertNoLabelRefusal(t, rec.Code, rec.Body.String())
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused survey write changed the template:\n before %+v\n after  %+v", before, after)
			}
		})

		t.Run(fmt.Sprintf("PATCH, label %q", label), func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))
			before := f.readTemplate(t, created.ID)

			status, body := f.patchTemplate(t, created.ID, `{"survey": `+unlabelledQuestion(label)+`}`)
			assertNoLabelRefusal(t, status, body)
			if after := f.readTemplate(t, created.ID); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused PATCH changed the template:\n before %+v\n after  %+v", before, after)
			}
		})
	}
}

// assertNoLabelRefusal checks a write was refused as the caller's mistake,
// naming the unlabelled question rather than the labelled one beside it.
func assertNoLabelRefusal(t *testing.T, status int, body string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "reason") || !strings.Contains(body, "label") {
		t.Errorf("the refusal %s does not name the question \"reason\" and its missing label", body)
	}
}

// TestTemplateAPI_RefusesANameLongerThanTheLaunchableRowHolds holds the name
// bound on both paths that write a name, at the boundary: 253 bytes is
// accepted, 254 is a 400. The bound is in bytes, as the database's is, so a
// name of two-byte characters reaches it at half the length.
func TestTemplateAPI_RefusesANameLongerThanTheLaunchableRowHolds(t *testing.T) {
	longest := strings.Repeat("n", 253)
	cases := map[string]struct {
		name string
		want int
	}{
		"253 bytes":                    {longest, http.StatusCreated},
		"254 bytes":                    {longest + "n", http.StatusBadRequest},
		"127 two-byte characters":      {strings.Repeat("\xc3\xa9", 127), http.StatusBadRequest},
		"253 bytes inside outer space": {"  " + longest + "  ", http.StatusCreated},
	}

	for label, tc := range cases {
		t.Run("create, "+label, func(t *testing.T) {
			f := newTemplateFixture(t)
			rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates", fmt.Sprintf(`{
				"name": %q, "kind": "runbook", "definition": "patch-edge", "inventory": %d
			}`, tc.name, f.invA))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "253") {
				t.Errorf("the refusal %s does not say what the bound is", rec.Body.String())
			}
		})

		t.Run("PATCH, "+label, func(t *testing.T) {
			f := newTemplateFixture(t)
			created := f.createTemplate(t, f.fullTemplateBody("patch"))

			want := http.StatusOK
			if tc.want != http.StatusCreated {
				want = tc.want
			}
			status, body := f.patchTemplate(t, created.ID, fmt.Sprintf(`{"name": %q}`, tc.name))
			if status != want {
				t.Fatalf("status = %d, want %d: %s", status, want, body)
			}
			if want != http.StatusOK {
				if got := f.readTemplate(t, created.ID); got.Name != "patch" {
					t.Errorf("name = %q, want the refused rename not applied", got.Name)
				}
			}
		})
	}
}
