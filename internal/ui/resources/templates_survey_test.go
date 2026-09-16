// This file covers the survey's write path: the four controls that author
// the questions a launching operator is asked.
//
// The survey was the largest read-only object in the product. Its model has
// been complete since the Templates view was built -- seven types, per-type
// validation, encrypted answers -- and the only way to author one was the
// JSON API or the database.
//
// Every test here creates its OWN template. The fixture's is shared with
// every other test in this package and with the conformance suite, and a
// survey question added to it changes what that suite's launch form
// renders. That has already happened once on the credential-type side.
package resources_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestTemplatesSurvey_AddQuestionAppendsAndEnables proves the add control
// writes a question and turns the survey on.
//
// Enabling matters as much as appending. A survey holding questions that is
// disabled asks nothing at launch, so an add that left Enabled false would
// redirect to a page listing a question the launch form never renders --
// the built-but-unreachable shape, arriving one row at a time.
func TestTemplatesSurvey_AddQuestionAppendsAndEnables(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)

	if w := h.post(t, "/ui/templates/"+id+"/add-question", map[string]string{
		"variable": "release_tag",
		"label":    "Release tag",
		"type":     "text",
		"required": "true",
		"help":     "Which tag to deploy.",
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("adding a question = %d, want a redirect: %s", w.Code, w.Body.String())
	}

	section := h.section(t, "/ui/templates/"+id, "Survey")
	for _, want := range []string{"release_tag", "Release tag"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Survey section does not show %q after adding it", want)
		}
	}

	// The launch form is the thing that proves the survey is ON. The
	// section would list the question either way.
	launch := h.get(t, "/ui/templates/"+id+"/launch").Body.String()
	if !strings.Contains(launch, `name="answer_release_tag"`) {
		t.Error("the launch form does not ask the question that was just added, so the survey was written but left disabled")
	}
}

// TestTemplatesSurvey_EditQuestionResubmittedUnchangedChangesNothing is the
// prefill test, and it is the one that catches the failure this whole seam
// exists to prevent: a form that renders a row's stored values as empty
// boxes and saves the blanks over them.
//
// It compares against what this test itself wrote in, never against what
// the page showed before. A before-and-after comparison is blind to
// uniform loss: if the prefill drops every control, before and after agree
// perfectly and the test passes while the data is gone.
func TestTemplatesSurvey_EditQuestionResubmittedUnchangedChangesNothing(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)

	cases := []struct {
		name string
		want map[string]string
	}{
		{
			// Distinguishing values in every control. Two questions both
			// sitting at their zero values would round-trip through a
			// prefill that returns an empty map.
			name: "a bounded text question",
			want: map[string]string{
				"label": "Bounded Round Trip", "type": "text",
				"required": "true", "help": "Help that must survive the round trip.",
				"default": "a-default", "min": "3", "max": "40",
			},
		},
		{
			name: "a choice question",
			want: map[string]string{
				"label": "Choice Round Trip", "type": "multiplechoice",
				"help": "Other help.", "choices": "alpha, beta", "default": "beta",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			variable := strings.ReplaceAll(uniqueName(t, "rt"), "-", "_")
			add := map[string]string{"variable": variable}
			for k, v := range tc.want {
				add[k] = v
			}
			if w := h.post(t, "/ui/templates/"+id+"/add-question", add); w.Code != http.StatusSeeOther {
				t.Fatalf("adding the question = %d: %s", w.Code, w.Body.String())
			}

			// Read back off the RENDERED form rather than reusing the map
			// above. A hardcoded resubmission would exercise a POST body
			// this product never sends and would pass against a prefill
			// that returns nothing at all.
			form := h.get(t, "/ui/templates/"+id+"/edit-question/"+variable).Body.String()
			submitted := renderedFormValues(t, form)
			if len(submitted) < len(tc.want) {
				t.Fatalf("read %d control(s) off the form but put in %d, so the prefill already lost something: %v",
					len(submitted), len(tc.want), submitted)
			}

			if w := h.post(t, "/ui/templates/"+id+"/edit-question/"+variable, submitted); w.Code != http.StatusSeeOther {
				t.Fatalf("resubmitting the form unchanged = %d, want a redirect: %s", w.Code, w.Body.String())
			}

			got := renderedFormValues(t, h.get(t, "/ui/templates/"+id+"/edit-question/"+variable).Body.String())
			for name, want := range tc.want {
				if got[name] != want {
					t.Errorf("after resubmitting the form unchanged, %s = %q, want %q: the prefill dropped it and the save wrote the blank",
						name, got[name], want)
				}
			}
		})
	}
}

// TestTemplatesSurvey_EditQuestionWritesWhatWasChanged is the other half,
// and it is what stops the test above passing against a Submit that does
// nothing at all.
func TestTemplatesSurvey_EditQuestionWritesWhatWasChanged(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)
	variable := strings.ReplaceAll(uniqueName(t, "edit"), "-", "_")

	if w := h.post(t, "/ui/templates/"+id+"/add-question", map[string]string{
		"variable": variable, "label": "Before", "type": "text", "help": "before",
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("adding the question = %d: %s", w.Code, w.Body.String())
	}

	form := renderedFormValues(t, h.get(t, "/ui/templates/"+id+"/edit-question/"+variable).Body.String())
	form["label"] = "After"
	form["required"] = "true"
	if w := h.post(t, "/ui/templates/"+id+"/edit-question/"+variable, form); w.Code != http.StatusSeeOther {
		t.Fatalf("editing the question = %d: %s", w.Code, w.Body.String())
	}

	got := renderedFormValues(t, h.get(t, "/ui/templates/"+id+"/edit-question/"+variable).Body.String())
	if got["label"] != "After" {
		t.Errorf("label = %q, want %q: the edit did not write", got["label"], "After")
	}
	if got["required"] != "true" {
		t.Errorf("required = %q, want %q: the checkbox did not write", got["required"], "true")
	}
	// The one control the edit form withholds. Renaming the variable in
	// place would leave every runbook reading it unset, while looking like
	// a spelling correction.
	if strings.Contains(h.get(t, "/ui/templates/"+id+"/edit-question/"+variable).Body.String(), `name="variable"`) {
		t.Error("the edit form offers the variable, but renaming one in place strands the answer in every saved configuration keyed by the old name")
	}
}

// TestTemplatesSurvey_MoveReordersAndIsWithheldAtTheEnds covers the two
// controls that author the order, and the gate that keeps them honest.
//
// The order is the authored thing: a question that only makes sense after
// another has been answered has to render after it. Withholding the control
// at each end is the "do not offer a choice that can only fail" rule -- a
// "move up" on the first row is a button whose only possible outcome is a
// refusal.
func TestTemplatesSurvey_MoveReordersAndIsWithheldAtTheEnds(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)

	for _, v := range []string{"first_q", "second_q", "third_q"} {
		if w := h.post(t, "/ui/templates/"+id+"/add-question", map[string]string{
			"variable": v, "label": v, "type": "text",
		}); w.Code != http.StatusSeeOther {
			t.Fatalf("adding %s = %d: %s", v, w.Code, w.Body.String())
		}
	}

	section := h.section(t, "/ui/templates/"+id, "Survey")
	if got := surveyOrder(section); !equalStrings(got, []string{"first_q", "second_q", "third_q"}) {
		t.Fatalf("the questions render in order %v, want the order they were added", got)
	}

	// Neither end offers the control that cannot work there.
	if strings.Contains(section, "move-question-up/first_q") {
		t.Error("the first question offers Move up, whose only possible outcome is a refusal")
	}
	if strings.Contains(section, "move-question-down/third_q") {
		t.Error("the last question offers Move down, whose only possible outcome is a refusal")
	}
	// ...and the middle one offers both, which is what proves the two
	// assertions above are not passing because no control renders at all.
	for _, want := range []string{"move-question-up/second_q", "move-question-down/second_q"} {
		if !strings.Contains(section, want) {
			t.Errorf("the middle question does not offer %s, so the checks above prove nothing", want)
		}
	}

	if w := h.post(t, "/ui/templates/"+id+"/move-question-up/third_q", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("moving the last question up = %d: %s", w.Code, w.Body.String())
	}
	if got := surveyOrder(h.section(t, "/ui/templates/"+id, "Survey")); !equalStrings(got, []string{"first_q", "third_q", "second_q"}) {
		t.Errorf("after moving third_q up the order is %v, want [first_q third_q second_q]", got)
	}

	// The order the launch form asks in is the stored order, not the
	// section's rendering of it. A reorder that only changed the table
	// would be a reorder of nothing.
	if got := launchOrder(h.get(t, "/ui/templates/"+id+"/launch").Body.String()); !equalStrings(got, []string{"first_q", "third_q", "second_q"}) {
		t.Errorf("the launch form asks in order %v, want the reordered [first_q third_q second_q]", got)
	}
}

// TestTemplatesSurvey_RemoveQuestionStopsItBeingAsked covers the control
// that makes the rest safe to use: an add with no remove is a one-way door,
// and the first typo through it is permanent.
func TestTemplatesSurvey_RemoveQuestionStopsItBeingAsked(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)

	if w := h.post(t, "/ui/templates/"+id+"/add-question", map[string]string{
		"variable": "doomed", "label": "Doomed", "type": "text",
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("adding the question = %d: %s", w.Code, w.Body.String())
	}
	if w := h.post(t, "/ui/templates/"+id+"/remove-question/doomed", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("removing the question = %d: %s", w.Code, w.Body.String())
	}

	if strings.Contains(h.get(t, "/ui/templates/"+id+"/launch").Body.String(), `name="answer_doomed"`) {
		t.Error("the launch form still asks the removed question")
	}
}

// TestTemplatesSurvey_RefusesAQuestionNobodyCouldAnswer proves the store's
// own rules reach the form rather than a 500, and land on the control that
// broke them.
//
// A password carrying a default is the rule worth pinning, because it is
// the one with a security reason: a default password is a credential
// sitting in the template record, readable by anybody who may edit the
// template and copied into every duplicate of it.
func TestTemplatesSurvey_RefusesAQuestionNobodyCouldAnswer(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)

	cases := []struct {
		name string
		form map[string]string
		want string
	}{
		{
			name: "a password carrying a default",
			form: map[string]string{"variable": "vault_pw", "label": "Vault", "type": "password", "default": "hunter2"},
			want: "must not carry a default",
		},
		{
			name: "a choice question offering nothing",
			form: map[string]string{"variable": "pick", "label": "Pick", "type": "multiplechoice"},
			want: "choice between nothing",
		},
		{
			name: "a minimum above its maximum",
			form: map[string]string{"variable": "bounded", "label": "Bounded", "type": "integer", "min": "9", "max": "2"},
			want: "minimum above its maximum",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := h.post(t, "/ui/templates/"+id+"/add-question", tc.form)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("adding %s = %d, want 422: %s", tc.name, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("the refusal does not say %q, so the store's own words did not reach the form", tc.want)
			}
			// A refusal that left the question stored would be worse than
			// one that failed silently.
			if strings.Contains(h.get(t, "/ui/templates/"+id+"/launch").Body.String(), `name="answer_`+tc.form["variable"]+`"`) {
				t.Error("the refused question was stored anyway")
			}
		})
	}
}

// ownTemplateForSurvey creates a template this test alone writes a survey
// to, and returns its id.
func ownTemplateForSurvey(t *testing.T, h *harness) string {
	t.Helper()

	form := body(t, h, "/ui/templates/new")
	inventory := optionValue(t, form, "inventory", "production (conformance)")
	definition := optionValue(t, form, "definition", "conformance (Runbook)")

	name := uniqueName(t, "survey-template")
	if w := h.post(t, "/ui/templates", map[string]string{
		"name": name, "description": "owned by the survey builder tests",
		"definition": definition, "inventory": inventory,
	}); w.Code >= http.StatusBadRequest {
		t.Fatalf("creating the survey template = %d: %s", w.Code, w.Body.String())
	}

	for _, candidate := range recordIDs(t, h, "templates") {
		if strings.Contains(h.get(t, "/ui/templates/"+candidate).Body.String(), name) {
			return candidate
		}
	}
	t.Fatal("the template this test created is not reachable")
	return ""
}

// surveyOrder reads the variables out of the Survey section, in the order
// the table renders them.
//
// It reads the row-action hrefs rather than the cells, because a cell is
// display text that a future column could reorder or reformat, while the
// href carries the row id the control will post to. Reading the thing the
// browser would actually submit is what makes this an assertion about the
// stored order rather than about the stylesheet.
func surveyOrder(section string) []string {
	var out []string
	for _, part := range strings.Split(section, "edit-question/")[1:] {
		end := strings.IndexAny(part, `"'`)
		if end < 0 {
			continue
		}
		out = append(out, part[:end])
	}
	return out
}

// launchOrder reads the survey controls out of a launch form, in the order
// the form asks them.
func launchOrder(form string) []string {
	var out []string
	for _, part := range strings.Split(form, `name="answer_`)[1:] {
		if end := strings.IndexByte(part, '"'); end >= 0 {
			out = append(out, part[:end])
		}
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestTemplatesSurvey_SectionTabIsReachable guards the slug every one of
// these controls redirects to. A redirect to a tab that does not exist
// falls back to the record's own fields, which looks like a successful save
// of something that then is not shown.
func TestTemplatesSurvey_SectionTabIsReachable(t *testing.T) {
	h := newHarness(t, adminIdentity)
	if slug := view.TabSlug("Survey"); slug == "" {
		t.Fatal("the Survey section has no tab slug")
	}
	if !strings.Contains(h.section(t, "/ui/templates/1", "Survey"), "Add question") {
		t.Error("the Survey section does not offer Add question")
	}
}

// TestTemplatesSurvey_FileQuestionIsAuthoredAndRendered covers the file
// type through the real router, as far as this harness can take it.
//
// It proves the authoring and rendering half: a file question can be
// written through the survey builder, the launch form draws it with its
// bound and says what it refuses, and the program-content control is
// withheld on a deployment that would not honour it. The REFUSAL half is
// proved in internal/api against a real dispatcher instead, because this
// harness wires Deps.Dispatcher as nil on purpose (harness_test.go:142) and
// no launch can complete through it.
func TestTemplatesSurvey_FileQuestionIsAuthoredAndRendered(t *testing.T) {
	h := newHarness(t, adminIdentity)
	id := ownTemplateForSurvey(t, h)

	if w := h.post(t, "/ui/templates/"+id+"/add-question", map[string]string{
		"variable": "hostlist",
		"label":    "Host list",
		"type":     "file",
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("adding a file question = %d: %s", w.Code, w.Body.String())
	}

	// The deployment does not consent in this harness, which is the state
	// almost every deployment is in, so the authoring form must not draw a
	// control that would change nothing.
	editForm := h.get(t, "/ui/templates/"+id+"/edit-question/hostlist").Body.String()
	if strings.Contains(editForm, `name="allow_program_content"`) {
		t.Error("the authoring form offers the program-content control on a deployment that refuses program content")
	}

	section := h.section(t, "/ui/templates/"+id, "Survey")
	if !strings.Contains(section, "PROGRAM CONTENT") {
		t.Error("the Survey section has no program-content column, so an armed question is invisible to a reviewer")
	}

	launchForm := h.get(t, "/ui/templates/"+id+"/launch").Body.String()
	if !strings.Contains(launchForm, `name="answer_hostlist"`) {
		t.Fatal("the launch form does not ask the file question")
	}
	// A bound the browser can see. The textarea carried no maxlength
	// attribute at all until the file type declared one, so a 40 KB paste
	// was answered only by a 422 after the fact.
	if !strings.Contains(launchForm, `maxlength="32768"`) {
		t.Error("the file control renders no maxlength, so nothing signals the bound before the paste")
	}
	if !strings.Contains(launchForm, "A file opening with an interpreter line is refused.") {
		t.Error("the file control does not say what it refuses")
	}
	// The secrecy consequence, said where the operator is deciding whether
	// to paste a private key rather than in a document.
	if !strings.Contains(launchForm, "never replayed by a relaunch or a schedule") {
		t.Error("the file control does not say the answer is treated as secret")
	}
}
