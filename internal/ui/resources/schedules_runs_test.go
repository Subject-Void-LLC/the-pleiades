// This file covers the RUNS picker on the Schedules form: what it offers, how
// it says which sort of thing each option is, and what it withholds.
//
// Three properties, and each one is a decision rather than a rendering detail.
// It offers more than templates now, which is the visible half of the whole
// launchable seam. It groups by type through a native <optgroup>, because a flat
// list mixing a job template and a project makes a reader guess from the name
// alone. And it offers only what the viewer could launch themselves, because a
// control that listed everything would be offering a choice that can only fail,
// at the latest possible moment and the furthest from the screen where somebody
// had the context to avoid it.
package resources_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// runsSelect returns the RUNS control's own HTML, so an assertion about what is
// offered cannot pass on something elsewhere in the page.
func runsSelect(t *testing.T, page string) string {
	t.Helper()
	sel := regexp.MustCompile(`(?s)<select[^>]*name="runs".*?</select>`)
	block := sel.FindString(page)
	if block == "" {
		t.Fatalf("the schedule form rendered no RUNS control:\n%s", page)
	}
	return block
}

// TestSchedulesForm_OffersEverySortOfLaunchableGroupedByWhatItIs is the visible
// half of the seam: a project sync is schedulable, beside a job template, on one
// control.
func TestSchedulesForm_OffersEverySortOfLaunchableGroupedByWhatItIs(t *testing.T) {
	h := newHarness(t, adminIdentity)
	control := runsSelect(t, body(t, h, "/ui/schedules/new"))

	// Grouped by the type's own label, through the native element for the job.
	for _, group := range []string{`<optgroup label="Job template">`, `<optgroup label="Project sync">`} {
		if !strings.Contains(control, group) {
			t.Errorf("the RUNS control has no %s, so a reader cannot tell the two sorts apart:\n%s", group, control)
		}
	}

	// And each group holds the thing the fixtures seeded.
	for _, name := range []string{"conformance-automation"} {
		if !strings.Contains(control, name) {
			t.Errorf("the RUNS control does not offer %q:\n%s", name, control)
		}
	}
}

// TestSchedulesForm_WithholdsWhatTheViewerCouldNotLaunch is the other half, and
// the one that is a security answer rather than a usability one: the picker is
// filtered by the same launchable.Reach the store checks on submit, so a person
// is never shown something that would be refused when they pressed Save.
//
// The identity here holds runbook:execute and not project:write, and is an
// operator rather than an administrator on purpose: the admin role bypasses
// scope checks, so an admin would see everything and the test would pass for a
// reason unrelated to the filter.
func TestSchedulesForm_WithholdsWhatTheViewerCouldNotLaunch(t *testing.T) {
	templatesOnly := &auth.Identity{
		Subject: "template-operator",
		Role:    auth.RoleOperator,
		Scopes: []auth.Scope{
			auth.ScopeScheduleRead, auth.ScopeScheduleWrite,
			auth.ScopeRunbookExecute, auth.ScopeTemplateRead, auth.ScopeJobRead,
		},
	}

	h := newHarness(t, templatesOnly)
	control := runsSelect(t, body(t, h, "/ui/schedules/new"))

	if !strings.Contains(control, `<optgroup label="Job template">`) {
		t.Errorf("a caller holding runbook:execute is offered no job template:\n%s", control)
	}
	if strings.Contains(control, `<optgroup label="Project sync">`) {
		t.Errorf("a caller without project:write is offered a project sync they could not launch:\n%s", control)
	}
	if strings.Contains(control, "conformance-automation") {
		t.Errorf("the project is offered to a caller who may not sync it:\n%s", control)
	}
}
