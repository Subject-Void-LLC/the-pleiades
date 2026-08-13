package resources_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file asserts one property over every writable view: the edit form's
// own fields are an acceptable submission.
//
// It exists because that was false on three views at once and CI was green.
// view.Field.Immutable removes a field from the edit form, and each of
// those views' Bind functions still parsed it unconditionally, so every
// inventory, team and template edit answered 422 with an error naming a
// control the page had not rendered. Nothing caught it: the conformance
// suite posted create forms and refusals, never a successful edit, and
// each view's own reasoning looked right in isolation.
//
// The assertion is deliberately mechanical rather than per-view. It reads
// the controls the server actually rendered, submits exactly those, and
// requires the write to be accepted. A view whose edit form and Bind
// disagree cannot pass it, whatever the disagreement is.

// controlName matches the name attribute of a rendered form control.
var controlName = regexp.MustCompile(`<(?:input|select|textarea)[^>]*\sname="([^"]+)"`)

// selectedOption matches an option marked selected, so a resubmission
// carries the values the record already has rather than a guess. Every
// match is taken, not the first: a multi-select's selection is the whole
// set, and submitting one of them is a deletion of the rest.
var selectedOption = regexp.MustCompile(`<option[^>]*\svalue="([^"]*)"[^>]*\sselected`)

// renderedControls returns the writable control names an edit form drew,
// excluding the machinery's own reserved keys.
func renderedControls(body string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range controlName.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if name == "_csrf" || name == "_method" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func TestViewConformance_AnEditFormsOwnFieldsAreAnAcceptableSubmission(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, d := range view.All() {
		if d.Handlers == nil || d.Handlers.Update == nil || d.Handlers.Form == nil {
			continue
		}
		t.Run(d.Name, func(t *testing.T) {
			// The first record, reached the way a user does: list, open,
			// edit.
			id := firstRecordID(t, h, d.Name)
			if id == "" {
				t.Skip("no seeded record to edit")
			}

			form := h.get(t, "/ui/"+d.Name+"/"+id+"/edit")
			if form.Code != http.StatusOK {
				t.Fatalf("GET the edit form = %d, want 200", form.Code)
			}
			body := form.Body.String()

			// Resubmit exactly what was rendered, prefilled as rendered,
			// including every value of a multi-select. Values come from the
			// form's own markup rather than from the projector, so this is
			// what a user pressing Save with nothing changed sends.
			//
			// A faithful round-trip is also what keeps this assertion
			// non-destructive: it writes the record's own current values
			// back over themselves, so the seeded fixtures every other test
			// in this package reads stay exactly as seeded. An earlier
			// version collapsed multi-selects to their first value and
			// silently emptied a template's promptable fields, which broke
			// a launch-form assertion three files away.
			submission := url.Values{}
			for _, name := range renderedControls(body) {
				for _, value := range renderedValues(body, name) {
					submission.Add(name, value)
				}
			}
			if len(submission) == 0 {
				t.Skip("the edit form renders no controls")
			}

			w := h.postValues(t, "/ui/"+d.Name+"/"+id, submission)
			if w.Code != http.StatusSeeOther && w.Code != http.StatusNoContent {
				t.Errorf("resubmitting the edit form's own fields = %d, want a redirect.\nSubmitted: %v\nErrors: %s",
					w.Code, submission, formErrors(w.Body.String()))
			}
		})
	}
}

// renderedValues reads back what a control was prefilled with: an input's
// value attribute, a textarea's content, or every selected option of a
// select. A control with nothing prefilled contributes one empty value,
// which is what a browser submits for it.
func renderedValues(body, name string) []string {
	quoted := regexp.QuoteMeta(name)

	if tag := regexp.MustCompile(`<input[^>]*\sname="` + quoted + `"[^>]*>`).FindString(body); tag != "" {
		if strings.Contains(tag, `type="checkbox"`) {
			if strings.Contains(tag, "checked") {
				return []string{"true"}
			}
			// An unchecked checkbox submits nothing at all, which is how
			// the machinery reads false; sending "" would be a value.
			return nil
		}
		if v := regexp.MustCompile(`\svalue="([^"]*)"`).FindStringSubmatch(tag); v != nil {
			return []string{v[1]}
		}
		return []string{""}
	}

	if block := regexp.MustCompile(`(?s)<select[^>]*\sname="` + quoted + `".*?</select>`).FindString(body); block != "" {
		var chosen []string
		for _, m := range selectedOption.FindAllStringSubmatch(block, -1) {
			chosen = append(chosen, m[1])
		}
		if len(chosen) > 0 {
			return chosen
		}
		// Nothing marked: a single select submits its first option, and a
		// multi-select with no selection submits nothing.
		if strings.Contains(block, "multiple") {
			return nil
		}
		if m := regexp.MustCompile(`<option[^>]*\svalue="([^"]*)"`).FindStringSubmatch(block); m != nil {
			return []string{m[1]}
		}
		return nil
	}

	if block := regexp.MustCompile(`(?s)<textarea[^>]*\sname="` + quoted + `"[^>]*>(.*?)</textarea>`).FindStringSubmatch(body); block != nil {
		return []string{html.UnescapeString(block[1])}
	}
	return []string{""}
}

// formErrors extracts the rendered error summary, so a failure says what
// the page complained about rather than dumping a document.
func formErrors(body string) string {
	re := regexp.MustCompile(`(?s)<ul class="error-summary-list">(.*?)</ul>`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "(no error summary rendered)"
	}
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[1], " ")
	return strings.Join(strings.Fields(text), " ")
}
