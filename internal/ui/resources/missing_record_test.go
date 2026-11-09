// Package resources_test: an edit or delete of a record that is not stored
// answers 404 on every writable view, as opening its edit form does.
package resources_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// missingRecordID names a record no fixture stores, on views keyed by a
// number and by a name alike.
const missingRecordID = "999999"

// wantMissingRecordViews is every writable view this suite builds over a
// store, named so a view that stops being reached here fails rather than
// quietly dropping out of the table.
var wantMissingRecordViews = []string{
	"access", "contacts", "credential-types", "credentials", "devices", "inventories",
	"organizations", "projects", "schedules", "teams", "templates", "users",
}

// TestViewConformance_AMissingRecordIsNotFound edits and deletes a record
// that is not stored, through the real handler, on every view with a writer.
// Each used to answer 500, telling somebody whose record had been removed
// that the server was broken. The edit submits a real record's own edit
// form, so it passes validation and reaches the writer, which is where the
// store reports the record missing.
func TestViewConformance_AMissingRecordIsNotFound(t *testing.T) {
	h := newHarness(t, adminIdentity)

	var covered []string
	for _, d := range view.All() {
		if d.Handlers == nil || d.Handlers.Update == nil || d.Handlers.Delete == nil || d.Handlers.Form == nil {
			continue
		}
		covered = append(covered, d.Name)
		t.Run(d.Name, func(t *testing.T) {
			id := firstEditableRecordID(t, h, d.Name)
			if id == "" {
				t.Fatal("no seeded editable record to take an edit form from")
			}
			if id == missingRecordID {
				t.Fatalf("a fixture is stored under %s, which this test needs to be missing", missingRecordID)
			}
			body := h.get(t, "/ui/"+d.Name+"/"+id+"/edit").Body.String()
			submission := url.Values{}
			for _, name := range renderedControls(body) {
				for _, value := range renderedValues(body, name) {
					submission.Add(name, value)
				}
			}

			path := "/ui/" + d.Name + "/" + missingRecordID
			if w := h.get(t, path+"/edit"); w.Code != http.StatusNotFound {
				t.Errorf("GET the edit form of a missing record = %d, want 404", w.Code)
			}
			if w := h.postValues(t, path, submission); w.Code != http.StatusNotFound {
				t.Errorf("editing a missing record = %d, want 404; errors: %s", w.Code, formErrors(w.Body.String()))
			}
			if w := h.post(t, path, map[string]string{"_method": http.MethodDelete}); w.Code != http.StatusNotFound {
				t.Errorf("deleting a missing record = %d, want 404", w.Code)
			}
		})
	}
	slices.Sort(covered)
	if !slices.Equal(covered, wantMissingRecordViews) {
		t.Errorf("the writable views are %v, want %v: update the table and give a new one its 404", covered, wantMissingRecordViews)
	}
}
