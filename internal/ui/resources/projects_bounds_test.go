// Package resources_test's project bounds coverage: the Projects form's
// length bounds are the database's own, read from internal/project, so a
// value the database would refuse is a field error beside the control
// (view.Validate enforces each field's MaxLen server-side) rather than a
// write the database refuses.
package resources_test

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// TestProjectsView_RefusesAFieldLongerThanTheDatabaseHolds holds each of the
// form's length bounds to the database's, on create and on edit: the bound is
// accepted and one byte past it is a field error on that control, not a
// write the database refuses.
func TestProjectsView_RefusesAFieldLongerThanTheDatabaseHolds(t *testing.T) {
	h := newHarness(t, adminIdentity)
	orgID := optionValue(t, body(t, h, "/ui/projects/new"), "organization", "acme")

	for _, bound := range []struct {
		field string
		max   int
		// value builds a valid value of n bytes for the field. A name is
		// unique per run, since the store is shared by every run of this
		// package under -count.
		value func(t *testing.T, n int) string
	}{
		{"name", project.MaxNameBytes, func(t *testing.T, n int) string {
			unique := uniqueName(t, "bounded")
			return unique + strings.Repeat("n", n-len(unique))
		}},
		{"description", project.MaxDescriptionBytes, func(_ *testing.T, n int) string { return strings.Repeat("d", n) }},
		{"scm_url", project.MaxSCMURLBytes, func(_ *testing.T, n int) string {
			const prefix = "https://git.example.test/"
			return prefix + strings.Repeat("u", n-len(prefix))
		}},
		{"scm_branch", project.MaxSCMBranchBytes, func(_ *testing.T, n int) string { return strings.Repeat("b", n) }},
	} {
		t.Run(bound.field, func(t *testing.T) {
			submission := func(n int) map[string]string {
				values := map[string]string{
					"name": uniqueName(t, "bounded"), "organization": orgID, "scm_type": "git",
					"scm_url": "https://git.example.test/team/bounded.git",
				}
				values[bound.field] = bound.value(t, n)
				return values
			}

			w := h.post(t, "/ui/projects", submission(bound.max+1))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("creating with a %s of %d bytes = %d, want 422: %s", bound.field, bound.max+1, w.Code, w.Body.String())
			}
			// The field error view.Validate renders beside the control,
			// not the maxlength attribute, which carries the bound too.
			if want := "must be " + strconv.Itoa(bound.max); !strings.Contains(w.Body.String(), want) {
				t.Errorf("the refusal does not say %q", want)
			}

			// Editing the seeded project the same way is refused too, and
			// changes nothing.
			before, err := conformanceProjectStore.Get(context.Background(), 1)
			if err != nil {
				t.Fatalf("reading the seeded project: %v", err)
			}
			edit := renderedFormValues(t, body(t, h, "/ui/projects/1/edit"))
			edit[bound.field] = bound.value(t, bound.max+1)
			if w := h.post(t, "/ui/projects/1", edit); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("editing to a %s of %d bytes = %d, want 422", bound.field, bound.max+1, w.Code)
			}
			if after, _ := conformanceProjectStore.Get(context.Background(), 1); !reflect.DeepEqual(after, before) {
				t.Errorf("a refused edit changed the project:\n before %+v\n after  %+v", before, after)
			}

			if w := h.post(t, "/ui/projects", submission(bound.max)); w.Code >= http.StatusBadRequest {
				t.Errorf("creating with a %s of exactly %d bytes = %d, want it accepted: %s",
					bound.field, bound.max, w.Code, formErrors(w.Body.String()))
			}
		})
	}
}
