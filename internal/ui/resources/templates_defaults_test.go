package resources_test

import (
	"net/http"
	"path"
	"strings"
	"testing"
)

// This file covers the Templates EDIT form's per-kind execution fields:
// forks, limit and the rest, each with its own "prompt on launch" checkbox
// (AWX_PARITY_ROADMAP.md B1). It replaces the old "prompt on launch"
// multi-select, and it is a different form from the one templates_test.go
// covers: the launch form renders only what a template already opened,
// where this one is what opens a field in the first place, prefilled with
// what the template is saved to run with.
//
// Only an edit gets these controls. Template 1 is a runbook seeded with
// Defaults{limit, forks, verbosity} and Prompts{limit, forks}; Template 2
// is a playbook, the kind that carries job_tags and skip_tags. The pair is
// the point: it is what proves the fields rendered are the record's own
// kind, not the union of every registered kind.

// editForm fetches the edit form for one seeded template.
func editForm(t *testing.T, h *harness, id string) string {
	t.Helper()
	w := h.get(t, "/ui/templates/"+id+"/edit")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/templates/%s/edit = %d, want 200", id, w.Code)
	}
	return w.Body.String()
}

// controlTag returns the opening tag for one named control, so a test can
// inspect its own attributes (checked, value) without a false match against
// a neighbour whose name merely contains the same substring.
func controlTag(body, name string) string {
	needle := `name="` + name + `"`
	idx := strings.Index(body, needle)
	if idx == -1 {
		return ""
	}
	start := strings.LastIndex(body[:idx], "<")
	if start == -1 {
		return ""
	}
	end := strings.IndexAny(body[idx:], ">")
	if end == -1 {
		return ""
	}
	return body[start : idx+end]
}

func TestTemplatesView_TheEditFormRendersOnlyItsOwnKindsFields(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// Template 1 is a runbook. Every runbook field renders, each with its
	// own prompt checkbox beside it.
	body := editForm(t, h, "1")
	for _, field := range []string{"limit", "verbosity", "forks", "timeout", "extra_vars", "labels"} {
		if !strings.Contains(body, `name="`+field+`"`) {
			t.Errorf("the edit form has no control for %s, which the runbook kind declares", field)
		}
		if !strings.Contains(body, `name="`+field+`_prompt"`) {
			t.Errorf("the edit form has no prompt checkbox for %s", field)
		}
	}

	// The playbook-only fields must not appear at all. A control for a
	// field this template's kind has never declared is the same affordance
	// that does nothing the launch form was rebuilt to avoid.
	for _, field := range []string{"job_tags", "skip_tags"} {
		if strings.Contains(body, `name="`+field+`"`) {
			t.Errorf("the edit form of a runbook template renders %s, which only the playbook kind declares", field)
		}
	}
}

func TestTemplatesView_TheEditFormOffersThePlaybookKindsOwnFields(t *testing.T) {
	h := newHarness(t, adminIdentity)

	// Template 2 is a playbook. job_tags and skip_tags are the two fields
	// that exist only on that kind, which is the concrete reason this form
	// has to be resolved per record instead of declared once for every
	// template regardless of what it runs.
	body := editForm(t, h, "2")
	for _, field := range []string{"job_tags", "skip_tags"} {
		if !strings.Contains(body, `name="`+field+`"`) {
			t.Errorf("the edit form of a playbook template has no control for %s", field)
		}
	}
}

func TestTemplatesView_TheEditFormPrefillsSavedValuesAndPromptability(t *testing.T) {
	h := newHarness(t, adminIdentity)

	body := editForm(t, h, "1")

	// Template 1's saved defaults: limit "edge-*", forks 5, verbosity 1.
	if !strings.Contains(controlTag(body, "limit"), `value="edge-*"`) {
		t.Error("the edit form does not prefill limit with its saved value")
	}
	if !strings.Contains(controlTag(body, "forks"), `value="5"`) {
		t.Error("the edit form does not prefill forks with its saved value")
	}

	// limit and forks are promptable; verbosity is not, so only the first
	// two checkboxes should carry the checked attribute.
	if !strings.Contains(controlTag(body, "limit_prompt"), "checked") {
		t.Error("limit_prompt is not checked, though the template's Prompts names limit")
	}
	if !strings.Contains(controlTag(body, "forks_prompt"), "checked") {
		t.Error("forks_prompt is not checked, though the template's Prompts names forks")
	}
	if strings.Contains(controlTag(body, "verbosity_prompt"), "checked") {
		t.Error("verbosity_prompt is checked, though the template's Prompts does not name verbosity")
	}
}

// TestTemplatesView_TheListShowsActivityAndLastRan is B2's own gate
// (AWX_PARITY_ROADMAP.md Section 5): a template with a job shows its most
// recent outcome and when it ran, read from one batched query across the
// page rather than a job lookup per row -- the fake store's own comment
// records that job-0001 is the only job carrying TemplateID 1, so a
// correct read here is proof the batched RecentForTemplates call, not a
// coincidence of iteration order, is what produced it.
func TestTemplatesView_TheListShowsActivityAndLastRan(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.get(t, "/ui/templates")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /ui/templates = %d, want 200", w.Code)
	}
	body := w.Body.String()

	for _, column := range []string{"ACTIVITY", "LAST RAN"} {
		if !strings.Contains(body, column) {
			t.Errorf("the templates list has no %s column", column)
		}
	}
	if !strings.Contains(body, "completed") {
		t.Error("template 1's Activity cell does not show its one recorded job's outcome")
	}
	if !strings.Contains(body, "2023-11-14") {
		// job-0001's CreatedAt is time.Unix(1_700_000_000, 0), which falls
		// on 2023-11-14 in UTC.
		t.Error("template 1's Last Ran cell does not show its job's timestamp")
	}
}

// TestTemplatesView_TickingAPromptCheckboxOpensTheFieldOnTheLaunchForm is
// B1's own gate (AWX_PARITY_ROADMAP.md Section 5): ticking the checkbox
// makes the field appear on that template's launch form, and leaving it
// unticked keeps it off, asserted end to end rather than against the stored
// Prompts list alone -- the launch form is what an operator actually sees.
//
// It creates its own template rather than editing the shared fixture's:
// the store behind this suite's harness lives for the whole test binary
// (newTestTemplateStore's own comment says so), so a test that saved an
// edit to template 1 or 2 would leave it mutated for every test that runs
// after it in the same package, which is exactly the kind of test the
// edit-form conformance suite exists to police, not to be.
func TestTemplatesView_TickingAPromptCheckboxOpensTheFieldOnTheLaunchForm(t *testing.T) {
	h := newHarness(t, adminIdentity)

	w := h.post(t, "/ui/templates", map[string]string{
		"name":       "conformance-b1-gate",
		"definition": "runbook:conformance",
		"inventory":  "1",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST create = %d, want 303: %s", w.Code, w.Body.String())
	}
	id := path.Base(w.Header().Get("Location"))

	if strings.Contains(launchForm(t, h, id), `name="verbosity"`) {
		t.Fatal("verbosity already appears on the launch form of a freshly created template")
	}

	// Tick verbosity's checkbox and save a value behind it: ticking a
	// checkbox with nothing set would open a field onto nothing.
	w = h.post(t, "/ui/templates/"+id, map[string]string{
		"name":             "conformance-b1-gate",
		"verbosity":        "2",
		"verbosity_prompt": "true",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST the edit = %d, want 303: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(launchForm(t, h, id), `name="verbosity"`) {
		t.Error("ticking verbosity's prompt checkbox did not open it on the launch form")
	}

	// Unticking it again closes it.
	w = h.post(t, "/ui/templates/"+id, map[string]string{
		"name":      "conformance-b1-gate",
		"verbosity": "2",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST the edit = %d, want 303: %s", w.Code, w.Body.String())
	}
	if strings.Contains(launchForm(t, h, id), `name="verbosity"`) {
		t.Error("leaving verbosity_prompt unticked did not close it on the launch form")
	}
}
