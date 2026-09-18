package view_test

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The registry is process-wide and has no reset, exactly like every other
// Section 25 registry in this repository. Tests therefore register under
// names they own, rather than sharing one fixture that later tests would
// have to unpick.

type device struct {
	name  string
	state string
}

type fakeReader struct {
	items []device
	err   error
}

func (r fakeReader) List(context.Context, view.Query) (view.Page[device], error) {
	if r.err != nil {
		return view.Page[device]{}, r.err
	}
	return view.Page[device]{Items: r.items, NextCursor: "next"}, nil
}

func (r fakeReader) Get(_ context.Context, id string) (device, error) {
	if r.err != nil {
		return device{}, r.err
	}
	for _, d := range r.items {
		if d.name == id {
			return d, nil
		}
	}
	return device{}, errors.New("not found")
}

type fakeWriter struct {
	created []device
	deleted []string
	err     error
}

func (w *fakeWriter) Create(_ context.Context, v device) (string, error) {
	if w.err != nil {
		return "", w.err
	}
	w.created = append(w.created, v)
	return v.name, nil
}

func (w *fakeWriter) Update(_ context.Context, _ string, v device) error {
	if w.err != nil {
		return w.err
	}
	w.created = append(w.created, v)
	return nil
}

func (w *fakeWriter) Delete(_ context.Context, id string) error {
	if w.err != nil {
		return w.err
	}
	w.deleted = append(w.deleted, id)
	return nil
}

func testFields() []view.Field {
	return []view.Field{
		{Name: "name", Label: "NAME", Kind: view.KindText, Required: true, MaxLen: 8,
			Autocomplete: "off", InList: true, InForm: true, MobilePrimary: true},
		{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true},
		{Name: "seen", Label: "LAST SEEN", Kind: view.KindTimestamp, InList: true},
	}
}

func testProjector() view.Projector[device] {
	return view.Projector[device]{
		Row: func(d device) view.Row {
			return view.Row{ID: d.name, Cells: view.Cells{"name": d.name, "state": d.state}}
		},
		Form: func(d device) map[string]string {
			return map[string]string{"name": d.name}
		},
		Bind: func(v view.Values) (device, view.FieldErrors) {
			return device{name: v.Get("name")}, view.FieldErrors{}
		},
	}
}

// emptyChartData is a chart data function that succeeds and returns
// nothing, for the cases asserting on some other part of a ChartSpec.
func emptyChartData(context.Context) (view.ChartData, error) {
	return view.ChartData{}, nil
}

func validDescriptor(name string) view.Descriptor {
	return view.Descriptor{
		Name:     name,
		Title:    "Test View",
		NavLabel: "TEST",
		NavOrder: 10,
		Summary:  "A view used by tests.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   testFields(),
		Ops: view.Ops{
			Get:    &apispec.GetDevice,
			Delete: &apispec.DeleteDevice,
		},
		Handlers: view.MustBind[device](fakeReader{}, nil, testProjector()),
	}
}

func TestRegister_AcceptsAValidDescriptor(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	d := validDescriptor("register-valid")
	if err := view.Register(d); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	got, ok := view.Lookup("register-valid")
	if !ok {
		t.Fatal("Lookup() found nothing after a successful Register")
	}
	if got.Title != d.Title {
		t.Errorf("Lookup().Title = %q, want %q", got.Title, d.Title)
	}
	if !got.Implemented() {
		t.Error("Implemented() = false for a StatusImplemented descriptor")
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	d := validDescriptor("register-duplicate")
	if err := view.Register(d); err != nil {
		t.Fatalf("first Register() = %v, want nil", err)
	}
	if err := view.Register(d); err == nil {
		t.Fatal("second Register() = nil, want a duplicate error")
	}
}

func TestRegister_RejectsInvalidDescriptors(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	// Every case here is a real failure this validation exists to catch,
	// named by what would break in production if it were let through.
	cases := []struct {
		name    string
		mutate  func(*view.Descriptor)
		wantErr string
	}{
		{"empty name", func(d *view.Descriptor) { d.Name = "" }, "must match"},
		{"uppercase name", func(d *view.Descriptor) { d.Name = "Devices" }, "must match"},
		{"path traversal in name", func(d *view.Descriptor) { d.Name = "../etc" }, "must match"},
		{"no title", func(d *view.Descriptor) { d.Title = "  " }, "no title"},
		{"no nav label", func(d *view.Descriptor) { d.NavLabel = "" }, "no nav label"},
		{"no fields", func(d *view.Descriptor) { d.Fields = nil }, "declares no fields"},
		{"unlabelled field", func(d *view.Descriptor) {
			d.Fields = []view.Field{{Name: "name", Label: "", Kind: view.KindText}}
		}, "has no label"},
		{"duplicate field", func(d *view.Descriptor) {
			d.Fields = append(d.Fields, view.Field{Name: "name", Label: "AGAIN", Kind: view.KindText})
		}, "twice"},
		{"id field not declared", func(d *view.Descriptor) { d.IDField = "nope" }, "is not a declared field"},
		{"select without options", func(d *view.Descriptor) {
			d.Fields = append(d.Fields, view.Field{Name: "kind", Label: "KIND", Kind: view.KindSelect, InForm: true})
		}, "offers choices but declares no options"},
		// The same refusal must reach a lookup. Both kinds render a control
		// whose entire content is its options, so either one declared
		// without them is a control a caller can focus and never operate.
		{"lookup without options", func(d *view.Descriptor) {
			d.Fields = append(d.Fields, view.Field{Name: "members", Label: "MEMBERS", Kind: view.KindLookup, InForm: true})
		}, "offers choices but declares no options"},
		{"invented autocomplete token", func(d *view.Descriptor) {
			d.Fields[0].Autocomplete = "device-name"
		}, "unknown autocomplete token"},
		{"timestamp in a form", func(d *view.Descriptor) {
			d.Fields[2].InForm = true
		}, "cannot appear in a form"},
		// Immutable narrows a form from two modes to one. On a field no
		// form ever offered, it narrows nothing and reads as a promise
		// about the record that nothing is keeping.
		{"immutable field in no form", func(d *view.Descriptor) {
			d.Fields[2].Immutable = true
		}, "appears in no form"},
		{"two mobile primaries", func(d *view.Descriptor) {
			d.Fields[1].MobilePrimary = true
		}, "MobilePrimary"},
		{"implemented without handlers", func(d *view.Descriptor) { d.Handlers = nil }, "carries no handlers"},
		{"declared with handlers", func(d *view.Descriptor) { d.Status = view.StatusDeclared }, "declared but carries handlers"},
		{"implemented without a Get endpoint", func(d *view.Descriptor) { d.Ops.Get = nil }, "declares no Get endpoint"},
		{"chart without a caption", func(d *view.Descriptor) {
			d.Chart = &view.ChartSpec{Title: "T", Data: emptyChartData}
		}, "chart with no caption"},
		{"chart without a data function", func(d *view.Descriptor) {
			d.Chart = &view.ChartSpec{Title: "T", Caption: "C"}
		}, "no data function"},
		{"stream without a title", func(d *view.Descriptor) {
			d.Stream = &view.StreamSpec{PathPattern: "/api/v1/jobs/{id}/logs"}
		}, "stream with no title"},
		{"stream with a relative path", func(d *view.Descriptor) {
			d.Stream = &view.StreamSpec{Title: "Output", PathPattern: "jobs/{id}/logs"}
		}, "not an absolute path"},
		{"protocol-relative stream path", func(d *view.Descriptor) {
			d.Stream = &view.StreamSpec{Title: "Output", PathPattern: "//evil.example/{id}/logs"}
		}, "protocol-relative"},
		{"stream path with no id token", func(d *view.Descriptor) {
			d.Stream = &view.StreamSpec{Title: "Output", PathPattern: "/api/v1/jobs/logs"}
		}, "contains no {id} token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDescriptor("reject-" + strings.ToLower(strings.ReplaceAll(tc.name, " ", "-")))
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

// A view naming an endpoint the API does not serve is the exact failure
// this UI's affordance model would otherwise turn into a rendered button
// that 404s, so it is refused at registration.
func TestRegister_RejectsUnknownEndpoint(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	ghost := apispec.Endpoint{
		Name: "list_ghosts", Method: "GET", Pattern: "/ghosts",
		Scope: auth.ScopeInventoryRead, Rel: auth.RelCollection,
	}
	d := validDescriptor("reject-unknown-endpoint")
	d.Ops.List = &ghost

	err := view.Register(d)
	if err == nil || !strings.Contains(err.Error(), "not in apispec.Endpoints") {
		t.Fatalf("Register() = %v, want an unknown-endpoint error", err)
	}
}

func TestRegister_RejectsStaleEndpointCopy(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	stale := apispec.GetDevice
	stale.Scope = auth.ScopeInventoryWrite // the real one is inventory:read

	d := validDescriptor("reject-stale-endpoint")
	d.Ops.Get = &stale

	err := view.Register(d)
	if err == nil || !strings.Contains(err.Error(), "stale copy") {
		t.Fatalf("Register() = %v, want a stale-copy error", err)
	}
}

func TestRegister_RejectsAmbiguousRelations(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	d := validDescriptor("reject-ambiguous-rel")
	// Both carry RelSelf, so Can(RelSelf) could not say which was meant.
	d.Ops.Get = &apispec.GetDevice
	d.Ops.List = &apispec.GetJob

	err := view.Register(d)
	if err == nil || !strings.Contains(err.Error(), "for both") {
		t.Fatalf("Register() = %v, want an ambiguous-relation error", err)
	}
}

// TestRegister_AcceptsOneEndpointNamedTwice is the other side of the
// ambiguity rule, and it is the case that rule used to get wrong.
//
// Two controls can be two affordances onto a single API operation: adding
// an input to a credential type and removing one are both its set-inputs
// endpoint, because the stored document is replaced either way. The check
// compared relations without comparing the endpoint behind them, so it read
// that as ambiguity and refused registration with a message naming the same
// endpoint twice. The only way past it was to invent a second endpoint and
// a second relation that described no separate API operation.
func TestRegister_AcceptsOneEndpointNamedTwice(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	d := validDescriptor("accept-shared-endpoint")
	d.Actions = []view.RecordAction{
		sharedEndpointAction("add-thing"),
		sharedEndpointAction("remove-thing"),
	}

	if err := view.Register(d); err != nil {
		t.Fatalf("Register() = %v, want nil: two actions may share one endpoint", err)
	}
}

// sharedEndpointAction is two actions' worth of declaration differing only
// in name, so the test above turns on the endpoint and nothing else.
func sharedEndpointAction(name string) view.RecordAction {
	return view.RecordAction{
		Name:     name,
		Label:    name,
		Endpoint: &apispec.SetCredentialTypeInputs,
		Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
			return "", nil, nil
		},
	}
}

func TestMustRegister_PanicsOnInvalid(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister() did not panic on an invalid descriptor")
		}
	}()
	view.MustRegister(view.Descriptor{Name: "!!!"})
}

func TestNamesAndNav_AreOrdered(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	first := validDescriptor("nav-bravo")
	first.NavOrder = 20
	second := validDescriptor("nav-alpha")
	second.NavOrder = 10

	for _, d := range []view.Descriptor{first, second} {
		if err := view.Register(d); err != nil {
			t.Fatalf("Register(%s) = %v", d.Name, err)
		}
	}

	names := view.Names()
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("Names() is not sorted: %v", names)
		}
	}

	// Nav orders by NavOrder, so the lower-ordered view precedes the
	// alphabetically-earlier one.
	nav := view.Nav()
	var alphaAt, bravoAt = -1, -1
	for i, d := range nav {
		switch d.Name {
		case "nav-alpha":
			alphaAt = i
		case "nav-bravo":
			bravoAt = i
		}
	}
	if alphaAt == -1 || bravoAt == -1 {
		t.Fatalf("Nav() is missing a registered view: %v", nav)
	}
	if alphaAt > bravoAt {
		t.Errorf("Nav() put NavOrder 20 before NavOrder 10")
	}
}

func TestAll_ReturnsASnapshot(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	if err := view.Register(validDescriptor("all-snapshot")); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	snapshot := view.All()
	delete(snapshot, "all-snapshot")

	if _, ok := view.Lookup("all-snapshot"); !ok {
		t.Error("mutating the All() snapshot removed the entry from the live registry")
	}
}

func TestDescriptor_FieldSelectors(t *testing.T) {
	d := validDescriptor("field-selectors")

	if got := len(d.ListFields()); got != 3 {
		t.Errorf("ListFields() returned %d fields, want 3", got)
	}
	// Only "name" is both InForm and a writable kind.
	form := d.FormFields()
	if len(form) != 1 || form[0].Name != "name" {
		t.Errorf("FormFields() = %v, want just name", form)
	}
	if got := d.PrimaryField().Name; got != "name" {
		t.Errorf("PrimaryField() = %q, want name", got)
	}
}

func TestOpsCandidates_CarryScopeAndRelOnly(t *testing.T) {
	d := validDescriptor("ops-candidates")
	got := d.Ops.Candidates()

	if len(got) != 2 {
		t.Fatalf("Candidates() returned %d, want 2", len(got))
	}
	for _, c := range got {
		if c.Rel == "" || c.Scope == "" {
			t.Errorf("candidate %+v is missing a rel or scope", c)
		}
		if !reflect.DeepEqual(c.Target, auth.ScopeTarget{}) {
			t.Errorf("candidate %+v carries a target; no route resolves one yet", c)
		}
	}
}

func TestAffordances(t *testing.T) {
	a := view.NewAffordances([]auth.LinkRel{auth.RelSelf, auth.RelDelete})
	if !a.Can(auth.RelDelete) {
		t.Error("Can(delete) = false for a permitted relation")
	}
	if a.Can(auth.RelCreate) {
		t.Error("Can(create) = true for a relation that was never permitted")
	}
	if a.Unknown {
		t.Error("Unknown = true for a successfully evaluated set")
	}

	// The unknown state is distinct from "nothing permitted": a template
	// renders an alert for the first and a plain read-only page for the
	// second, and conflating them repeats FAILURE_PATTERNS #73 in HTML.
	u := view.UnknownAffordances()
	if !u.Unknown {
		t.Error("UnknownAffordances().Unknown = false")
	}
	if u.Can(auth.RelSelf) {
		t.Error("UnknownAffordances() permitted a relation")
	}
}

func TestNewValues_NarrowsToDeclaredFields(t *testing.T) {
	raw := url.Values{
		"name":     {"router-1"},
		"state":    {"active"}, // declared, but not writable (a badge)
		"is_admin": {"true"},   // never declared at all
		"_csrf":    {"token"},  // reserved, not a field
	}

	v, undeclared := view.NewValues(testFields(), raw, false)

	if got := v.Get("name"); got != "router-1" {
		t.Errorf("Get(name) = %q, want router-1", got)
	}
	// Mass assignment is structurally impossible: there is no method that
	// returns a value the descriptor did not declare as writable.
	if got := v.Get("is_admin"); got != "" {
		t.Errorf("Get(is_admin) = %q, want empty for an undeclared field", got)
	}
	if got := v.Get("state"); got != "" {
		t.Errorf("Get(state) = %q, want empty for a non-writable field", got)
	}

	want := []string{"is_admin", "state"}
	if len(undeclared) != len(want) {
		t.Fatalf("undeclared = %v, want %v", undeclared, want)
	}
	for i := range want {
		if undeclared[i] != want[i] {
			t.Errorf("undeclared[%d] = %q, want %q", i, undeclared[i], want[i])
		}
	}
}

func TestValues_BoolAndTags(t *testing.T) {
	fields := []view.Field{
		{Name: "enabled", Label: "ENABLED", Kind: view.KindBool, InForm: true},
		{Name: "tags", Label: "TAGS", Kind: view.KindTags, InForm: true},
	}

	cases := []struct {
		raw  string
		want bool
	}{{"", false}, {"0", false}, {"false", false}, {"off", false}, {"no", false}, {"on", true}, {"true", true}}
	for _, tc := range cases {
		v, _ := view.NewValues(fields, url.Values{"enabled": {tc.raw}}, false)
		if got := v.Bool("enabled"); got != tc.want {
			t.Errorf("Bool(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	// A naive Split on empty input yields one empty tag, which persists
	// as a device carrying a nameless tag.
	v, _ := view.NewValues(fields, url.Values{"tags": {"  "}}, false)
	if got := v.Tags("tags"); got != nil {
		t.Errorf("Tags(blank) = %v, want nil", got)
	}

	v, _ = view.NewValues(fields, url.Values{"tags": {" edge , , core "}}, false)
	got := v.Tags("tags")
	if len(got) != 2 || got[0] != "edge" || got[1] != "core" {
		t.Errorf("Tags() = %v, want [edge core]", got)
	}
}

// TestValues_SelectedReadsEveryChosenOption is the assertion that separates
// a multi-select from every other control.
//
// A multi-select submits one value per chosen option, and url.Values.Get
// returns only the first. Reading a five-member team through the singular
// accessor persists one member and silently drops four, which is a
// permissions change nobody asked for and nothing reports.
func TestValues_SelectedReadsEveryChosenOption(t *testing.T) {
	fields := []view.Field{
		{Name: "users", Label: "MEMBERS", Kind: view.KindLookup, InForm: true,
			Options: func(context.Context) ([]view.Option, error) { return nil, nil }},
		{Name: "name", Label: "NAME", Kind: view.KindText, InForm: true},
	}

	v, _ := view.NewValues(fields, url.Values{"users": {"3", "7", "11"}}, false)
	got := v.Selected("users")
	if len(got) != 3 || got[0] != "3" || got[1] != "7" || got[2] != "11" {
		t.Errorf("Selected() = %v, want all three chosen options", got)
	}

	// Blank entries are dropped: a multi-select can legitimately submit
	// nothing, and an empty string among ids is not a selection anybody
	// made.
	v, _ = view.NewValues(fields, url.Values{"users": {" 3 ", "", "  "}}, false)
	if got := v.Selected("users"); len(got) != 1 || got[0] != "3" {
		t.Errorf("Selected() with blanks = %v, want [3]", got)
	}

	// Undeclared names read as nothing, exactly as Get does, so a value
	// this type never narrowed cannot be reached through the plural door.
	v, _ = view.NewValues(fields, url.Values{"smuggled": {"1"}}, false)
	if got := v.Selected("smuggled"); got != nil {
		t.Errorf("Selected(undeclared) = %v, want nil", got)
	}
}

// TestFieldKind_LookupOffersChoicesAndSelectsMany pins the two predicates
// the render path and every option resolver now branch on. A lookup that
// stopped reporting that it offers choices would render as an empty listbox
// with no error anywhere.
func TestFieldKind_LookupOffersChoicesAndSelectsMany(t *testing.T) {
	lookup := view.Field{Name: "users", Kind: view.KindLookup}
	sel := view.Field{Name: "kind", Kind: view.KindSelect}
	text := view.Field{Name: "name", Kind: view.KindText}

	if !lookup.OffersChoices() || !sel.OffersChoices() {
		t.Error("a choice-backed kind does not report that it offers choices, so its options never resolve")
	}
	if text.OffersChoices() {
		t.Error("a text field reports that it offers choices")
	}
	if !lookup.SelectsMany() {
		t.Error("a lookup does not report that it submits many values")
	}
	if sel.SelectsMany() || text.SelectsMany() {
		t.Error("a single-valued kind reports that it submits many values")
	}
}

// TestQuery_PageSizeAppliesTheDocumentedDefault. Every reader over-fetches
// by one to observe whether a next page exists, and computing that from a
// zero limit asks for a single row and then slices it to nothing.
func TestQuery_PageSizeAppliesTheDocumentedDefault(t *testing.T) {
	for _, tc := range []struct {
		limit, want int
	}{{0, view.DefaultPageSize}, {-5, view.DefaultPageSize}, {10, 10}} {
		if got := (view.Query{Limit: tc.limit}).PageSize(); got != tc.want {
			t.Errorf("Query{Limit: %d}.PageSize() = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

func TestValidate_EnforcesTheDeclaration(t *testing.T) {
	fields := []view.Field{
		{Name: "name", Label: "NAME", Kind: view.KindText, Required: true, MaxLen: 4, InForm: true},
		{Name: "count", Label: "COUNT", Kind: view.KindNumber, InForm: true},
		{Name: "kind", Label: "KIND", Kind: view.KindSelect, InForm: true,
			Options: func(context.Context) ([]view.Option, error) {
				return []view.Option{{Label: "Router", Value: "router"}}, nil
			}},
	}

	t.Run("required", func(t *testing.T) {
		v, _ := view.NewValues(fields, url.Values{"name": {"  "}}, false)
		errs := view.Validate(t.Context(), fields, v)
		if !errs.Any() || len(errs["name"]) == 0 {
			t.Fatalf("Validate() = %v, want a required error on name", errs)
		}
	})

	t.Run("max length", func(t *testing.T) {
		v, _ := view.NewValues(fields, url.Values{"name": {"far-too-long"}}, false)
		errs := view.Validate(t.Context(), fields, v)
		if len(errs["name"]) == 0 {
			t.Fatalf("Validate() = %v, want a length error on name", errs)
		}
	})

	t.Run("number", func(t *testing.T) {
		v, _ := view.NewValues(fields, url.Values{"name": {"ok"}, "count": {"many"}}, false)
		errs := view.Validate(t.Context(), fields, v)
		if len(errs["count"]) == 0 {
			t.Fatalf("Validate() = %v, want a number error on count", errs)
		}
	})

	t.Run("select membership", func(t *testing.T) {
		v, _ := view.NewValues(fields, url.Values{"name": {"ok"}, "kind": {"switch"}}, false)
		errs := view.Validate(t.Context(), fields, v)
		if len(errs["kind"]) == 0 {
			t.Fatalf("Validate() = %v, want an option error on kind", errs)
		}
	})

	t.Run("valid input passes", func(t *testing.T) {
		v, _ := view.NewValues(fields, url.Values{"name": {"ok"}, "count": {"3"}, "kind": {"router"}}, false)
		if errs := view.Validate(t.Context(), fields, v); errs.Any() {
			t.Fatalf("Validate() = %v, want no errors", errs)
		}
	})

	// A failed option lookup is an outage, not the user's mistake, and
	// must not be reported as an invalid choice.
	t.Run("option lookup failure is not blamed on the user", func(t *testing.T) {
		broken := []view.Field{{Name: "kind", Label: "KIND", Kind: view.KindSelect, InForm: true,
			Options: func(context.Context) ([]view.Option, error) { return nil, errors.New("db down") }}}
		v, _ := view.NewValues(broken, url.Values{"kind": {"router"}}, false)
		errs := view.Validate(t.Context(), broken, v)
		if len(errs["kind"]) == 0 || !strings.Contains(errs["kind"][0], "could not be loaded") {
			t.Fatalf("Validate() = %v, want a load-failure message", errs)
		}
	})
}

func TestFieldErrors_SummaryFollowsDeclarationOrder(t *testing.T) {
	fields := testFields()
	errs := view.FieldErrors{}
	errs.Add("state", "state is wrong")
	errs.Add("name", "name is wrong")

	summary := errs.Summary(fields)
	if len(summary) != 2 {
		t.Fatalf("Summary() = %v, want 2 entries", summary)
	}
	// "name" is declared first, so it leads the summary regardless of the
	// order the errors were added in. A summary that reshuffles between
	// submissions is one a screen reader user re-reads from the top.
	if summary[0].Field != "name" {
		t.Errorf("Summary()[0].Field = %q, want name", summary[0].Field)
	}
	if summary[0].Label != "NAME" {
		t.Errorf("Summary()[0].Label = %q, want NAME", summary[0].Label)
	}
	if got := errs.Names(); len(got) != 2 || got[0] != "name" || got[1] != "state" {
		t.Errorf("Names() = %v, want sorted [name state]", got)
	}
}

func TestBind_ReadOnlyResourceHasNoWriteHandlers(t *testing.T) {
	h, err := view.Bind[device](fakeReader{}, nil, testProjector())
	if err != nil {
		t.Fatalf("Bind() = %v", err)
	}
	if h.Create != nil || h.Update != nil || h.Delete != nil {
		t.Error("Bind() with a nil Writer produced write handlers")
	}
	if h.Writable() {
		t.Error("Writable() = true for a read-only resource")
	}
}

func TestBind_ErasesThroughTheProjector(t *testing.T) {
	reader := fakeReader{items: []device{{name: "router-1", state: "active"}}}
	h := view.MustBind[device](reader, nil, testProjector())

	page, err := h.List(t.Context(), view.Query{})
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ID != "router-1" {
		t.Fatalf("List() = %+v, want one row identified router-1", page)
	}
	if page.Rows[0].Cells["state"] != "active" {
		t.Errorf("row cells = %v, want state=active", page.Rows[0].Cells)
	}
	if page.NextCursor != "next" {
		t.Errorf("NextCursor = %q, want next", page.NextCursor)
	}

	row, err := h.Get(t.Context(), "router-1")
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if row.ID != "router-1" {
		t.Errorf("Get().ID = %q, want router-1", row.ID)
	}
}

// A validation failure must return before the port is touched. A half
// written record is the failure the conformance suite asserts against, so
// the erasure layer is where it is prevented.
func TestBind_ValidationFailureNeverReachesThePort(t *testing.T) {
	writer := &fakeWriter{}
	projector := testProjector()
	projector.Bind = func(v view.Values) (device, view.FieldErrors) {
		errs := view.FieldErrors{}
		errs.Add("name", "always invalid")
		return device{}, errs
	}
	h := view.MustBind[device](fakeReader{}, writer, projector)

	values, _ := view.NewValues(testFields(), url.Values{"name": {"router-1"}}, false)

	id, errs, err := h.Create(t.Context(), values)
	if err != nil {
		t.Fatalf("Create() = %v, want nil (a validation failure is not an error)", err)
	}
	if !errs.Any() {
		t.Fatal("Create() reported no field errors")
	}
	if id != "" {
		t.Errorf("Create() = %q, want no id", id)
	}
	if len(writer.created) != 0 {
		t.Errorf("the port was called %d times despite a validation failure", len(writer.created))
	}

	if _, err := h.Update(t.Context(), "router-1", values); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	if len(writer.created) != 0 {
		t.Errorf("Update reached the port despite a validation failure")
	}
}

func TestBind_WritesReachThePort(t *testing.T) {
	writer := &fakeWriter{}
	h := view.MustBind[device](fakeReader{}, writer, testProjector())

	values, _ := view.NewValues(testFields(), url.Values{"name": {"router-9"}}, false)
	id, errs, err := h.Create(t.Context(), values)
	if err != nil || errs.Any() {
		t.Fatalf("Create() = (%q, %v, %v)", id, errs, err)
	}
	if id != "router-9" || len(writer.created) != 1 {
		t.Errorf("Create() did not reach the port: id=%q created=%v", id, writer.created)
	}
	if err := h.Delete(t.Context(), "router-9"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "router-9" {
		t.Errorf("Delete() did not reach the port: %v", writer.deleted)
	}
	if !h.Writable() {
		t.Error("Writable() = false for a resource with a Writer")
	}
}

func TestBind_RejectsIncompleteProjectors(t *testing.T) {
	cases := []struct {
		name    string
		reader  view.Reader[device]
		writer  view.Writer[device]
		project view.Projector[device]
		wantErr string
	}{
		{"no reader", nil, nil, testProjector(), "no Reader"},
		{"no row function", fakeReader{}, nil, view.Projector[device]{}, "no Row function"},
		{"writer without form", fakeReader{}, &fakeWriter{},
			view.Projector[device]{Row: testProjector().Row}, "no Form function"},
		{"writer without bind", fakeReader{}, &fakeWriter{},
			view.Projector[device]{Row: testProjector().Row, Form: testProjector().Form}, "no Bind function"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := view.Bind(tc.reader, tc.writer, tc.project)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Bind() = %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestMustBind_PanicsOnIncompleteProjector(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustBind() did not panic on an incomplete projector")
		}
	}()
	view.MustBind[device](fakeReader{}, nil, view.Projector[device]{})
}

func TestErrNotImplemented_IsDistinguishable(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), view.ErrNotImplemented)
	if !errors.Is(wrapped, view.ErrNotImplemented) {
		t.Error("ErrNotImplemented does not survive wrapping")
	}
}

// TestReferences_RenderNamesAndLinkToTheirTarget covers the mechanism that
// keeps a primary key off a page.
//
// The href is built from the referenced view's registered name and the
// stored target id, never from anything a projector put in the cell, which
// is the same argument ChartSpec.Data makes about author-supplied hrefs.
func TestReferences_RenderNamesAndLinkToTheirTarget(t *testing.T) {
	field := view.Field{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText,
		InList: true, References: "organizations"}
	plain := view.Field{Name: "name", Label: "NAME", Kind: view.KindText, InList: true}

	if !field.Referencing() || plain.Referencing() {
		t.Fatal("Referencing() does not distinguish a reference from a plain field")
	}

	model := view.ListModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: view.Descriptor{Name: "teams"},
	}
	row := view.Row{
		ID:    "3",
		Cells: view.Cells{"organization": "Network", "name": "netops"},
		Refs:  map[string]string{"organization": "7"},
	}

	if got, want := model.RefHref(row, field), "/ui/organizations/7"; got != want {
		t.Errorf("RefHref() = %q, want %q", got, want)
	}
	if got := model.RefHref(row, plain); got != "" {
		t.Errorf("a plain field produced the link %q", got)
	}
	if got := row.Ref("organization"); got != "7" {
		t.Errorf("Ref() = %q, want the target id", got)
	}
	if got := row.Ref("name"); got != "" {
		t.Errorf("Ref() on a non-reference = %q, want empty", got)
	}
	if got := (view.Row{}).Ref("organization"); got != "" {
		t.Errorf("Ref() on a row with no Refs map = %q, want empty", got)
	}
}

// TestReferences_NeverLinkWithoutText is the accessibility invariant.
//
// An anchor whose content is empty has no accessible name and is announced
// as its URL, which is worse than the plain cell it replaced. This is the
// last line of defence rather than the only one, but it is the one that
// holds when a projector forgets.
func TestReferences_NeverLinkWithoutText(t *testing.T) {
	field := view.Field{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText,
		InList: true, References: "organizations"}
	model := view.ListModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: view.Descriptor{Name: "teams"},
	}

	for name, row := range map[string]view.Row{
		"no label":    {ID: "3", Cells: view.Cells{"organization": ""}, Refs: map[string]string{"organization": "7"}},
		"blank label": {ID: "3", Cells: view.Cells{"organization": "   "}, Refs: map[string]string{"organization": "7"}},
		"no target":   {ID: "3", Cells: view.Cells{"organization": "Network"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := model.RefHref(row, field); got != "" {
				t.Errorf("RefHref() = %q, want no link", got)
			}
		})
	}
}

// TestCheckReferences_RefusesAReferenceToNothing.
//
// It cannot live in Register, because registration order is map iteration
// and a view may legitimately reference one that has not registered yet.
// Running it after everything is in turns a dangling reference into a
// startup refusal rather than a link that 404s when somebody clicks it.
func TestCheckReferences_RefusesAReferenceToNothing(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	// A view referencing one that exists.
	target := validDescriptor("reference-target")
	if err := view.Register(target); err != nil {
		t.Fatalf("registering the target: %v", err)
	}
	source := validDescriptor("reference-source")
	source.Fields = append(source.Fields, view.Field{
		Name: "target", Label: "TARGET", Kind: view.KindText, InList: true,
		References: target.Name,
	})
	if err := view.Register(source); err != nil {
		t.Fatalf("registering the source: %v", err)
	}
	if err := view.CheckReferences(); err != nil {
		t.Fatalf("CheckReferences() = %v, want nil for a resolvable reference", err)
	}

	// One referencing a view nobody registered.
	dangling := validDescriptor("reference-dangling")
	dangling.Fields = append(dangling.Fields, view.Field{
		Name: "target", Label: "TARGET", Kind: view.KindText, InList: true,
		References: "no-such-view",
	})
	if err := view.Register(dangling); err != nil {
		t.Fatalf("registering the dangling source: %v", err)
	}
	err := view.CheckReferences()
	if err == nil {
		t.Fatal("CheckReferences() = nil for a reference to an unregistered view")
	}
	if !strings.Contains(err.Error(), "no-such-view") {
		t.Errorf("CheckReferences() = %q, want it to name the missing view", err)
	}
}

// TestResolveFormFields_WithNoFieldsForIsJustTheStaticSet covers the
// answer every view without a per-record dynamic field set gets: exactly
// FormFieldsFor's own answer, in both modes, unchanged.
func TestResolveFormFields_WithNoFieldsForIsJustTheStaticSet(t *testing.T) {
	d := validDescriptor("resolve-fields-static")

	create, err := d.ResolveFormFields(t.Context(), view.Resolve{})
	if err != nil {
		t.Fatalf("ResolveFormFields(create) = %v", err)
	}
	if len(create) != len(d.FormFieldsFor(false)) {
		t.Errorf("ResolveFormFields(create) returned %d fields, want the same as FormFieldsFor(false)", len(create))
	}

	edit, err := d.ResolveFormFields(t.Context(), view.Resolve{ID: "router-1"})
	if err != nil {
		t.Fatalf("ResolveFormFields(edit) = %v", err)
	}
	if len(edit) != len(d.FormFieldsFor(true)) {
		t.Errorf("ResolveFormFields(edit) returned %d fields, want the same as FormFieldsFor(true)", len(edit))
	}
}

// TestResolveFormFields_MergesFieldsForInBothModes covers the seam after it
// was widened for Credentials.
//
// It used to assert the opposite of half of this: that FieldsFor was never
// consulted for a create, because a record that does not exist yet has
// nothing to resolve a per-record field set from. That reasoning only ever
// held for a view whose dynamic fields are declared by something the STORED
// record names, which is how Templates uses it. A credential's fields are
// declared by the type being chosen in the form, so the thing driving the
// resolution is in the submission rather than in the database, and it is
// there on a create exactly as much as on an edit.
func TestResolveFormFields_MergesFieldsForInBothModes(t *testing.T) {
	extra := view.Field{Name: "forks", Label: "FORKS", Kind: view.KindNumber, InForm: true}
	var calls []view.Resolve
	d := validDescriptor("resolve-fields-dynamic")
	d.FieldsFor = func(_ context.Context, r view.Resolve) ([]view.Field, error) {
		calls = append(calls, r)
		return []view.Field{extra}, nil
	}

	create, err := d.ResolveFormFields(t.Context(), view.Resolve{})
	if err != nil {
		t.Fatalf("ResolveFormFields(create) = %v", err)
	}
	if want := len(d.FormFieldsFor(false)) + 1; len(create) != want {
		t.Errorf("ResolveFormFields(create) = %d fields, want %d (the static create set plus FieldsFor's one)", len(create), want)
	}
	if len(calls) != 1 {
		t.Fatalf("FieldsFor called %d times for a create, want exactly once", len(calls))
	}
	if calls[0].Editing() {
		t.Errorf("FieldsFor saw Editing() true on a create, want false with an empty ID")
	}

	edit, err := d.ResolveFormFields(t.Context(), view.Resolve{ID: "router-1"})
	if err != nil {
		t.Fatalf("ResolveFormFields(edit) = %v", err)
	}
	if len(calls) != 2 || calls[1].ID != "router-1" {
		t.Fatalf("FieldsFor calls = %+v, want a second one carrying the record id", calls)
	}
	if !calls[1].Editing() {
		t.Errorf("FieldsFor saw Editing() false for record %q, want true", calls[1].ID)
	}
	wantLen := len(d.FormFieldsFor(true)) + 1
	if len(edit) != wantLen {
		t.Fatalf("ResolveFormFields(edit) = %d fields, want %d (the static edit set plus FieldsFor's one)", len(edit), wantLen)
	}
	if edit[len(edit)-1].Name != "forks" {
		t.Errorf("ResolveFormFields(edit) appended %q last, want the FieldsFor field", edit[len(edit)-1].Name)
	}
}

// TestResolveFormFields_PropagatesAFieldsForError proves a resolution
// failure is reported rather than silently rendering the static set alone,
// which would make a broken per-record field source indistinguishable from
// one with nothing extra to add.
func TestResolveFormFields_PropagatesAFieldsForError(t *testing.T) {
	wantErr := errors.New("could not resolve the record's own fields")
	d := validDescriptor("resolve-fields-error")
	d.FieldsFor = func(context.Context, view.Resolve) ([]view.Field, error) { return nil, wantErr }

	if _, err := d.ResolveFormFields(t.Context(), view.Resolve{ID: "router-1"}); !errors.Is(err, wantErr) {
		t.Errorf("ResolveFormFields(edit) = %v, want it to propagate FieldsFor's own error", err)
	}
}
