package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// navDescriptors builds a set spanning three groups plus the ungrouped
// bucket, so ordering and omission can both be observed.
func navDescriptors() []view.Descriptor {
	return []view.Descriptor{
		{Name: "jobs", NavLabel: "JOBS", NavGroup: view.NavGroupViews},
		{Name: "devices", NavLabel: "DEVICES", NavGroup: view.NavGroupResources},
		{Name: "access", NavLabel: "ACCESS", NavGroup: view.NavGroupAccess},
		{Name: "dashboard", NavLabel: "DASHBOARD", NavGroup: view.NavGroupNone},
	}
}

// TestBuildNavSections_OrdersGroupsAndNotDescriptors proves the order comes
// from the vocabulary rather than from the descriptors.
//
// That matters because the registry is a map: if order came from whichever
// descriptor declared a group first, the sidebar would reshuffle between
// process starts, which defeats the muscle memory a navigation exists to
// build.
func TestBuildNavSections_OrdersGroupsAndNotDescriptors(t *testing.T) {
	sections := view.BuildNavSections("/ui", "", navDescriptors(), nil)

	labels := make([]string, 0, len(sections))
	for _, s := range sections {
		labels = append(labels, s.Label)
	}
	want := []string{"", "VIEWS", "RESOURCES", "ACCESS"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("groups rendered %v, want %v", labels, want)
	}

	// The ungrouped bucket renders first and carries no heading to label.
	if sections[0].Heading() {
		t.Error("the ungrouped bucket rendered a heading")
	}
	if sections[0].ID != "" {
		t.Errorf("the ungrouped bucket has id %q, which would label nothing", sections[0].ID)
	}
}

// TestBuildNavSections_DropsAGroupWithNoReachableItems is the accessibility
// rule. An empty labelled list tells a reader there is something under that
// heading and then shows them nothing, which reads as a broken page rather
// than as a permission boundary.
func TestBuildNavSections_DropsAGroupWithNoReachableItems(t *testing.T) {
	sections := view.BuildNavSections("/ui", "", navDescriptors(), func(d view.Descriptor) bool {
		return d.NavGroup != view.NavGroupAccess
	})

	for _, s := range sections {
		if s.Label == "ACCESS" {
			t.Fatal("a group whose every item was filtered out still rendered its heading")
		}
		if len(s.Items) == 0 {
			t.Errorf("group %q rendered with no items", s.Label)
		}
	}
}

func TestBuildNavSections_MarksTheCurrentViewAndBuildsHrefs(t *testing.T) {
	sections := view.BuildNavSections("/ui", "devices", navDescriptors(), nil)

	var current int
	for _, s := range sections {
		for _, item := range s.Items {
			if item.Current {
				current++
				if item.Label != "DEVICES" {
					t.Errorf("marked %q as current, want DEVICES", item.Label)
				}
			}
			if !strings.HasPrefix(item.Href, "/ui/") {
				t.Errorf("href %q does not sit under the mount prefix", item.Href)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d entries marked current, want exactly 1", current)
	}
}

// TestFirstHref_IsWhereTheIndexRedirects covers the index's own decision,
// including the case where a caller may reach nothing at all.
func TestFirstHref_IsWhereTheIndexRedirects(t *testing.T) {
	sections := view.BuildNavSections("/ui", "", navDescriptors(), nil)
	if got := view.FirstHref(sections); got != "/ui/dashboard" {
		t.Errorf("FirstHref() = %q, want the first reachable entry", got)
	}

	none := view.BuildNavSections("/ui", "", navDescriptors(), func(view.Descriptor) bool { return false })
	if got := view.FirstHref(none); got != "" {
		t.Errorf("FirstHref() = %q for a caller who may reach nothing, want empty", got)
	}
}

// TestNavGroup_IDIsSafeInAnAttribute matters because the value labels a list
// via aria-labelledby, so a broken reference is a silently unlabelled group.
func TestNavGroup_IDIsSafeInAnAttribute(t *testing.T) {
	for _, g := range []view.NavGroup{
		view.NavGroupViews, view.NavGroupResources,
		view.NavGroupAccess, view.NavGroupAdministration,
	} {
		id := g.ID()
		if !strings.HasPrefix(id, "nav-group-") {
			t.Errorf("NavGroup(%q).ID() = %q", g, id)
		}
		if strings.ContainsAny(id, ` "'<>&/`) {
			t.Errorf("NavGroup(%q).ID() = %q contains a character unsafe in an attribute", g, id)
		}
	}
	if view.NavGroupNone.ID() != "" {
		t.Error("the ungrouped bucket produced an id for a heading it does not render")
	}
}

// TestRegister_RefusesAnUndeclaredNavGroup is the closed-vocabulary guard.
// Free text would let two views spell one group two ways and render two
// headings that look like a bug in the sidebar.
func TestRegister_RefusesAnUndeclaredNavGroup(t *testing.T) {
	d := validDescriptor("navgroup-invalid")
	d.NavGroup = view.NavGroup("MISCELLANEOUS")

	err := view.Register(d)
	if err == nil {
		t.Fatal("an undeclared nav group was accepted")
	}
	if !strings.Contains(err.Error(), "nav group") {
		t.Errorf("err = %q, want it to name the nav group", err)
	}
}

// TestField_RendersBadgeIsIndependentOfTheFormControl is the decoupling the
// Access view needed: its effect column is chosen from a closed vocabulary,
// so the form wants a select, and it is the one column an auditor scans, so
// the table wants a badge.
func TestField_RendersBadgeIsIndependentOfTheFormControl(t *testing.T) {
	mapped := view.Field{
		Name: "effect", Kind: view.KindSelect, InForm: true,
		BadgeClass: func(string) string { return "badge-ok" },
	}
	if !mapped.RendersBadge() {
		t.Error("a select carrying a badge mapping does not render as a badge")
	}
	if !mapped.Writable() {
		t.Error("making it a badge took away its form control")
	}

	// KindBadge with no mapping still renders a badge, degrading to neutral,
	// so the declared views that rely on that are unaffected.
	if !(view.Field{Kind: view.KindBadge}).RendersBadge() {
		t.Error("KindBadge stopped rendering a badge")
	}
	if (view.Field{Kind: view.KindText}).RendersBadge() {
		t.Error("a plain text field rendered as a badge")
	}
}
