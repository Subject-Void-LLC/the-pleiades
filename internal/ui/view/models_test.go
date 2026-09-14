// Package view_test's page-model coverage: the accessors the templates
// call.
//
// These are pure functions over already-loaded data, and that is exactly
// why they are worth testing rather than assumed. A templ template is
// compiled Go, so a wrong answer here does not fail to build; it renders a
// link to the wrong place, a maxlength attribute that silently truncates,
// or an aria-describedby pointing at an element that is not on the page.
// None of those show up in a handler test, because the handler was right.
package view_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// modelFields is a small field set covering the kinds these accessors
// branch on.
var modelFields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, InForm: true, MobilePrimary: true, MaxLen: 64, Help: "What it is called."},
	{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true},
	{Name: "notes", Label: "NOTES", Kind: view.KindLongText, InForm: true},
	// Immutable is what makes an edit's declared set differ from a
	// create's, which is the distinction Values.Editing exists to carry.
	{Name: "organization", Label: "ORGANIZATION", Kind: view.KindSelect, InForm: true, Immutable: true},
}

// TestTableModel_RendersItsShape covers the accessors the shared table
// component asks before it draws anything.
func TestTableModel_RendersItsShape(t *testing.T) {
	rows := []view.Row{{ID: "1", Cells: view.Cells{"name": "web", "state": "ok"}}}

	full := view.TableModel{Columns: modelFields[:2], Rows: rows, Caption: "Devices"}
	if !full.ShowsColumns() || !full.HasRows() || !full.Captioned() {
		t.Errorf("a populated table hid its own header: %+v", full)
	}
	if got := full.ColumnSpan(); got != "2" {
		t.Errorf("ColumnSpan() = %q, want 2", got)
	}
	if got := full.Cell(rows[0], modelFields[0]); got != "web" {
		t.Errorf("Cell() = %q, want web", got)
	}
	// A string, because it is rendered straight into an attribute value.
	if got := full.IsPrimary(modelFields[0]); got != "true" {
		t.Errorf("IsPrimary(name) = %q, want true", got)
	}
	if got := full.IsPrimary(modelFields[1]); got != "false" {
		t.Errorf("IsPrimary(state) = %q, want false", got)
	}

	// An empty table draws no header and no caption, so the zero state is
	// the only thing a reader sees.
	empty := view.TableModel{Columns: modelFields[:2]}
	if empty.ShowsColumns() || empty.HasRows() || empty.Captioned() {
		t.Errorf("an empty table still drew its chrome: %+v", empty)
	}

	// A preview is the exception: no rows, but the columns render, which is
	// what turns "not built" into "this is what it is going to be".
	preview := view.TableModel{Columns: modelFields[:2], Preview: true}
	if !preview.ShowsColumns() {
		t.Error("a preview table hid the columns it exists to show")
	}
	if preview.HasRows() {
		t.Error("a preview table claims to have rows")
	}

	// No columns means no header even in a preview, because a header row
	// of nothing is a rule with no cells under it.
	if (view.TableModel{Preview: true}).ShowsColumns() {
		t.Error("a table with no columns drew a header row")
	}
}

// TestZeroState_HasActions covers the branch that decides whether the empty
// panel offers a way out of itself.
func TestZeroState_HasActions(t *testing.T) {
	if (view.ZeroState{}).HasActions() {
		t.Error("a zero state with no actions claims to have some")
	}
	z := view.ZeroState{Actions: []view.ChromeAction{{Label: "New device"}}}
	if !z.HasActions() {
		t.Error("a zero state with an action hid it")
	}
}

// TestValues_ReportsWhatItWasNarrowedAgainst covers the two accessors a
// binder uses when its own field set is not knowable from its package.
//
// Credentials is that case: its controls come from a credential type's
// input schema. Ranging over the declared set is what keeps the narrowing
// guarantee intact, so a Fields that returned the raw submission instead
// would quietly undo the one property this type exists for.
func TestValues_ReportsWhatItWasNarrowedAgainst(t *testing.T) {
	form := url.Values{
		"name":       {"web"},
		"notes":      {"a note"},
		"undeclared": {"should not survive"},
	}

	values, undeclared := view.NewValues(modelFields, form, true)
	if len(undeclared) != 1 || undeclared[0] != "undeclared" {
		t.Fatalf("undeclared = %v, want exactly the one field nothing declared", undeclared)
	}
	if !values.Editing() {
		t.Error("Editing() = false for a submission built as an edit")
	}

	// An edit declares the writable fields MINUS the immutable one, which
	// is absent from an edit form by design. A binder that walked a static
	// list instead would reject every edit, which is the defect this
	// accessor was added to fix.
	editable := fieldNameSet(values.Fields())
	if len(editable) != 2 || !editable["name"] || !editable["notes"] {
		t.Fatalf("Fields() on an edit = %v, want name and notes only", editable)
	}
	if editable["undeclared"] {
		t.Fatal("Fields() leaked a field the form never declared")
	}
	if editable["state"] {
		t.Fatal("Fields() offered a badge, which no form can submit")
	}

	// A create is the other half: the immutable field is writable exactly
	// once, and this is that once.
	created, _ := view.NewValues(modelFields, form, false)
	if created.Editing() {
		t.Error("Editing() = true for a create submission")
	}
	if creatable := fieldNameSet(created.Fields()); !creatable["organization"] {
		t.Errorf("Fields() on a create = %v, want the immutable field included", creatable)
	}
}

// fieldNameSet indexes a field slice for membership assertions.
func fieldNameSet(fields []view.Field) map[string]bool {
	out := make(map[string]bool, len(fields))
	for _, f := range fields {
		out[f.Name] = true
	}
	return out
}

// detailModel builds a record page over the given descriptor.
func detailModel(t *testing.T, d view.Descriptor, row view.Row) view.DetailModel {
	t.Helper()
	return view.DetailModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: d,
		Row:        row,
	}
}

// TestDetailModel_Links covers the hrefs a record page renders, including
// the escaping an id that is not a number needs.
func TestDetailModel_Links(t *testing.T) {
	d := view.Descriptor{Name: "devices", Title: "Devices", Fields: modelFields}
	row := view.Row{ID: "core/1", Cells: view.Cells{"name": "web", "state": "ok"}}
	m := detailModel(t, d, row)

	if got := len(m.Fields()); got != len(modelFields) {
		t.Errorf("Fields() = %d, want %d", got, len(modelFields))
	}
	if got := m.Value(modelFields[0]); got != "web" {
		t.Errorf("Value(name) = %q, want web", got)
	}
	if got := m.ListHref(); got != "/ui/devices" {
		t.Errorf("ListHref() = %q", got)
	}
	// The id carries a slash, which must not open a path segment of its
	// own: /ui/devices/core%2F1/edit, never /ui/devices/core/1/edit.
	if got := m.EditHref(); got != "/ui/devices/core%2F1/edit" {
		t.Errorf("EditHref() = %q, want the id escaped into one segment", got)
	}
	if got := m.SelfHref(); got != "/ui/devices/core%2F1" {
		t.Errorf("SelfHref() = %q", got)
	}
}

// TestStreamModel_BackHref covers the way out of a standalone log page.
func TestStreamModel_BackHref(t *testing.T) {
	m := view.StreamModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: view.Descriptor{Name: "jobs"},
		ID:         "a b",
	}
	if got := m.BackHref(); got != "/ui/jobs/a%20b" {
		t.Errorf("BackHref() = %q, want the id escaped", got)
	}
}

// formModel builds a form page carrying the given values and errors.
func formModel(values map[string]string, errs view.FieldErrors) view.FormModel {
	return view.FormModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: view.Descriptor{Name: "devices", Title: "Devices", Fields: modelFields},
		Values:     values,
		Errors:     errs,
		Options:    map[string][]view.Option{"state": {{Value: "ok", Label: "OK"}}},
	}
}

// TestFormModel_ControlWiring covers the accessors that decide what a
// control announces to a screen reader.
//
// DescribedBy is the one with real consequence: an aria-describedby
// pointing at an element that is not on the page is worse than no
// attribute, because the dangling reference is announced.
func TestFormModel_ControlWiring(t *testing.T) {
	withHelp := modelFields[0]
	noHelp := modelFields[2]

	clean := formModel(map[string]string{"name": "web"}, view.FieldErrors{})
	if clean.HasErrors() {
		t.Error("HasErrors() = true with no errors")
	}
	if got := clean.Invalid(withHelp); got != "false" {
		t.Errorf("Invalid() = %q, want false", got)
	}
	if got := clean.DescribedBy(withHelp); got != clean.HintID(withHelp) {
		t.Errorf("DescribedBy() = %q, want just the hint when there is no error", got)
	}
	// No help and no error means no attribute at all.
	if got := clean.DescribedBy(noHelp); got != "" {
		t.Errorf("DescribedBy() = %q, want empty so the attribute is omitted", got)
	}

	broken := formModel(map[string]string{}, view.FieldErrors{"name": {"is required"}})
	if !broken.HasErrors() {
		t.Error("HasErrors() = false with an error present")
	}
	if got := broken.Invalid(withHelp); got != "true" {
		t.Errorf("Invalid() = %q, want true", got)
	}
	if got := broken.FieldErrorsFor(withHelp); len(got) != 1 || got[0] != "is required" {
		t.Errorf("FieldErrorsFor() = %v", got)
	}
	// Both ids, in order, so the hint is announced before the complaint.
	want := broken.HintID(withHelp) + " " + broken.ErrorID(withHelp)
	if got := broken.DescribedBy(withHelp); got != want {
		t.Errorf("DescribedBy() = %q, want %q", got, want)
	}
	// An error on a field with no help still wires the error alone.
	errOnly := formModel(map[string]string{}, view.FieldErrors{"notes": {"too long"}})
	if got := errOnly.DescribedBy(noHelp); got != errOnly.ErrorID(noHelp) {
		t.Errorf("DescribedBy() = %q, want just the error id", got)
	}
	if got := len(broken.Summary()); got != 1 {
		t.Errorf("Summary() listed %d errors, want 1", got)
	}
}

// TestFormModel_ValuesAndChoices covers the accessors that fill controls.
func TestFormModel_ValuesAndChoices(t *testing.T) {
	m := formModel(map[string]string{"name": "web", "state": "ok"}, view.FieldErrors{})

	if got := m.Value(modelFields[0]); got != "web" {
		t.Errorf("Value() = %q", got)
	}
	if got := m.Choices(modelFields[1]); len(got) != 1 || got[0].Value != "ok" {
		t.Errorf("Choices() = %+v", got)
	}
	if got := m.ControlID(modelFields[0]); got != "f-name" {
		t.Errorf("ControlID() = %q", got)
	}
	// maxlength is rendered only when it bounds something. A literal "0"
	// would make every control reject every keystroke.
	if got := m.MaxLen(modelFields[0]); got != "64" {
		t.Errorf("MaxLen() = %q, want 64", got)
	}
	if got := m.MaxLen(modelFields[1]); got != "" {
		t.Errorf("MaxLen() = %q, want empty for an unbounded field", got)
	}
}

// TestFormModel_IsSelected covers the comma-separated encoding a lookup
// field's current values arrive in.
func TestFormModel_IsSelected(t *testing.T) {
	lookup := view.Field{Name: "members", Kind: view.KindLookup}
	m := view.FormModel{
		Descriptor: view.Descriptor{Name: "teams", Fields: []view.Field{lookup}},
		Values:     map[string]string{"members": "3, 7 ,11"},
	}

	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"3", true},
		// Surrounding space is the whole reason this splits and trims
		// rather than comparing the joined string.
		{"7", true},
		{"11", true},
		{"1", false},
		// A prefix of a real member must not match, or selecting 11 would
		// also select 1.
		{"", false},
	} {
		if got := m.IsSelected(lookup, tc.value); got != tc.want {
			t.Errorf("IsSelected(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestDeclaredModel_ShowsTheShapeItWillHave covers the rendering a view
// that is declared but not built gets.
func TestDeclaredModel_ShowsTheShapeItWillHave(t *testing.T) {
	d := view.Descriptor{
		Name:   "notifications",
		Title:  "Notifications",
		Fields: modelFields,
		Sections: []view.Section{
			{Title: "Targets", Fields: modelFields[:1], Empty: "The Notification Engine owns these."},
		},
	}
	m := view.DeclaredModel{Page: view.PageModel{Prefix: "/ui"}, Descriptor: d}

	table := m.Table()
	if !table.Preview {
		t.Error("a declared view's table is not a preview, so it renders as an empty real table")
	}
	if len(table.Columns) == 0 {
		t.Error("a declared view drew no columns, which is the one thing it has to offer")
	}

	tabs := m.FutureTabs()
	if !m.HasFutureTabs() || len(tabs) != 2 {
		t.Fatalf("FutureTabs() = %+v, want Details plus the declared section", tabs)
	}
	if tabs[0].Slug != "details" || tabs[1].Label != "Targets" {
		t.Errorf("FutureTabs() = %+v", tabs)
	}

	// A view with nothing but Details has no future worth listing, and must
	// not render a one-item list saying so.
	bare := view.DeclaredModel{Descriptor: view.Descriptor{Name: "labels", Title: "Labels", Fields: modelFields}}
	if bare.HasFutureTabs() || bare.FutureTabs() != nil {
		t.Errorf("FutureTabs() = %+v, want nothing for a view with only Details", bare.FutureTabs())
	}
}

// TestListModel_TableAndCount covers the collection page's own accessors.
func TestListModel_TableAndCount(t *testing.T) {
	d := view.Descriptor{Name: "devices", Title: "Devices", Fields: modelFields}
	m := view.ListModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: d,
		Rows:       []view.Row{{ID: "1"}, {ID: "2"}},
	}

	table := m.Table()
	if table.RecordView != "devices" {
		t.Errorf("Table().RecordView = %q, want the rows to link to their own view", table.RecordView)
	}
	if table.Caption != "Devices" || len(table.Rows) != 2 {
		t.Errorf("Table() = %+v", table)
	}

	// The label says "shown" rather than a bare total, because this store
	// is read through a cursor and has no total to report.
	if got := m.CountLabel(); !strings.HasSuffix(got, " shown") {
		t.Errorf("CountLabel() = %q, want it to say what it is counting", got)
	}
	if got := m.CountLabel(); got != "2 shown" {
		t.Errorf("CountLabel() = %q, want 2 shown", got)
	}
	one := view.ListModel{Descriptor: d, Rows: []view.Row{{ID: "1"}}}
	if got := one.CountLabel(); got != "1 shown" {
		t.Errorf("CountLabel() = %q, want 1 shown", got)
	}
}

// TestSettingsModels covers the settings area's identifiers, which label
// its fieldsets and address its tabs.
func TestSettingsModels(t *testing.T) {
	g := view.SettingsGroup{Title: "LDAP and Active Directory", Status: view.StatusImplemented}
	if got := g.ID(); got != "settings-ldap-and-active-directory" {
		t.Errorf("ID() = %q", got)
	}
	if !g.Implemented() {
		t.Error("Implemented() = false for an implemented group")
	}
	if (view.SettingsGroup{Title: "x"}).Implemented() {
		t.Error("a group with no status reads as implemented")
	}

	tile := view.SettingsTile{Title: "Authentication", Status: view.StatusImplemented}
	if got := tile.Slug(); got != "authentication" {
		t.Errorf("Slug() = %q", got)
	}
	if !tile.Implemented() {
		t.Error("Implemented() = false for an implemented tile")
	}

	// The settings area is not a registered view, so it has no descriptor
	// name for the navigation builder to have marked current. The title is
	// what stands in.
	if !(view.PageModel{Title: "Settings"}).SettingsCurrent() {
		t.Error("SettingsCurrent() = false on the settings page")
	}
	if (view.PageModel{Title: "Devices"}).SettingsCurrent() {
		t.Error("SettingsCurrent() = true on a page that is not settings")
	}
}
