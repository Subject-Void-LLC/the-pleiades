// Package view_test's chrome tests: breadcrumbs, titles, status badges, record
// actions and tabs.
//
// internal/ui/render is excluded from the coverage ratchet on the
// understanding that the decisions live here, so they are tested here.
package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Chrome is the header every page renders, and it is where the decisions a
// template is not allowed to make now live: what a page is called, where the
// reader is, what state the record is in, what they may do to it, and which
// of its parts they are looking at. internal/ui/render is excluded from the
// coverage ratchet on the understanding that those decisions are all here, so
// they are all tested here.

// chromeDescriptor is a record-shaped view with a name distinct from its
// identifier, a status field, sections and a stream: the four things the
// header is computed from.
func chromeDescriptor() view.Descriptor {
	return view.Descriptor{
		Name:             "gadgets",
		Title:            "Gadgets",
		NavLabel:         "GADGETS",
		NavGroup:         view.NavGroupResources,
		Summary:          "Everything this platform acts on.",
		Status:           view.StatusImplemented,
		IDField:          "gadget_id",
		NameField:        "label",
		StatusBadgeField: "state",
		Fields: []view.Field{
			{Name: "gadget_id", Label: "ID", Kind: view.KindText, InList: true},
			{Name: "label", Label: "LABEL", Kind: view.KindText, InList: true, MobilePrimary: true},
			{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true,
				BadgeClass: func(string) string { return "badge-failed" }},
			{Name: "owner", Label: "OWNER", Kind: view.KindText, InList: true, References: "teams"},
		},
		Sections: []view.Section{
			{Title: "Related records", Status: view.StatusImplemented, Empty: "None yet."},
			{Title: "Not built", Status: view.StatusDeclared, Empty: "No port for this."},
		},
		Stream: &view.StreamSpec{Title: "Live output"},
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
	}
}

func chromePage() view.PageModel {
	return view.PageModel{Prefix: "/ui", Subject: "operator@example.test"}
}

func chromeRow() view.Row {
	return view.Row{
		ID: "g-1",
		Cells: view.Cells{
			"gadget_id": "g-1",
			"label":     "The first gadget",
			"state":     "failed",
			"owner":     "Platform Engineering",
		},
		Refs: map[string]string{"owner": "1"},
	}
}

// allAffordances permits everything, so a test about rendering is not
// silently a test about authorization.
func allAffordances() view.Affordances {
	return view.NewAffordances([]auth.LinkRel{
		apispec.CreateDevice.Rel,
		apispec.UpdateDevice.Rel,
		apispec.DeleteDevice.Rel,
	})
}

func TestTabSlug(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Device outcomes", "device-outcomes"},
		{"Completed jobs", "completed-jobs"},
		{"Survey", "survey"},
		{"UPPER Case", "upper-case"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"a/b", "a-b"},
		// A hostile title cannot emit anything outside the slug alphabet,
		// which is what makes interpolating the result into a URL safe.
		{`"><script>`, "script"},
		{"", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got := view.TabSlug(tc.in)
			if got != tc.want {
				t.Errorf("TabSlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for _, r := range got {
				valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
				if !valid {
					t.Errorf("TabSlug(%q) = %q, which contains %q", tc.in, got, r)
				}
			}
		})
	}
}

func TestChrome_ListHeader(t *testing.T) {
	m := view.ListModel{
		Page:       chromePage(),
		Descriptor: chromeDescriptor(),
		Rows:       []view.Row{chromeRow()},
		Aff:        allAffordances(),
	}

	c := m.Chrome()
	if c.Title != "Gadgets" {
		t.Errorf("Title = %q, want the view's own title", c.Title)
	}
	if c.HasTabs() {
		t.Error("a collection rendered a tab strip")
	}
	if c.HeaderClass() != "view-header view-header-plain" {
		t.Errorf("HeaderClass = %q, want the tabless variant", c.HeaderClass())
	}

	// The trail locates the reader in the navigation, not only in the URL,
	// which is why the group is the first step even though it is not a page.
	if len(c.Crumbs) != 2 {
		t.Fatalf("Crumbs = %v, want the group and the view", c.Crumbs)
	}
	if c.Crumbs[0].Label != "Resources" || c.Crumbs[0].Linked() {
		t.Errorf("first crumb = %+v, want an unlinked group heading", c.Crumbs[0])
	}
	if !c.Crumbs[1].Current || c.Crumbs[1].Linked() {
		t.Errorf("last crumb = %+v, want the current page, unlinked", c.Crumbs[1])
	}
	if c.Crumbs[1].CurrentAttr() != "page" || c.Crumbs[0].CurrentAttr() != "false" {
		t.Error("aria-current is not set on exactly the last crumb")
	}

	// The create control says what it creates, in the singular.
	if len(c.Actions) != 1 || c.Actions[0].Label != "New gadget" {
		t.Errorf("Actions = %+v, want one create action naming the singular", c.Actions)
	}
	if c.Actions[0].Class() != "btn btn-primary" {
		t.Errorf("create action class = %q, want the primary button", c.Actions[0].Class())
	}
	if c.Actions[0].OpensDialog() {
		t.Error("the create action opens a dialog rather than navigating")
	}
}

func TestChrome_ListOffersNoCreateWithoutThePermission(t *testing.T) {
	m := view.ListModel{
		Page:       chromePage(),
		Descriptor: chromeDescriptor(),
		Aff:        view.NewAffordances(nil),
	}
	if len(m.Chrome().Actions) != 0 {
		t.Error("a caller who may not create was offered a create control")
	}
}

func TestChrome_RecordHeader(t *testing.T) {
	m := view.DetailModel{
		Page:       chromePage(),
		Descriptor: chromeDescriptor(),
		Row:        chromeRow(),
		Aff:        allAffordances(),
	}

	c := m.Chrome()

	// The title is what the record is called. A page headed by a primary
	// key has moved the join into the reader's head.
	if c.Title != "The first gadget" {
		t.Errorf("Title = %q, want the record's name", c.Title)
	}
	// And the identifier stays visible for somebody who needs it.
	if !strings.Contains(c.Summary, "g-1") {
		t.Errorf("Summary = %q, want it to carry the identifier", c.Summary)
	}

	if len(c.Crumbs) != 3 {
		t.Fatalf("Crumbs = %+v, want group, collection, record", c.Crumbs)
	}
	if c.Crumbs[1].Href != "/ui/gadgets" {
		t.Errorf("the collection crumb = %q, want a link back to the list", c.Crumbs[1].Href)
	}
	if c.Crumbs[2].Label != "The first gadget" || !c.Crumbs[2].Current {
		t.Errorf("the record crumb = %+v, want the record's name, marked current", c.Crumbs[2])
	}

	// The state is a badge beside the title, resolved through the one
	// validated implementation.
	if len(c.Badges) != 1 || c.Badges[0].Label != "failed" || c.Badges[0].Class != "badge-failed" {
		t.Errorf("Badges = %+v, want the record's state", c.Badges)
	}

	// Edit navigates; delete opens the confirmation dialog rather than
	// being a link something can prefetch.
	var deleted bool
	for _, a := range c.Actions {
		if a.Kind == view.ActionDanger {
			deleted = true
			if !a.OpensDialog() || a.Dialog != "confirm-delete" {
				t.Errorf("the delete action = %+v, want it to open the confirmation dialog", a)
			}
			if a.Class() != "btn btn-danger" {
				t.Errorf("delete class = %q", a.Class())
			}
		}
	}
	if !deleted {
		t.Error("a caller permitted to delete was offered no delete control")
	}
}

func TestChrome_RecordFallsBackToItsIdentifier(t *testing.T) {
	d := chromeDescriptor()
	row := chromeRow()
	row.Cells["label"] = "   "

	m := view.DetailModel{Page: chromePage(), Descriptor: d, Row: row}
	if got := m.RecordName(); got != "g-1" {
		t.Errorf("RecordName with a blank name = %q, want the identifier", got)
	}
	// A page with no heading is worse than one headed by a key, and the
	// summary must then not repeat the identifier it is already showing.
	if got := m.RecordSummary(); got != d.Summary {
		t.Errorf("RecordSummary = %q, want the descriptor's own summary", got)
	}
}

func TestChrome_NoStatusFieldMeansNoBadge(t *testing.T) {
	d := chromeDescriptor()
	d.Fields = []view.Field{{Name: "gadget_id", Label: "ID", Kind: view.KindText, InList: true}}
	d.NameField = ""

	m := view.DetailModel{Page: chromePage(), Descriptor: d, Row: chromeRow()}
	if _, ok := m.StatusBadge(); ok {
		t.Error("a view declaring no badge field was given a status badge anyway")
	}
	if len(m.Chrome().Badges) != 0 {
		t.Error("the header rendered a badge for a view with no status field")
	}
}

func TestTabs_AreComputedFromTheDeclaration(t *testing.T) {
	m := view.DetailModel{
		Page:       chromePage(),
		Descriptor: chromeDescriptor(),
		Row:        chromeRow(),
		Sections: []view.LoadedSection{
			{Spec: view.Section{Title: "Related records", Status: view.StatusImplemented},
				Rows: []view.Row{{ID: "a"}, {ID: "b"}}},
			{Spec: view.Section{Title: "Not built", Status: view.StatusDeclared}},
		},
	}

	tabs := m.Tabs()
	if len(tabs) != 4 {
		t.Fatalf("got %d tabs, want details, two sections and the stream: %+v", len(tabs), tabs)
	}

	if tabs[0].Label != "Details" || !tabs[0].Current {
		t.Errorf("first tab = %+v, want Details, current by default", tabs[0])
	}
	if tabs[0].HasCount() {
		t.Error("the record's own fields were given a row count")
	}
	if tabs[0].Class() != "tab tab-current" || tabs[0].CurrentAttr() != "page" {
		t.Error("the current tab is not marked as such")
	}

	if tabs[1].Count != "2" {
		t.Errorf("an implemented section's count = %q, want the number of rows", tabs[1].Count)
	}
	if tabs[1].Href != "/ui/gadgets/g-1?tab=related-records" {
		t.Errorf("section tab href = %q", tabs[1].Href)
	}
	// A declared section has no rows to count, and a zero beside it would
	// be a claim about live data it has no port to make.
	if tabs[2].HasCount() {
		t.Errorf("a declared section was given a count of %q", tabs[2].Count)
	}
	if tabs[3].Label != "Live output" || tabs[3].Slug != "output" {
		t.Errorf("last tab = %+v, want the declared stream", tabs[3])
	}
}

func TestTabs_SelectionAndFallback(t *testing.T) {
	sections := []view.LoadedSection{
		{Spec: view.Section{Title: "Related records", Status: view.StatusImplemented}, Rows: []view.Row{{ID: "a"}}},
	}

	for _, tc := range []struct {
		name, tab, want string
		details, stream bool
		visible         int
	}{
		{name: "unset opens the record", tab: "", want: "details", details: true},
		{name: "a section", tab: "related-records", want: "related-records", visible: 1},
		{name: "the stream", tab: "output", want: "output", stream: true},
		// The value arrives from a query string, so it arrives from
		// whatever somebody pasted into an address bar.
		{name: "unknown falls back", tab: "no-such-thing", want: "details", details: true},
		{name: "a hostile value falls back", tab: `"><script>`, want: "details", details: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := view.DetailModel{
				Page:       chromePage(),
				Descriptor: chromeDescriptor(),
				Row:        chromeRow(),
				Sections:   sections,
				Tab:        tc.tab,
			}
			if got := m.CurrentTab(); got != tc.want {
				t.Errorf("CurrentTab = %q, want %q", got, tc.want)
			}
			if m.ShowDetails() != tc.details {
				t.Errorf("ShowDetails = %v, want %v", m.ShowDetails(), tc.details)
			}
			if m.ShowStream() != tc.stream {
				t.Errorf("ShowStream = %v, want %v", m.ShowStream(), tc.stream)
			}
			if got := len(m.SectionViews()); got != tc.visible {
				t.Errorf("SectionViews = %d, want %d", got, tc.visible)
			}
		})
	}
}

func TestTabs_TheStreamTabIsNotOfferedWithoutOne(t *testing.T) {
	d := chromeDescriptor()
	d.Stream = nil

	m := view.DetailModel{Page: chromePage(), Descriptor: d, Row: chromeRow(), Tab: "output"}
	for _, tab := range m.Tabs() {
		if tab.Slug == "output" {
			t.Error("a view with no stream offered an output tab")
		}
	}
	// And asking for it by URL falls back rather than rendering an empty
	// region wired to nothing.
	if !m.ShowDetails() {
		t.Error("asking for a stream this view does not have did not fall back to the record")
	}
	if m.StreamURL() != "" {
		t.Errorf("StreamURL = %q on a view with no stream", m.StreamURL())
	}
}

func TestChrome_FormAndActionAndStreamAndDeclaredHeaders(t *testing.T) {
	d := chromeDescriptor()
	page := chromePage()

	t.Run("a create form", func(t *testing.T) {
		c := view.FormModel{Page: page, Descriptor: d}.Chrome()
		last := c.Crumbs[len(c.Crumbs)-1]
		if last.Label != "New" || !last.Current {
			t.Errorf("last crumb = %+v, want New", last)
		}
		if len(c.Crumbs) != 3 {
			t.Errorf("Crumbs = %+v, want group, collection, New", c.Crumbs)
		}
	})

	t.Run("an edit form walks through its record", func(t *testing.T) {
		m := view.FormModel{
			Page: page, Descriptor: d, ID: "g-1",
			Values: map[string]string{"label": "The first gadget"},
		}
		c := m.Chrome()
		if len(c.Crumbs) != 4 {
			t.Fatalf("Crumbs = %+v, want group, collection, record, Edit", c.Crumbs)
		}
		if c.Crumbs[2].Label != "The first gadget" || c.Crumbs[2].Href != "/ui/gadgets/g-1" {
			t.Errorf("record crumb = %+v, want a named link back to the record", c.Crumbs[2])
		}
	})

	t.Run("an action prompt", func(t *testing.T) {
		m := view.ActionModel{
			Page: page, Descriptor: d, ID: "g-1",
			Action: view.RecordAction{Name: "launch", Label: "Launch", Heading: "Launch"},
		}
		c := m.Chrome()
		if c.Crumbs[len(c.Crumbs)-1].Label != "Launch" {
			t.Errorf("Crumbs = %+v, want the action as the last step", c.Crumbs)
		}
	})

	t.Run("the standalone stream page walks back to its record", func(t *testing.T) {
		// The page exists for a link made before the record had a tab for
		// it, and its whole problem was leaving a reader with no way to
		// tell which record they were watching.
		c := view.StreamModel{Page: page, Descriptor: d, ID: "g-1"}.Chrome()
		if len(c.Crumbs) != 4 {
			t.Fatalf("Crumbs = %+v, want a trail back to the record", c.Crumbs)
		}
		if c.Crumbs[2].Href != "/ui/gadgets/g-1" {
			t.Errorf("record crumb = %+v, want a link back to the record", c.Crumbs[2])
		}
	})

	t.Run("a declared view says so in its header", func(t *testing.T) {
		c := view.DeclaredModel{Page: page, Descriptor: d, Reason: "No backing API."}.Chrome()
		if len(c.Badges) != 1 || c.Badges[0].Label != "Declared" {
			t.Errorf("Badges = %+v, want the declared marker", c.Badges)
		}
	})
}
