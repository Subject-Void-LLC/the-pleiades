package view_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers the two composition points a descriptor gained beyond
// list-and-form: Section, the read-only related-record table, and
// RecordAction, the named operation that is neither CRUD nor a link. Both
// render through the shared templates, so their model methods carry the
// decisions the templates are not allowed to make.

// sectionFields are the columns a section renders. Only InList matters here:
// a section has no form.
var sectionFields = []view.Field{
	{Name: "device", Label: "DEVICE", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "outcome", Label: "OUTCOME", Kind: view.KindBadge, InList: true,
		BadgeClass: func(v string) string {
			if v == "dispatched" {
				return "badge-ok"
			}
			return "invented-class"
		}},
	{Name: "reason", Label: "REASON", Kind: view.KindText, InList: true},
	{Name: "hidden", Label: "HIDDEN", Kind: view.KindText},
}

// loadedSection builds a section with rows in hand, as a handler would hand
// it to a template.
func loadedSection(title string, rows ...view.Row) view.LoadedSection {
	return view.LoadedSection{
		Spec: view.Section{
			Title:   title,
			Summary: "what happened on each device",
			Fields:  sectionFields,
			Empty:   "Nothing recorded yet.",
			Rows: func(context.Context, string) ([]view.Row, error) {
				return rows, nil
			},
		},
		Rows: rows,
	}
}

// TestLoadedSection_IDIsASafeSlugOfTheTitle matters because the value reaches
// an id attribute and an aria-labelledby reference. A title with a space, a
// slash or a quote in it would otherwise produce either a broken reference or
// an attribute injection.
func TestLoadedSection_IDIsASafeSlugOfTheTitle(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  string
	}{
		{"Device outcomes", "section-device-outcomes"},
		{"Operator notices", "section-operator-notices"},
		{"UPPER Case", "section-upper-case"},
		// Only the three delimiters become dashes. The letters survive,
		// which is fine: what makes this safe is that nothing outside
		// [a-z0-9-] can reach the attribute, not that the text is unreadable.
		{`"><script>`, "section----script-"},
		{"a/b", "section-a-b"},
	} {
		got := loadedSection(tc.title).ID()
		if got != tc.want {
			t.Errorf("Section(%q).ID() = %q, want %q", tc.title, got, tc.want)
		}
		// Whatever the title, the id must be usable unquoted in an
		// attribute and as a fragment.
		if strings.ContainsAny(got, ` "'<>&/`) {
			t.Errorf("Section(%q).ID() = %q contains a character unsafe in an attribute", tc.title, got)
		}
	}
}

// TestLoadedSection_HeadingIDDerivesFromTheID keeps the <h2> id and the
// aria-labelledby reference in lockstep. If they were built independently one
// could be changed without the other and the label would dangle silently.
func TestLoadedSection_HeadingIDDerivesFromTheID(t *testing.T) {
	s := loadedSection("Device outcomes")
	if want := s.ID() + "-heading"; s.HeadingID() != want {
		t.Errorf("HeadingID() = %q, want %q", s.HeadingID(), want)
	}
}

// TestLoadedSection_ColumnsAreTheListedFieldsOnly proves a section honours
// InList the same way the main list does, so a field declared for a detail
// view does not silently widen a related table.
func TestLoadedSection_ColumnsAreTheListedFieldsOnly(t *testing.T) {
	cols := loadedSection("Device outcomes").Columns()
	if len(cols) != 3 {
		t.Fatalf("Columns() returned %d fields, want the 3 marked InList", len(cols))
	}
	for _, c := range cols {
		if c.Name == "hidden" {
			t.Error("a field not marked InList became a column")
		}
	}
}

// TestLoadedSection_BadgeClassRefusesAnythingOutsideTheSet is the same
// guarantee the main list makes, and it exists here because a section is not
// a lesser table: a caller-controlled string reaching a class attribute is
// exactly what the content security policy is meant to make impossible.
func TestLoadedSection_BadgeClassRefusesAnythingOutsideTheSet(t *testing.T) {
	row := view.Row{ID: "d1", Cells: view.Cells{"device": "rtr-01", "outcome": "exploded"}}
	s := loadedSection("Device outcomes", row)

	outcome := sectionFields[1]
	if got := s.BadgeClass(row, outcome); got != "badge-neutral" {
		t.Errorf("BadgeClass for an unmapped value = %q, want badge-neutral", got)
	}

	ok := view.Row{ID: "d2", Cells: view.Cells{"outcome": "dispatched"}}
	if got := s.BadgeClass(ok, outcome); got != "badge-ok" {
		t.Errorf("BadgeClass for a mapped value = %q, want badge-ok", got)
	}

	// A field with no mapping at all degrades rather than rendering an
	// empty class attribute.
	if got := s.BadgeClass(row, sectionFields[0]); got != "badge-neutral" {
		t.Errorf("BadgeClass with no BadgeClass func = %q, want badge-neutral", got)
	}
}

func TestLoadedSection_CellAndPrimaryAndRows(t *testing.T) {
	row := view.Row{ID: "d1", Cells: view.Cells{"device": "rtr-01", "outcome": "dispatched"}}
	s := loadedSection("Device outcomes", row)

	if got := s.Cell(row, sectionFields[0]); got != "rtr-01" {
		t.Errorf("Cell = %q, want rtr-01", got)
	}
	// A missing cell renders empty rather than panicking, because a section's
	// rows come from a different port than its columns were declared against.
	if got := s.Cell(row, sectionFields[2]); got != "" {
		t.Errorf("Cell for an absent value = %q, want empty", got)
	}
	if got := s.IsPrimary(sectionFields[0]); got != "true" {
		t.Errorf("IsPrimary(device) = %q, want true", got)
	}
	if got := s.IsPrimary(sectionFields[1]); got != "false" {
		t.Errorf("IsPrimary(outcome) = %q, want false", got)
	}
	if !s.HasRows() {
		t.Error("HasRows() = false with a row present")
	}
	if loadedSection("Empty").HasRows() {
		t.Error("HasRows() = true with no rows")
	}
}

// actionDescriptor is a descriptor carrying one record action, shaped like
// the runbook Run action: an endpoint whose relation is execute rather than
// create, and one prompted field.
func actionDescriptor() view.Descriptor {
	d := modelDescriptor()
	d.Actions = []view.RecordAction{{
		Name:     "run",
		Label:    "Run",
		Heading:  "Run runbook",
		Endpoint: &apispec.LaunchTemplate,
		Fields: []view.Field{
			{Name: "group", Label: "TARGET GROUP", Kind: view.KindText,
				Required: true, InForm: true, Help: "Which group this runs against."},
		},
		Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
			return "", nil, nil
		},
	}}
	return d
}

// TestRecordAction_PromptsReflectsDeclaredFields decides whether opening the
// action renders a form or redirects, so it is the difference between a GET
// that asks a question and a GET that runs a job.
func TestRecordAction_PromptsReflectsDeclaredFields(t *testing.T) {
	prompting := actionDescriptor().Actions[0]
	if !prompting.Prompts() {
		t.Error("an action with fields does not prompt")
	}

	silent := prompting
	silent.Fields = nil
	if silent.Prompts() {
		t.Error("an action with no fields claims to prompt")
	}
}

// TestDetailModel_ActionsAreGatedByAffordanceAndApplies proves a record
// action goes through the same two filters an edit or delete control does:
// the permitted set the JSON _links array is computed from, and the record's
// own state.
func TestDetailModel_ActionsAreGatedByAffordanceAndApplies(t *testing.T) {
	row := view.Row{ID: "reboot-edge", Cells: view.Cells{"name": "reboot-edge", "state": "ok"}}

	t.Run("offered when permitted", func(t *testing.T) {
		m := view.DetailModel{
			Page:       view.PageModel{Prefix: "/ui"},
			Descriptor: actionDescriptor(),
			Row:        row,
			Aff:        view.NewAffordances([]auth.LinkRel{apispec.LaunchTemplate.Rel}),
		}
		actions := m.Actions()
		if len(actions) != 1 {
			t.Fatalf("Actions() returned %d, want 1", len(actions))
		}
		if actions[0].Label != "Run" {
			t.Errorf("label = %q, want Run", actions[0].Label)
		}
		if actions[0].Href != "/ui/widgets/reboot-edge/run" {
			t.Errorf("href = %q, want /ui/widgets/reboot-edge/run", actions[0].Href)
		}
	})

	t.Run("withheld when the relation is not permitted", func(t *testing.T) {
		m := view.DetailModel{
			Page:       view.PageModel{Prefix: "/ui"},
			Descriptor: actionDescriptor(),
			Row:        row,
			Aff:        view.NewAffordances([]auth.LinkRel{auth.RelSelf}),
		}
		if got := m.Actions(); len(got) != 0 {
			t.Errorf("Actions() returned %v for a caller without the relation", got)
		}
	})

	// The same per-record withdrawal that hides delete on an archived
	// device: an action must be offered on exactly the records it would
	// actually work on.
	t.Run("withheld when Applies says no for this record", func(t *testing.T) {
		d := actionDescriptor()
		d.Applies = func(r view.Row, rel auth.LinkRel) bool {
			return !(rel == apispec.LaunchTemplate.Rel && r.Cells["state"] == "ok")
		}
		m := view.DetailModel{
			Page:       view.PageModel{Prefix: "/ui"},
			Descriptor: d,
			Row:        row,
			Aff:        view.NewAffordances([]auth.LinkRel{apispec.LaunchTemplate.Rel}),
		}
		if got := m.Actions(); len(got) != 0 {
			t.Errorf("Actions() returned %v despite Applies withdrawing it", got)
		}
	})
}

// TestDetailModel_ActionHrefEscapesTheIdentifier is the same defect class
// path.Join hides elsewhere in this file's neighbours: Join cleans its
// result, so an unescaped ".." would not be a segment, it would delete the
// segment in front of it and point the control somewhere else.
func TestDetailModel_ActionHrefEscapesTheIdentifier(t *testing.T) {
	m := view.DetailModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: actionDescriptor(),
		Row:        view.Row{ID: "../../etc", Cells: view.Cells{}},
		Aff:        view.NewAffordances([]auth.LinkRel{apispec.LaunchTemplate.Rel}),
	}
	actions := m.Actions()
	if len(actions) != 1 {
		t.Fatalf("Actions() returned %d, want 1", len(actions))
	}
	// Four segments after the leading slash: ui, widgets, the escaped id,
	// and the action name. Counting segments rather than looking for dots,
	// because an escaped ".." is inert and still spelled with dots.
	if n := len(strings.Split(strings.TrimPrefix(actions[0].Href, "/"), "/")); n != 4 {
		t.Errorf("href %q has %d segments, want 4: the identifier escaped the path", actions[0].Href, n)
	}
}

// actionModel builds the prompt model a record action renders through.
func actionModel() view.ActionModel {
	return view.ActionModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: actionDescriptor(),
		Action:     actionDescriptor().Actions[0],
		ID:         "reboot-edge",
		Fields:     actionDescriptor().Actions[0].Fields,
		Values:     map[string]string{"group": "routers"},
		Errors:     view.FieldErrors{},
		Options:    map[string][]view.Option{},
	}
}

// TestActionModel_FormCarriesTheActionsFieldsNotTheResources is the point of
// the projection. Running a runbook prompts for a target group, not for the
// runbook's own columns, so the synthesized descriptor must carry the
// action's fields.
func TestActionModel_FormCarriesTheActionsFieldsNotTheResources(t *testing.T) {
	form := actionModel().Form()

	fields := form.Fields()
	if len(fields) != 1 || fields[0].Name != "group" {
		t.Fatalf("Form().Fields() = %v, want just the action's own group field", fieldNames(fields))
	}
	if form.Descriptor.Title != "Run runbook" {
		t.Errorf("form title = %q, want the action's heading", form.Descriptor.Title)
	}
	// The resource name is preserved, because the form's own Action() would
	// otherwise post to the wrong resource.
	if form.Descriptor.Name != "widgets" {
		t.Errorf("form resource = %q, want widgets", form.Descriptor.Name)
	}
	if form.Value(fields[0]) != "routers" {
		t.Errorf("submitted value did not survive the projection: %q", form.Value(fields[0]))
	}
}

// TestActionModel_FormReusesTheSharedAccessibilityWiring proves the
// projection is worth having: the labels, hints, error ids and aria wiring
// all come from FormModel rather than from a second copy that would need
// keeping accessible separately.
func TestActionModel_FormReusesTheSharedAccessibilityWiring(t *testing.T) {
	m := actionModel()
	m.Errors = view.FieldErrors{"group": {"That group no longer exists."}}
	form := m.Form()
	field := form.Fields()[0]

	if form.ControlID(field) != "f-group" {
		t.Errorf("ControlID = %q, want f-group", form.ControlID(field))
	}
	if form.Invalid(field) != "true" {
		t.Errorf("Invalid = %q, want true", form.Invalid(field))
	}
	// Both the hint and the error are announced with the control, in that
	// order, rather than one orphaning the other.
	if got := form.DescribedBy(field); got != "f-group-hint f-group-error" {
		t.Errorf("DescribedBy = %q, want both ids", got)
	}
	if !form.HasErrors() {
		t.Error("HasErrors() = false with a field error present")
	}
	if summary := form.Summary(); len(summary) != 1 || summary[0].Label != "TARGET GROUP" {
		t.Errorf("Summary() = %v, want the action's own field label", summary)
	}
}

func TestActionModel_HeadingAndHrefs(t *testing.T) {
	m := actionModel()

	if want := "Run runbook: reboot-edge"; m.Heading() != want {
		t.Errorf("Heading() = %q, want %q", m.Heading(), want)
	}
	// The button says what it does. "Save" would be wrong: this runs
	// something, it does not store something.
	if m.SubmitLabel() != "Run" {
		t.Errorf("SubmitLabel() = %q, want Run", m.SubmitLabel())
	}
	if want := "/ui/widgets/reboot-edge/run"; m.ActionHref() != want {
		t.Errorf("ActionHref() = %q, want %q", m.ActionHref(), want)
	}
	if want := "/ui/widgets/reboot-edge"; m.CancelHref() != want {
		t.Errorf("CancelHref() = %q, want %q", m.CancelHref(), want)
	}
}

func TestActionModel_HrefsEscapeTheIdentifier(t *testing.T) {
	m := actionModel()
	m.ID = "../../etc"

	for name, href := range map[string]string{
		"ActionHref": m.ActionHref(),
		"CancelHref": m.CancelHref(),
	} {
		segments := strings.Split(strings.TrimPrefix(href, "/"), "/")
		for _, s := range segments {
			if s == ".." {
				t.Errorf("%s = %q let the identifier escape the path", name, href)
			}
		}
	}
}

// fieldNames projects fields onto their names so a failure message is
// readable.
func fieldNames(fields []view.Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}

// refreshDescriptor is a descriptor that keeps itself current, with a
// predicate treating a completed record as terminal.
func refreshDescriptor() view.Descriptor {
	d := modelDescriptor()
	d.Refresh = &view.RefreshSpec{
		Interval: 5 * time.Second,
		Active:   func(row view.Row) bool { return row.Cells["state"] != "completed" },
	}
	return d
}

// TestListModel_PollsOnlyWithARefreshURL is the guard on the behaviour that
// shipped broken once: an absent URL means "do not refresh this one", and
// there is deliberately no default to fall back to. A reader who paged
// forward must not have page one swapped underneath them.
func TestListModel_PollsOnlyWithARefreshURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{"a refresh URL polls", "/ui/widgets", true},
		{"no refresh URL does not", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := listModel()
			m.Descriptor = refreshDescriptor()
			m.RefreshURL = tc.url

			if m.Polls() != tc.want {
				t.Errorf("Polls() = %v, want %v", m.Polls(), tc.want)
			}
			if got := m.RefreshTrigger(); (got != "") != tc.want {
				t.Errorf("RefreshTrigger() = %q with Polls() = %v", got, tc.want)
			}
			if m.RefreshHref() != tc.url {
				t.Errorf("RefreshHref() = %q, want %q", m.RefreshHref(), tc.url)
			}
		})
	}
}

// TestListModel_DoesNotPollWithoutARefreshSpec proves a view that declares
// no refresh stays a snapshot even when a URL is present.
func TestListModel_DoesNotPollWithoutARefreshSpec(t *testing.T) {
	m := listModel()
	m.RefreshURL = "/ui/widgets"

	if m.Polls() {
		t.Error("a view declaring no refresh polled anyway")
	}
	if got := m.RefreshTrigger(); got != "" {
		t.Errorf("RefreshTrigger() = %q, want empty", got)
	}
}

// TestDetailModel_StopsPollingAtATerminalRecord is the property that makes a
// finished job free to leave open. The refreshed fragment carries no
// trigger, so the polling ends there with nothing to cancel.
func TestDetailModel_StopsPollingAtATerminalRecord(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  bool
	}{
		{"running", true},
		{"completed", false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			m := view.DetailModel{
				Page:       view.PageModel{Prefix: "/ui"},
				Descriptor: refreshDescriptor(),
				Row:        view.Row{ID: "job-7", Cells: view.Cells{"name": "job-7", "state": tc.state}},
			}
			if m.Polls() != tc.want {
				t.Errorf("Polls() = %v for state %q, want %v", m.Polls(), tc.state, tc.want)
			}
			if got := m.RefreshTrigger(); (got != "") != tc.want {
				t.Errorf("RefreshTrigger() = %q for state %q", got, tc.state)
			}
			// The record's own URL, always: a refresh is the same read.
			if want := "/ui/widgets/job-7"; m.RefreshHref() != want {
				t.Errorf("RefreshHref() = %q, want %q", m.RefreshHref(), want)
			}
		})
	}
}

// TestDetailModel_PollsWhenNoPredicateIsDeclared covers the nil-Active case,
// which means "always" for a record just as it does for a collection.
func TestDetailModel_PollsWhenNoPredicateIsDeclared(t *testing.T) {
	d := modelDescriptor()
	d.Refresh = &view.RefreshSpec{Interval: 2 * time.Second}
	m := view.DetailModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: d,
		Row:        view.Row{ID: "alpha", Cells: view.Cells{}},
	}

	if !m.Polls() {
		t.Error("a record with no Active predicate stopped polling")
	}
	if got := m.RefreshTrigger(); got != "every 2s" {
		t.Errorf("RefreshTrigger() = %q, want \"every 2s\"", got)
	}
}

// TestDetailModel_RefreshAnnouncementChangesWithTheRecord is what makes a
// polled announcement bearable. app.js speaks only when this value changes,
// so it must move when the record's state does and stay put otherwise.
func TestDetailModel_RefreshAnnouncementChangesWithTheRecord(t *testing.T) {
	announcement := func(state string) string {
		return view.DetailModel{
			Descriptor: refreshDescriptor(),
			Row:        view.Row{ID: "job-7", Cells: view.Cells{"name": "job-7", "state": state}},
		}.RefreshAnnouncement()
	}

	running, completed := announcement("running"), announcement("completed")
	if running == completed {
		t.Error("the announcement did not change when the record's state did, so a screen reader is never told")
	}
	if announcement("running") != running {
		t.Error("the announcement is not stable for an unchanged record, so it would be read out on every tick")
	}
	// It names the record, so an announcement heard out of context still
	// says what it is about.
	if !strings.Contains(running, "job-7") {
		t.Errorf("announcement %q does not name the record", running)
	}
	if !strings.Contains(completed, "completed") {
		t.Errorf("announcement %q does not carry the state that changed", completed)
	}
}

// TestRegister_RefusesAnUnservableRefresh covers both validation rules. A
// sub-second interval works perfectly in a one-tab test and multiplies every
// open tab by ten requests a second against the handler that renders the
// page, which is a fail-fast worth having.
func TestRegister_RefusesAnUnservableRefresh(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*view.Descriptor)
		wantMsg string
	}{
		{
			name:    "interval below the floor",
			mutate:  func(d *view.Descriptor) { d.Refresh = &view.RefreshSpec{Interval: 100 * time.Millisecond} },
			wantMsg: "below",
		},
		{
			name: "a declared view claiming to refresh",
			mutate: func(d *view.Descriptor) {
				d.Status = view.StatusDeclared
				d.Handlers = nil
				d.Refresh = &view.RefreshSpec{Interval: time.Second}
			},
			wantMsg: "declared but carries a refresh",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor("refresh-" + strings.ReplaceAll(tc.name, " ", "-"))
			tc.mutate(&d)
			err := view.Register(d)
			if err == nil {
				t.Fatal("an unservable refresh was accepted")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err = %q, want it to mention %q", err, tc.wantMsg)
			}
		})
	}
}

// TestRecordAction_ResolvesItsPromptPerRecord covers the seam a launch form
// needs: an action whose controls differ from record to record.
//
// A template declares which of its fields a launch may override, so a form
// built from one static list would render controls the resolver then
// reports as ignored. The fields a record opens are a property of that
// record, which is why an action may resolve them from it.
func TestRecordAction_ResolvesItsPromptPerRecord(t *testing.T) {
	static := view.RecordAction{
		Name:   "run",
		Fields: []view.Field{{Name: "group", Label: "GROUP", Kind: view.KindText, InForm: true}},
	}
	if !static.Prompts() {
		t.Error("an action declaring fields does not prompt")
	}
	fields, err := static.ResolveFields(context.Background(), "anything")
	if err != nil || len(fields) != 1 || fields[0].Name != "group" {
		t.Errorf("ResolveFields of a static action = %v, %v, want its own declaration", fieldNames(fields), err)
	}

	perRecord := view.RecordAction{
		Name: "launch",
		FieldsFor: func(_ context.Context, id string) ([]view.Field, error) {
			if id == "locked" {
				return nil, nil
			}
			return []view.Field{{Name: "limit", Label: "LIMIT", Kind: view.KindText, InForm: true}}, nil
		},
	}

	// It prompts even for the record that opens nothing. The resulting form
	// is a confirmation, which is the right answer for something that
	// launches production work, and the alternative would be deciding
	// whether to prompt before knowing which record was being acted on.
	if !perRecord.Prompts() {
		t.Error("an action resolving its fields per record does not prompt")
	}
	if fields, err := perRecord.ResolveFields(context.Background(), "locked"); err != nil || len(fields) != 0 {
		t.Errorf("ResolveFields of a record that opens nothing = %v, %v, want none", fieldNames(fields), err)
	}
	if fields, err := perRecord.ResolveFields(context.Background(), "open"); err != nil || len(fields) != 1 {
		t.Errorf("ResolveFields of a record that opens one field = %v, %v", fieldNames(fields), err)
	}

	// An action with neither prompts for nothing, which is the button that
	// posts straight through.
	if (view.RecordAction{Name: "cancel"}).Prompts() {
		t.Error("an action declaring no fields at all prompts")
	}
}

// TestSection_DeclaredSectionsSaySoRatherThanRenderingEmpty covers the
// other half of the same honesty rule Descriptor.Status carries: an empty
// table and a thing with no backing entity look identical, and this project
// has shipped that ambiguity twice.
func TestSection_DeclaredSectionsSaySoRatherThanRenderingEmpty(t *testing.T) {
	declared := view.Section{Title: "Notifications", Empty: "No entity backs this yet."}
	if declared.Implemented() {
		t.Error("a section with no status reads as implemented; empty must mean declared, as it does for a view")
	}

	implemented := view.Section{
		Status: view.StatusImplemented,
		Title:  "Completed jobs",
		Empty:  "Nothing yet.",
		Rows:   func(context.Context, string) ([]view.Row, error) { return nil, nil },
	}
	if !implemented.Implemented() {
		t.Error("a section declaring StatusImplemented does not read as implemented")
	}
}

// TestField_APasswordFieldIsNeverAColumn proves the rule is enforced rather
// than trusted. The kind exists so a value is not shown, and honouring
// InList for it would make one forgotten flag enough to print a secret into
// a table.
func TestField_APasswordFieldIsNeverAColumn(t *testing.T) {
	d := view.Descriptor{
		Name: "widgets", Title: "Widgets", NavLabel: "WIDGETS", IDField: "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true},
			{Name: "secret", Label: "SECRET", Kind: view.KindPassword, InList: true, InForm: true},
		},
	}

	for _, f := range d.ListFields() {
		if f.Kind == view.KindPassword {
			t.Error("a password field appears as a list column")
		}
	}
	// It is still writable: not being displayed is the point, not being
	// unusable.
	writable := false
	for _, f := range d.FormFields() {
		if f.Name == "secret" {
			writable = true
		}
	}
	if !writable {
		t.Error("a password field cannot be submitted, so no survey could ask for one")
	}
}
