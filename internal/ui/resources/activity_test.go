package resources_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
)

// This file proves the activity stream records what the *web UI* does.
//
// That is the whole reason the recording lives in a store decorator rather
// than in the JSON API's handlers. The UI's writers reach the access store
// directly and pass through no API handler at all, so a recording call
// placed in a handler would have covered the API surface, left the UI
// surface silent, and looked complete in every test that exercised the
// covered one. These assertions drive the real UI handler, through the real
// router, with a real CSRF token and a real session, and then read the
// stream.

// streamEntries reads the whole stream the conformance fixture writes to.
func streamEntries(t *testing.T) []activity.Entry {
	t.Helper()
	registerViews(t)

	entries, err := conformanceStream.List(context.Background(), activity.Query{})
	if err != nil {
		t.Fatalf("listing the activity stream: %v", err)
	}
	return entries
}

// findEntry returns the newest entry naming object as its name, if any.
func findEntry(entries []activity.Entry, action activity.Action, name string) (activity.Entry, bool) {
	for _, e := range entries {
		if e.Action == action && e.ObjectName == name {
			return e, true
		}
	}
	return activity.Entry{}, false
}

func TestActivityStream_RecordsAWriteMadeThroughTheWebUI(t *testing.T) {
	h := newHarness(t, adminIdentity)

	name := uniqueName(t, "ui-recorded-organization")
	w := h.post(t, "/ui/organizations", map[string]string{"name": name})
	if w.Code != http.StatusOK && w.Code != http.StatusSeeOther {
		t.Fatalf("POST /ui/organizations = %d, want a successful write", w.Code)
	}

	entry, found := findEntry(streamEntries(t), activity.ActionCreated, name)
	if !found {
		t.Fatal("a create performed through the web UI left no entry in the activity stream, " +
			"which is exactly the gap a handler-side recording call would leave")
	}

	// The acting subject, not a placeholder. An entry recorded against
	// "system" or an empty string would tell a reader that a change was
	// made and nothing about who made it.
	if entry.Actor != adminIdentity.Subject {
		t.Errorf("the entry is attributed to %q, want the signed-in subject %q", entry.Actor, adminIdentity.Subject)
	}
	if entry.ObjectKind != activity.KindOrganization {
		t.Errorf("the entry names kind %q, want %q", entry.ObjectKind, activity.KindOrganization)
	}
	if entry.ObjectID <= 0 {
		t.Errorf("the entry names object id %d, which identifies nothing", entry.ObjectID)
	}
}

func TestActivityStream_RendersWhatItRecorded(t *testing.T) {
	h := newHarness(t, adminIdentity)

	name := uniqueName(t, "ui-rendered-organization")
	if w := h.post(t, "/ui/organizations", map[string]string{"name": name}); w.Code >= http.StatusBadRequest {
		t.Fatalf("POST /ui/organizations = %d, want a successful write", w.Code)
	}

	body := h.get(t, "/ui/activity").Body.String()
	if !strings.Contains(body, name) {
		t.Errorf("the Activity Stream does not render the organization it just recorded (%q)", name)
	}
	if !strings.Contains(body, adminIdentity.Subject) {
		t.Errorf("the Activity Stream does not name the subject that made the change (%q)", adminIdentity.Subject)
	}
}

func TestActivityStream_OffersNoWayToChangeWhatItSays(t *testing.T) {
	h := newHarness(t, adminIdentity)

	body := h.get(t, "/ui/activity").Body.String()

	// An append-only record with an edit or delete control is not one. The
	// absence is structural -- no route is mounted and no handler is bound
	// -- and this asserts the structure reaches the page.
	for _, forbidden := range []string{"/ui/activity/new", "btn-danger"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the Activity Stream renders %q, so it offers a way to alter the audit trail", forbidden)
		}
	}

	// The routes themselves, not only the controls. Hiding a control while
	// leaving the endpoint open would be security by CSS, which this suite
	// refuses elsewhere for the same reason.
	if w := h.post(t, "/ui/activity", map[string]string{"actor": "somebody-else"}); w.Code < http.StatusBadRequest {
		t.Errorf("POST /ui/activity = %d, want a refusal: an audit trail a caller can append to is forgeable", w.Code)
	}
}

// TestDuplicateName_IsAFieldErrorRatherThanAnInternalError pins what a
// person sees after typing a name something else already has.
//
// It used to be HTTP 500 and the plain text "internal error", with the
// rest of the form discarded: the resource's Writer handed the store's
// uniqueness sentinel straight back, and view.Bind routes any error it
// cannot attribute to a field to serverError. view.FieldFault's own doc
// comment names this exact case ("a name already taken") as one of the
// three it exists for, and until now the only production caller of it in
// the module was the schedules resource.
//
// Both resources are checked here rather than one, because they failed
// through two different sentinels (access.ErrExists and launch.ErrExists)
// reaching the same place, and a fix that only proved one would say
// nothing about the other.
func TestDuplicateName_IsAFieldErrorRatherThanAnInternalError(t *testing.T) {
	h := newHarness(t, adminIdentity)

	name := uniqueName(t, "already-taken-organization")
	if w := h.post(t, "/ui/organizations", map[string]string{"name": name}); w.Code >= http.StatusBadRequest {
		t.Fatalf("the first POST /ui/organizations = %d, want it to succeed", w.Code)
	}

	w := h.post(t, "/ui/organizations", map[string]string{"name": name})
	if w.Code >= http.StatusInternalServerError {
		t.Fatalf("the second POST /ui/organizations = %d and body %q, want the form back rather than an error page",
			w.Code, strings.TrimSpace(w.Body.String()))
	}
	if body := w.Body.String(); !strings.Contains(body, "already exists") {
		t.Errorf("the response does not tell the submitter the name is taken; body = %q", body)
	}
}
