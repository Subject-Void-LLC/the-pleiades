// Package view_test's row-action file: the half of the section write path
// that addresses one row rather than the record the section hangs off.
//
// The header half is covered beside Section in sections_test.go. What is
// different here, and what these tests are about, is that a row control
// carries two identifiers instead of one and is decided per row instead of
// per record. Both of those are places a plausible implementation is wrong
// in a way that still renders.
package view_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// rowActionDescriptor is a descriptor whose one section offers one row
// control, withheld from any row whose outcome cell reads "locked".
func rowActionDescriptor(rows ...view.Row) view.Descriptor {
	d := modelDescriptor()
	d.Sections = []view.Section{{
		Title:   "Inputs",
		Status:  view.StatusImplemented,
		Fields:  sectionFields,
		Empty:   "No inputs yet.",
		Rows:    func(context.Context, string) ([]view.Row, error) { return rows, nil },
		Actions: nil,
		RowActions: []view.RowAction{{
			Name:     "remove",
			Label:    "Remove",
			Endpoint: &apispec.SetCredentialTypeInputs,
			Confirm:  "This removes the input.",
			Applies:  func(r view.Row, _ view.RowPosition) bool { return r.Cells["outcome"] != "locked" },
			Submit: func(context.Context, string, string, view.Values) (string, view.FieldErrors, error) {
				return "", nil, nil
			},
		}},
	}}
	return d
}

// rowSectionView resolves the section's controls the way a record page does.
func rowSectionView(t *testing.T, d view.Descriptor, parent view.Row, permitted []auth.LinkRel, rows []view.Row) view.SectionView {
	t.Helper()
	m := view.DetailModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: d,
		Row:        parent,
		Aff:        view.NewAffordances(permitted),
		Sections:   []view.LoadedSection{{Spec: d.Sections[0], Rows: rows}},
		// The section's own tab, since a record page renders one tab
		// at a time and the default is the record's own fields.
		Tab: view.TabSlug(d.Sections[0].Title),
	}
	views := m.SectionViews()
	if len(views) != 1 {
		t.Fatalf("SectionViews() returned %d, want 1", len(views))
	}
	return views[0]
}

// permittedRel is the relation the row control above is gated on.
var permittedRel = []auth.LinkRel{apispec.SetCredentialTypeInputs.Rel}

// TestRowControls_AreDecidedPerRow is the property that separates a row
// action from the header action it is built beside.
//
// Three rows, one of which the action withholds itself from, so a resolver
// that decided once for the whole section fails whichever way it decided.
func TestRowControls_AreDecidedPerRow(t *testing.T) {
	rows := []view.Row{
		{ID: "username", Cells: view.Cells{"outcome": "free"}},
		{ID: "password", Cells: view.Cells{"outcome": "locked"}},
		{ID: "host", Cells: view.Cells{"outcome": "free"}},
	}
	s := rowSectionView(t, rowActionDescriptor(rows...), view.Row{ID: "7"}, permittedRel, rows)

	table := s.Table()
	if !table.HasRowControls() {
		t.Fatal("HasRowControls() = false, so no row control resolved at all")
	}
	for i, want := range []int{1, 0, 1} {
		if got := len(table.ControlsAt(i)); got != want {
			t.Errorf("row %d (%s) has %d controls, want %d", i, rows[i].ID, got, want)
		}
	}
}

// TestRowControls_CarryBothIdentifiers pins the href's shape, which is the
// only place the row's identity is recorded before the POST.
func TestRowControls_CarryBothIdentifiers(t *testing.T) {
	rows := []view.Row{{ID: "username", Cells: view.Cells{"outcome": "free"}}}
	s := rowSectionView(t, rowActionDescriptor(rows...), view.Row{ID: "7"}, permittedRel, rows)

	controls := s.Table().ControlsAt(0)
	if len(controls) != 1 {
		t.Fatalf("ControlsAt(0) returned %d, want 1", len(controls))
	}
	if got, want := controls[0].Href, "/ui/widgets/7/remove/username"; got != want {
		t.Errorf("Href = %q, want %q", got, want)
	}
	if !controls[0].Confirms() {
		t.Error("a control declaring Confirm text opens no dialog")
	}
}

// TestRowControls_EscapeBothIdentifiers is the defect path.Join hides: Join
// cleans its result, so an unescaped ".." is not a segment, it deletes the
// segment in front of it and points the control somewhere else entirely.
//
// Both ids are author data reaching the same path, so both are checked. An
// implementation that escaped the parent and forgot the row would pass a
// test that only looked at one of them, and the row id is the newer of the
// two and therefore the likelier to be forgotten.
func TestRowControls_EscapeBothIdentifiers(t *testing.T) {
	rows := []view.Row{{ID: "../../etc", Cells: view.Cells{"outcome": "free"}}}
	s := rowSectionView(t, rowActionDescriptor(rows...), view.Row{ID: "../../root"}, permittedRel, rows)

	controls := s.Table().ControlsAt(0)
	if len(controls) != 1 {
		t.Fatalf("ControlsAt(0) returned %d, want 1", len(controls))
	}
	// Five segments after the leading slash: ui, widgets, the escaped
	// parent, the action name, the escaped row. Counting segments rather
	// than looking for dots, because an escaped ".." is inert and is still
	// spelled with dots.
	got := len(strings.Split(strings.TrimPrefix(controls[0].Href, "/"), "/"))
	if got != 5 {
		t.Errorf("href %q has %d segments, want 5: an identifier escaped the path", controls[0].Href, got)
	}
}

// TestRowControls_AreWithheldByTheSameGatesTheHeaderHalfUses covers the two
// decisions taken once for the whole section rather than per row.
//
// Both matter for one reason: a managed credential type refuses every write,
// so withholding Remove from its rows has to be the same judgement that
// withholds Add input from its header. Reaching that judgement in two places
// is how the two drift apart.
func TestRowControls_AreWithheldByTheSameGatesTheHeaderHalfUses(t *testing.T) {
	rows := []view.Row{{ID: "username", Cells: view.Cells{"outcome": "free"}}}

	t.Run("without the relation", func(t *testing.T) {
		s := rowSectionView(t, rowActionDescriptor(rows...), view.Row{ID: "7"},
			[]auth.LinkRel{auth.RelSelf}, rows)
		if s.Table().HasRowControls() {
			t.Error("a row control rendered for a caller without the relation")
		}
	})

	t.Run("when the parent record does not qualify", func(t *testing.T) {
		d := rowActionDescriptor(rows...)
		d.Applies = func(view.Row, auth.LinkRel) bool { return false }
		s := rowSectionView(t, d, view.Row{ID: "7"}, permittedRel, rows)
		if s.Table().HasRowControls() {
			t.Error("a row control rendered on a record whose Applies refuses the relation")
		}
	})

	t.Run("on a collection page, where there is no parent record", func(t *testing.T) {
		s := rowSectionView(t, rowActionDescriptor(rows...), view.Row{}, permittedRel, rows)
		if s.Table().HasRowControls() {
			t.Error("a row control rendered with no record for it to act on")
		}
	})

	t.Run("on a row with no identifier to address", func(t *testing.T) {
		unaddressable := []view.Row{{Cells: view.Cells{"outcome": "free"}}}
		s := rowSectionView(t, rowActionDescriptor(unaddressable...), view.Row{ID: "7"}, permittedRel, unaddressable)
		if s.Table().HasRowControls() {
			t.Error("a row control rendered for a row with no id, so it would post to a trailing empty segment")
		}
	})
}

// TestRowControls_DialogIDsAreUniqueAcrossRowsThatSlugAlike is why the
// dialog id is built from the row's index rather than from its id.
//
// An id is author data. "api.key" and "api-key" are two different inputs and
// slug to the same string, so ids built from them would collide, two dialogs
// would share one element id, and the opener would open whichever the
// document happened to reach first: a confirmation that names one row and
// removes another.
func TestRowControls_DialogIDsAreUniqueAcrossRowsThatSlugAlike(t *testing.T) {
	rows := []view.Row{
		{ID: "api.key", Cells: view.Cells{"outcome": "free"}},
		{ID: "api-key", Cells: view.Cells{"outcome": "free"}},
	}
	s := rowSectionView(t, rowActionDescriptor(rows...), view.Row{ID: "7"}, permittedRel, rows)

	table := s.Table()
	first, second := table.ControlsAt(0), table.ControlsAt(1)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("controls = %d and %d, want 1 each", len(first), len(second))
	}
	if first[0].DialogID == second[0].DialogID {
		t.Errorf("both rows carry dialog id %q, so one opener opens the other row's confirmation",
			first[0].DialogID)
	}
}

// TestTableModel_ColumnSpanCountsTheControlsColumn keeps the zero state the
// full width of the table it sits in. One short leaves a stray empty cell
// beside the sentence explaining why the table is empty.
func TestTableModel_ColumnSpanCountsTheControlsColumn(t *testing.T) {
	plain := view.TableModel{Columns: sectionFields[:2]}
	if got := plain.ColumnSpan(); got != "2" {
		t.Errorf("ColumnSpan() = %q with no controls, want 2", got)
	}

	withControls := view.TableModel{
		Columns:     sectionFields[:2],
		Rows:        []view.Row{{ID: "a"}},
		RowControls: [][]view.RowControl{{{Label: "Remove", Href: "/ui/x/1/remove/a"}}},
	}
	if got := withControls.ColumnSpan(); got != "3" {
		t.Errorf("ColumnSpan() = %q with a controls column, want 3", got)
	}
}

// TestTableModel_ControlsAtIsBoundsChecked matters because it is called from
// a template. A range past the end there panics inside a render, and a panic
// inside a render answers the page with a blank screen rather than an error.
func TestTableModel_ControlsAtIsBoundsChecked(t *testing.T) {
	m := view.TableModel{RowControls: [][]view.RowControl{{{Label: "Remove"}}}}
	for _, i := range []int{-1, 1, 99} {
		if got := m.ControlsAt(i); got != nil {
			t.Errorf("ControlsAt(%d) = %v, want nil", i, got)
		}
	}
}

// TestRegister_RefusesAnUnreachableRowAction covers the declarations that
// would register cleanly and then never work.
func TestRegister_RefusesAnUnreachableRowAction(t *testing.T) {
	valid := func() view.RowAction {
		return view.RowAction{
			Name:     "remove",
			Label:    "Remove",
			Endpoint: &apispec.SetCredentialTypeInputs,
			Submit: func(context.Context, string, string, view.Values) (string, view.FieldErrors, error) {
				return "", nil, nil
			},
		}
	}

	cases := []struct {
		name    string
		mutate  func(*view.Descriptor)
		wantErr string
	}{
		{
			name: "a name chi would never route to",
			mutate: func(d *view.Descriptor) {
				a := valid()
				a.Name = "edit"
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "reserved path segment",
		},
		{
			// "download" was added to the static route table without
			// being added to the reserved set, so for as long as
			// downloads have existed an action could take the name,
			// register cleanly, and be shadowed forever with nothing
			// reporting it -- while handler.go's own comment said
			// Register refused exactly this.
			name: "a name the download route already owns",
			mutate: func(d *view.Descriptor) {
				a := valid()
				a.Name = "download"
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "reserved path segment",
		},
		{
			name: "a name a record action already owns",
			mutate: func(d *view.Descriptor) {
				a := valid()
				a.Name = "run"
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "collides with an action",
		},
		{
			name: "a name another section's row action already owns",
			mutate: func(d *view.Descriptor) {
				d.Sections[0].RowActions = []view.RowAction{valid()}
				second := d.Sections[0]
				second.Title = "Injectors"
				d.Sections = append(d.Sections, second)
			},
			wantErr: "collides with a row action",
		},
		{
			name: "a button with no accessible name",
			mutate: func(d *view.Descriptor) {
				a := valid()
				a.Label = ""
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "has no label",
		},
		{
			name: "nothing to gate it on",
			mutate: func(d *view.Descriptor) {
				a := valid()
				a.Endpoint = nil
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "names no endpoint",
		},
		{
			name: "a control that would reach nothing",
			mutate: func(d *view.Descriptor) {
				a := valid()
				a.Submit = nil
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "has no Submit function",
		},
		{
			name: "a write control on a section that says it is not wired",
			mutate: func(d *view.Descriptor) {
				d.Sections[0].Status = view.StatusDeclared
				d.Sections[0].Rows = nil
				d.Sections[0].RowActions = []view.RowAction{valid()}
			},
			wantErr: "declared but declares row actions",
		},
		{
			name: "a stale copy of the endpoint it names",
			mutate: func(d *view.Descriptor) {
				stale := apispec.SetCredentialTypeInputs
				stale.Scope = auth.ScopeInventoryRead
				a := valid()
				a.Endpoint = &stale
				d.Sections[0].RowActions = []view.RowAction{a}
			},
			wantErr: "stale copy",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(view.SnapshotForTest())
			d := validDescriptor("row-action-" + view.TabSlug(tc.name))
			d.Actions = []view.RecordAction{{
				Name: "run", Label: "Run", Endpoint: &apispec.LaunchTemplate,
				Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
					return "", nil, nil
				},
			}}
			d.Sections = []view.Section{{
				Title:  "Inputs",
				Status: view.StatusImplemented,
				Fields: sectionFields,
				Empty:  "No inputs yet.",
				Rows:   func(context.Context, string) ([]view.Row, error) { return nil, nil },
			}}
			tc.mutate(&d)

			err := view.Register(d)
			if err == nil {
				t.Fatalf("Register() = nil, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Register() = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestRegister_AcceptsARowActionBesideTheHeaderHalf is the positive control
// the table above needs: every refusal there would also be produced by a
// validator that refused everything.
func TestRegister_AcceptsARowActionBesideTheHeaderHalf(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	d := validDescriptor("row-action-accepted")
	d.Actions = []view.RecordAction{{
		Name: "add-input", Label: "Add input", Endpoint: &apispec.SetCredentialTypeInputs,
		Heading: "Add an input",
		Fields:  []view.Field{{Name: "id", Label: "ID", Kind: view.KindText, InForm: true}},
		Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
			return "", nil, nil
		},
	}}
	d.Sections = []view.Section{{
		Title:   "Inputs",
		Status:  view.StatusImplemented,
		Fields:  sectionFields,
		Empty:   "No inputs yet.",
		Rows:    func(context.Context, string) ([]view.Row, error) { return nil, nil },
		Actions: []string{"add-input"},
		RowActions: []view.RowAction{{
			// The same endpoint the header action names, which is the real
			// shape: adding to a document and removing from it are one API
			// operation and one affordance question.
			Name: "remove-input", Label: "Remove", Endpoint: &apispec.SetCredentialTypeInputs,
			Submit: func(context.Context, string, string, view.Values) (string, view.FieldErrors, error) {
				return "", nil, nil
			},
		}},
	}}

	if err := view.Register(d); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
}

// TestDescriptor_CandidatesOffersARowActionsRelation is the gap that made
// every row control invisible rather than merely ungated.
//
// Candidates is what the affordance generator is ASKED about. A relation
// missing from it is never permitted, so the control is withheld from
// everybody, including an administrator, and a Remove button that is simply
// never drawn reads as a design decision rather than as a bug.
func TestDescriptor_CandidatesOffersARowActionsRelation(t *testing.T) {
	d := rowActionDescriptor()

	var found bool
	for _, c := range d.Candidates() {
		if c.Rel == apispec.SetCredentialTypeInputs.Rel {
			found = true
			if c.Scope != apispec.SetCredentialTypeInputs.Scope {
				t.Errorf("candidate scope = %q, want %q", c.Scope, apispec.SetCredentialTypeInputs.Scope)
			}
		}
	}
	if !found {
		t.Error("Candidates() omits a row action's relation, so no caller can ever be permitted it")
	}
}
