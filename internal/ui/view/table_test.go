// Package view_test's table and zero-state tests, written around the
// capabilities the copies they replaced did not share: reference links in a
// section, an accessible name on every link, and an empty region that says
// which emptiness it is.
package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The shared table and the shared zero state are what replaced three nearly
// identical blocks of template: a collection's rows, a record's related rows,
// and four different renderings of "there is nothing here". Nearly identical
// was the problem, so these tests are about the capabilities the copies did
// not share rather than about the markup they did.

func tableColumns() []view.Field {
	return []view.Field{
		{Name: "label", Label: "LABEL", Kind: view.KindText, InList: true, MobilePrimary: true},
		{Name: "owner", Label: "OWNER", Kind: view.KindText, InList: true, References: "teams"},
		{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true,
			BadgeClass: func(string) string { return "badge-ok" }},
		{Name: "loose", Label: "LOOSE", Kind: view.KindText, InList: true, References: "teams"},
	}
}

func TestTable_LinksTheRecordAndItsReferences(t *testing.T) {
	tbl := view.TableModel{
		Prefix:     "/ui",
		Columns:    tableColumns(),
		RecordView: "gadgets",
		Rows: []view.Row{{
			ID:    "g 1",
			Cells: view.Cells{"label": "First", "owner": "Platform", "state": "ok", "loose": "Orphan"},
			Refs:  map[string]string{"owner": "1"},
		}},
	}
	row := tbl.Rows[0]

	// The record's name is the way into it, which is what replaced a
	// trailing Open column somebody had to scroll sideways to reach.
	if got := tbl.CellHref(row, tableColumns()[0]); got != "/ui/gadgets/g%201" {
		t.Errorf("primary cell href = %q, want an escaped link to the record", got)
	}

	// A referencing cell reaches what it names. This is the capability the
	// section copy of this table never had: a job's device outcome could
	// name a device and not reach it.
	if got := tbl.RefHref(row, tableColumns()[1]); got != "/ui/teams/1" {
		t.Errorf("reference href = %q, want a link to the referenced record", got)
	}

	// A field that declares a reference but whose row carries no target id
	// is not a link, rather than a link to the collection.
	if got := tbl.CellHref(row, tableColumns()[3]); got != "" {
		t.Errorf("a reference with no target id produced %q", got)
	}

	// A badge is never a link: a chip that navigates is a target nobody
	// expects.
	if got := tbl.BadgeClass(row, tableColumns()[2]); got != "badge-ok" {
		t.Errorf("BadgeClass = %q", got)
	}
}

func TestTable_NeverRendersALinkWithNoAccessibleName(t *testing.T) {
	// A record whose primary cell is empty still has to be reachable, and
	// an anchor with no content is announced as its own URL. Both halves
	// matter, and getting one without the other is how a nameless link was
	// shipped the first time.
	tbl := view.TableModel{
		Prefix:     "/ui",
		Columns:    tableColumns(),
		RecordView: "gadgets",
		Rows: []view.Row{{
			ID:    "g-1",
			Cells: view.Cells{"label": "   ", "owner": "", "state": "ok"},
			Refs:  map[string]string{"owner": "1"},
		}},
	}
	row := tbl.Rows[0]
	primary := tableColumns()[0]

	if got := tbl.CellText(row, primary); got != "g-1" {
		t.Errorf("CellText for an empty primary cell = %q, want the identifier", got)
	}
	if got := tbl.CellHref(row, primary); got == "" {
		t.Error("a record with an empty primary cell became unreachable")
	}

	// A referencing cell with no text is the opposite case: there is
	// nothing to name the link, and the record is reachable elsewhere, so
	// no link is rendered at all.
	if got := tbl.CellHref(row, tableColumns()[1]); got != "" {
		t.Errorf("a reference cell with no text produced a link to %q", got)
	}

	// Every cell that is a link has text, which is the invariant the two
	// rules above exist to hold.
	for _, col := range tbl.Columns {
		if tbl.CellHref(row, col) != "" && strings.TrimSpace(tbl.CellText(row, col)) == "" {
			t.Errorf("column %q renders a link with no accessible name", col.Name)
		}
	}
}

func TestTable_ASectionsRowsLeadNowhereOnTheirOwn(t *testing.T) {
	// A section's rows are a projection of something else: a job's
	// per-device outcome is not a record of the Jobs view. It is the
	// referencing cells inside such a row that lead anywhere.
	s := view.SectionView{
		Prefix: "/ui",
		LoadedSection: view.LoadedSection{
			Spec: view.Section{Title: "Device outcomes", Status: view.StatusImplemented, Fields: tableColumns()},
			Rows: []view.Row{{
				ID:    "d-1",
				Cells: view.Cells{"label": "edge-fra-01", "owner": "Platform", "state": "ok"},
				Refs:  map[string]string{"owner": "1"},
			}},
		},
	}

	tbl := s.Table()
	if tbl.Linkable() {
		t.Error("a section's rows were treated as records of a view")
	}
	if got := tbl.RowHref(tbl.Rows[0]); got != "" {
		t.Errorf("RowHref on a section row = %q, want nothing", got)
	}
	if got := tbl.RefHref(tbl.Rows[0], tableColumns()[1]); got != "/ui/teams/1" {
		t.Errorf("a section's referencing cell = %q, want it to reach the record it names", got)
	}
	if got := s.Slug(); got != "device-outcomes" {
		t.Errorf("Slug = %q", got)
	}
}

func TestZeroState_NamesWhichEmptinessItIs(t *testing.T) {
	d := chromeDescriptor()
	d.Empty = "No gadget has been registered yet."
	page := chromePage()

	t.Run("an empty collection offers the way to start one", func(t *testing.T) {
		m := view.ListModel{Page: page, Descriptor: d, Aff: allAffordances()}
		z := m.ZeroState()
		if !strings.Contains(z.Body, "registered yet") {
			t.Errorf("Body = %q, want the descriptor's declared text", z.Body)
		}
		if !z.HasActions() || z.Actions[0].Label != "New gadget" {
			t.Errorf("Actions = %+v, want the create control", z.Actions)
		}
		if z.Class() != "zero" {
			t.Errorf("Class = %q, want the quiet tone", z.Class())
		}
	})

	t.Run("a page past the end offers the way back", func(t *testing.T) {
		// Opposite responses: offering "create the first one" to somebody
		// who has paged off the end is wrong, and offering "back to the
		// start" to somebody looking at an empty install goes nowhere.
		m := view.ListModel{Page: page, Descriptor: d, Cursor: "g-400", Aff: allAffordances()}
		z := m.ZeroState()
		if !m.Paged() {
			t.Error("a model with a cursor did not report itself as paged")
		}
		if !z.HasActions() || !strings.Contains(z.Actions[0].Href, "/ui/gadgets") {
			t.Errorf("Actions = %+v, want a way back to the first page", z.Actions)
		}
		if strings.Contains(z.Actions[0].Label, "New") {
			t.Error("somebody past the end of a list was offered the create control")
		}
	})

	t.Run("a view with no declared text still says something", func(t *testing.T) {
		bare := chromeDescriptor()
		bare.Empty = ""
		if got := bare.EmptyText(); got == "" || got == "No records." {
			t.Errorf("EmptyText = %q, want a sentence naming the view", got)
		}
	})

	t.Run("a declared section reads as deliberate", func(t *testing.T) {
		s := view.SectionView{LoadedSection: view.LoadedSection{
			Spec: view.Section{Title: "Notifications", Status: view.StatusDeclared, Empty: "No port for this."},
		}}
		z := s.ZeroState()
		if z.Tone != view.ZoneDeclared || z.Class() != "zero zero-declared" {
			t.Errorf("a declared section rendered as %q", z.Class())
		}
		if z.HasActions() {
			t.Error("a declared section offered an action for something that does not exist")
		}
	})

	t.Run("an implemented but empty section is not declared", func(t *testing.T) {
		s := view.SectionView{LoadedSection: view.LoadedSection{
			Spec: view.Section{Title: "Device outcomes", Status: view.StatusImplemented, Empty: "Nothing reported yet."},
		}}
		if got := s.ZeroState().Class(); got != "zero" {
			t.Errorf("an empty implemented section rendered as %q", got)
		}
	})

	t.Run("unevaluated authorization is a problem, not an absence", func(t *testing.T) {
		z := view.AffordanceUnknown()
		if z.Tone != view.ZoneProblem || z.Class() != "zero zero-problem" {
			t.Errorf("Class = %q, want the problem tone", z.Class())
		}
		if z.Body == "" {
			t.Error("the alert explains nothing, which is the failure it exists to avoid")
		}
	})

	t.Run("a declared view says what is missing", func(t *testing.T) {
		z := view.DeclaredModel{Page: page, Descriptor: d, Reason: "No backing API."}.ZeroState()
		if z.Tone != view.ZoneDeclared || z.Body != "No backing API." {
			t.Errorf("ZeroState = %+v, want the handler's own reason", z)
		}
	})
}

func TestDetail_ValuesReachWhatTheyName(t *testing.T) {
	// The detail list was the one place in the application where a
	// reference rendered as plain text, so a walk down the hierarchy
	// stopped at whichever record you opened first.
	m := view.DetailModel{
		Page:       chromePage(),
		Descriptor: chromeDescriptor(),
		Row:        chromeRow(),
	}

	fields := chromeDescriptor().Fields
	if got := m.RefHref(fields[3]); got != "/ui/teams/1" {
		t.Errorf("RefHref for a referencing field = %q, want a link to the record it names", got)
	}
	if got := m.RefHref(fields[0]); got != "" {
		t.Errorf("a non-referencing field produced a link to %q", got)
	}
	if got := m.BadgeClass(fields[2]); got != "badge-failed" {
		t.Errorf("BadgeClass = %q", got)
	}

	// A reference whose cell has no text is not a link, for the same
	// reason it is not one in a table.
	blank := m
	blank.Row.Cells = view.Cells{"owner": "  "}
	if got := blank.RefHref(fields[3]); got != "" {
		t.Errorf("a reference with no text produced %q", got)
	}
}

func TestDescriptor_TitleAndStatusFields(t *testing.T) {
	d := chromeDescriptor()
	if got := d.TitleField(); got != "label" {
		t.Errorf("TitleField = %q, want the declared name field", got)
	}

	// Most views are addressed by their name, so they declare nothing and
	// fall back to the identifier.
	d.NameField = ""
	if got := d.TitleField(); got != "gadget_id" {
		t.Errorf("TitleField without a declaration = %q, want the identifier", got)
	}

	f, ok := d.StatusField()
	if !ok || f.Name != "state" {
		t.Errorf("StatusField = %+v, %v; want the declared status field", f, ok)
	}

	// A badge field that is not listed is not a status: it is not something
	// the view chose to show at a glance.
	d.Fields = []view.Field{{Name: "state", Kind: view.KindBadge}}
	if _, ok := d.StatusField(); ok {
		t.Error("an unlisted badge field was taken as the record's status")
	}

	// And a view that declares none gets none. This replaced taking the
	// first listed badge field, which was wrong nearly everywhere it was
	// applied: a job's first badge is its KIND, so a failed job read
	// "4821 runbook", and a runbook's only badge is INTERRUPTIBLE, so every
	// runbook record was headed with the word "YES". A badge beside a title
	// claims the value is what the record IS, and most badge fields are
	// properties rather than states.
	undeclared := chromeDescriptor()
	undeclared.StatusBadgeField = ""
	if _, ok := undeclared.StatusField(); ok {
		t.Error("a view declaring no status field was given one anyway")
	}
	if len(view.DetailModel{Page: chromePage(), Descriptor: undeclared, Row: chromeRow()}.Chrome().Badges) != 0 {
		t.Error("a record with no declared status rendered a badge beside its title")
	}
}
