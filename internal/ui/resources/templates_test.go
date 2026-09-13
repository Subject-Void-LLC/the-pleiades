package resources_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers the launch form, which is the one place in this UI where
// the controls differ from record to record.
//
// A template declares which of its fields a launch may override, and the
// resolver reports every other one as ignored rather than applying it. A
// form that rendered a locked field would therefore be offering a control
// that does nothing: the operator types a value, submits, and the run uses
// something else. This repository has shipped affordances that silently did
// nothing before and recorded it, so the form is built from the record.

// launchForm fetches the launch prompt for one seeded template.
func launchForm(t *testing.T, h *harness, id string) string {
	t.Helper()
	w := h.get(t, "/ui/templates/"+id+"/launch")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/templates/%s/launch = %d, want 200", id, w.Code)
	}
	return w.Body.String()
}

func TestTemplatesView_TheLaunchFormRendersOnlyWhatTheTemplateOpened(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// Template 1 opens limit and forks, and locks verbosity, timeout,
	// extra_vars and labels.
	body := launchForm(t, h, "1")

	for _, opened := range []string{`name="limit"`, `name="forks"`} {
		if !strings.Contains(body, opened) {
			t.Errorf("the launch form has no control for %s, which this template opened", opened)
		}
	}

	// The locked ones must not appear at all. Rendering one disabled would
	// be no better: the point is that the operator is not invited to decide
	// something the template author already decided.
	for _, locked := range []string{`name="verbosity"`, `name="timeout"`, `name="extra_vars"`, `name="labels"`} {
		if strings.Contains(body, locked) {
			t.Errorf("the launch form renders a control for %s, which this template locks: a control whose value is then ignored is an affordance that does nothing", locked)
		}
	}

	// The survey is asked on the same form, under prefixed names so a
	// question whose variable matches a launch field cannot collide with
	// it.
	if !strings.Contains(body, `name="answer_version"`) {
		t.Error("the launch form does not ask the survey's own question")
	}

	// And a password question renders as a password control. A survey
	// answer of that type is encrypted at rest and reads back as a
	// redaction marker; typing it into a visible box that a browser offers
	// to remember would undo both.
	if !strings.Contains(body, `name="answer_vault_token"`) {
		t.Fatal("the launch form does not ask the survey's password question")
	}
	if !strings.Contains(body, `type="password"`) {
		t.Error("the survey's password question is not rendered as a password control")
	}
	if !strings.Contains(body, `autocomplete="new-password"`) {
		t.Error("the password control invites the browser to remember a secret that belongs to one run")
	}
}

func TestTemplatesView_ATemplateThatOpensNothingStillOffersAConfirmation(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// Template 2 opens no field and asks nothing. The form is then a
	// confirmation, which is the right answer for something that launches
	// production work: the alternative is a button that runs a fleet-wide
	// change on one click from a list.
	body := launchForm(t, h, "2")

	for _, field := range []string{`name="limit"`, `name="forks"`, `name="verbosity"`, `name="answer_`} {
		if strings.Contains(body, field) {
			t.Errorf("the launch form of a template that opens nothing renders a control for %s", field)
		}
	}
	if !strings.Contains(body, "Launch") {
		t.Error("the launch form carries no submit control")
	}
}

func TestTemplatesView_RefusesASubmissionCarryingALockedField(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// The form never offered verbosity, so a submission carrying it did not
	// come from the form. It is refused rather than ignored: the launch
	// path's ignored-field report exists for API callers who choose their
	// own request body, and a form post that smuggles a control is a
	// different thing from a caller asking for something they are not
	// allowed.
	w := h.post(t, "/ui/templates/1/launch", map[string]string{
		"limit":     "edge-01",
		"verbosity": "4",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST with a locked field = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestTemplatesView_TheDetailPageSaysWhatIsNotBuilt(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/templates/1")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/templates/1 = %d, want 200", w.Code)
	}
	body := w.Body.String()

	// Four sections that reach a port, and one that says out loud that it
	// does not. Each is a tab on the record, so this asserts the tab is
	// offered rather than merely that its title appears somewhere: a title
	// in a heading and a title in a tab strip that links nowhere look the
	// same to strings.Contains.
	for _, section := range []string{"Survey", "Access", "Notifications", "Jobs"} {
		slug := view.TabSlug(section)
		if !strings.Contains(body, "tab="+slug) {
			t.Errorf("the detail page offers no %q tab", section)
		}
	}

	// An empty Notifications table would read as "no policies are
	// configured", which is indistinguishable from a working section with
	// no records, and this project has shipped that ambiguity twice.
	if !strings.Contains(h.section(t, "/ui/templates/1", "Notifications"), "Declared, not implemented") {
		t.Error("the Notifications section renders as though it were backed by something")
	}

	// The sections that do reach a port render their tables rather than
	// that panel, which is the other half of the same claim.
	if strings.Contains(h.section(t, "/ui/templates/1", "Access"), "Declared, not implemented") {
		t.Error("the Access section claims to be unimplemented")
	}

	// The survey's questions are listed; the answers to them are not. What
	// a survey asks is not secret, and what somebody answered is.
	body = h.section(t, "/ui/templates/1", "Survey")
	if !strings.Contains(body, "version") {
		t.Error("the Survey section does not list the question the template asks")
	}
}
