package view_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers the create-but-never-write erasure and the model
// accessors that are pure projections. Both are small, and both are the kind
// of code that is only ever wrong in one direction: silently producing
// handlers that a template then renders controls for.

// widget is a resource domain type for the binding tests.
type widget struct {
	Name string
}

// widgetReader is a Reader over a fixed slice.
type widgetReader struct{ items []widget }

func (r widgetReader) List(context.Context, view.Query) (view.Page[widget], error) {
	return view.Page[widget]{Items: r.items}, nil
}

func (r widgetReader) Get(_ context.Context, id string) (widget, error) {
	for _, w := range r.items {
		if w.Name == id {
			return w, nil
		}
	}
	return widget{}, errors.New("not found")
}

// widgetCreator records what it was asked to create and can be made to fail
// with a field-blaming error.
type widgetCreator struct {
	created []widget
	err     error
}

func (c *widgetCreator) Create(_ context.Context, w widget) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	c.created = append(c.created, w)
	return w.Name, nil
}

// widgetProjector is the domain-to-presentation mapping.
func widgetProjector() view.Projector[widget] {
	return view.Projector[widget]{
		Row: func(w widget) view.Row {
			return view.Row{ID: w.Name, Cells: view.Cells{"name": w.Name}}
		},
		Form: func(w widget) map[string]string {
			return map[string]string{"name": w.Name}
		},
		Bind: func(v view.Values) (widget, view.FieldErrors) {
			name := strings.TrimSpace(v.Get("name"))
			if name == "" {
				errs := view.FieldErrors{}
				errs.Add("name", "A name is required.")
				return widget{}, errs
			}
			return widget{Name: name}, nil
		},
	}
}

// bindFields are the declared fields the Values wrapper narrows against.
var bindFields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InForm: true, Required: true},
}

// newValues builds a narrowed Values from a plain map, the same way the web
// handler does from a submitted form.
func newValues(t *testing.T, raw map[string]string) view.Values {
	t.Helper()
	form := map[string][]string{}
	for k, v := range raw {
		form[k] = []string{v}
	}
	values, undeclared := view.NewValues(bindFields, form, false)
	if len(undeclared) > 0 {
		t.Fatalf("test supplied undeclared keys %v", undeclared)
	}
	return values
}

// TestBindCreatable_ProducesCreateWithoutUpdateOrDelete is the whole point of
// the third constructor. A resource that can be created but never edited
// (a job, dispatched and then immutable) must not report Writable, because
// that one boolean is what makes the templates render an edit link and a
// delete control.
func TestBindCreatable_ProducesCreateWithoutUpdateOrDelete(t *testing.T) {
	creator := &widgetCreator{}
	h, err := view.BindCreatable[widget](widgetReader{}, creator, widgetProjector())
	if err != nil {
		t.Fatalf("BindCreatable: %v", err)
	}

	if h.Create == nil {
		t.Fatal("Create handler is nil")
	}
	if h.Update != nil || h.Delete != nil {
		t.Error("BindCreatable produced Update or Delete handlers")
	}
	if h.Writable() {
		t.Error("Writable() = true, so a template would render edit and delete controls")
	}

	id, errs, err := h.Create(context.Background(), newValues(t, map[string]string{"name": "alpha"}))
	if err != nil || errs.Any() {
		t.Fatalf("Create: err = %v, errs = %v", err, errs)
	}
	if id != "alpha" {
		t.Errorf("id = %q, want alpha", id)
	}
	if len(creator.created) != 1 {
		t.Fatalf("creator saw %d writes, want 1", len(creator.created))
	}
}

// TestBindCreatable_ValidationFailureNeverReachesThePort is the invariant
// every write path in this package shares: a rejected submission must not
// leave a half-written record behind.
func TestBindCreatable_ValidationFailureNeverReachesThePort(t *testing.T) {
	creator := &widgetCreator{}
	h, err := view.BindCreatable[widget](widgetReader{}, creator, widgetProjector())
	if err != nil {
		t.Fatalf("BindCreatable: %v", err)
	}

	_, errs, err := h.Create(context.Background(), newValues(t, map[string]string{"name": "  "}))
	if err != nil {
		t.Fatalf("Create returned a hard error for a validation failure: %v", err)
	}
	if !errs.Any() {
		t.Fatal("a blank name was accepted")
	}
	if len(creator.created) != 0 {
		t.Error("a rejected submission still reached the port")
	}
}

// TestBindCreatable_FieldFaultBecomesAFieldError proves a port that blames a
// declared field gets its message rendered beside that control rather than as
// a page-level failure. The alternative is a form that says "something went
// wrong" while the offending input sits unmarked.
func TestBindCreatable_FieldFaultBecomesAFieldError(t *testing.T) {
	creator := &widgetCreator{err: view.FieldFault{Field: "name", Message: "That name is taken."}}
	h, err := view.BindCreatable[widget](widgetReader{}, creator, widgetProjector())
	if err != nil {
		t.Fatalf("BindCreatable: %v", err)
	}

	_, errs, err := h.Create(context.Background(), newValues(t, map[string]string{"name": "alpha"}))
	if err != nil {
		t.Fatalf("a field fault surfaced as a hard error: %v", err)
	}
	if got := errs["name"]; len(got) != 1 || got[0] != "That name is taken." {
		t.Errorf("field errors = %v, want the fault's message on name", errs)
	}
}

// TestBindCreatable_RefusesAnIncompleteProjector covers the constructor's own
// guards. Each one turns a nil dereference three clicks into the UI into a
// refusal at process start.
func TestBindCreatable_RefusesAnIncompleteProjector(t *testing.T) {
	full := widgetProjector()

	for _, tc := range []struct {
		name    string
		creator view.Creator[widget]
		proj    view.Projector[widget]
		want    string
	}{
		{"no creator", nil, full, "no Creator"},
		{"no bind", &widgetCreator{}, view.Projector[widget]{Row: full.Row, Form: full.Form}, "no Bind"},
		{"no row", &widgetCreator{}, view.Projector[widget]{Form: full.Form, Bind: full.Bind}, "no Row"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := view.BindCreatable[widget](widgetReader{}, tc.creator, tc.proj)
			if err == nil {
				t.Fatal("an incomplete projector was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestMustBindCreatable_PanicsRatherThanReturningNil mirrors MustRegister:
// a composition root that got this wrong must not start.
func TestMustBindCreatable_PanicsRatherThanReturningNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustBindCreatable returned instead of panicking on a nil Creator")
		}
	}()
	view.MustBindCreatable[widget](widgetReader{}, nil, widgetProjector())
}

func TestFieldFault_ErrorNamesTheFieldAndTheMessage(t *testing.T) {
	err := view.FieldFault{Field: "name", Message: "That name is taken."}
	if got := err.Error(); got != "name: That name is taken." {
		t.Errorf("Error() = %q", got)
	}
}

// TestPageModel_Accessors covers the small projections the layout template
// reads. They are trivial, and that is exactly why they are worth pinning:
// every one of them builds a URL or a document title that no other test
// would notice going wrong.
func TestPageModel_Accessors(t *testing.T) {
	p := view.PageModel{Prefix: "/ui", Title: "Widgets"}

	// Most specific part first, so a truncated browser tab still shows the
	// useful half.
	if want := "Widgets // The Pleiades"; p.DocumentTitle() != want {
		t.Errorf("DocumentTitle() = %q, want %q", p.DocumentTitle(), want)
	}
	if got := (view.PageModel{}).DocumentTitle(); got != "Pleiades" {
		t.Errorf("DocumentTitle() with no title = %q, want The Pleiades", got)
	}
	if want := "/ui/static/app.abc123.css"; p.AssetPath("app.abc123.css") != want {
		t.Errorf("AssetPath() = %q, want %q", p.AssetPath("app.abc123.css"), want)
	}
	for name, got := range map[string]string{
		"/ui/theme":  p.ThemeAction(),
		"/ui/skin":   p.SkinAction(),
		"/ui/a11y":   p.A11yAction(),
		"/ui/logout": p.LogoutAction(),
	} {
		if got != name {
			t.Errorf("action href = %q, want %q", got, name)
		}
	}
	// No token means an empty object rather than a broken attribute, so a
	// page rendered before a session exists still parses.
	if got := (view.PageModel{}).CSRFHeaders(); got != "{}" {
		t.Errorf("CSRFHeaders() with no token = %q, want {}", got)
	}
}

// TestListModel_Projections covers the accessors a list template reads on
// every row.
func TestListModel_Projections(t *testing.T) {
	m := listModel()
	m.Chart = view.ChartData{Buckets: []view.ChartBucket{
		{Label: "Completed", Count: 3, Class: "badge-ok"},
		{Label: "Failed", Count: 1, Class: "badge-failed"},
	}}

	if cols := m.Columns(); len(cols) != 2 {
		t.Errorf("Columns() returned %d, want the 2 marked InList", len(cols))
	}
	if got := m.Cell(m.Rows[0], m.Columns()[0]); got != "alpha" {
		t.Errorf("Cell = %q, want alpha", got)
	}
	if got := m.IsPrimary(m.Columns()[0]); got != "false" {
		t.Errorf("IsPrimary with no MobilePrimary = %q, want false", got)
	}
	if want := "/ui/widgets/new"; m.CreateHref() != want {
		t.Errorf("CreateHref() = %q, want %q", m.CreateHref(), want)
	}
	// Announced to a screen reader after an HTMX swap, so the count has to
	// be the rendered count rather than a page size.
	if want := "2 results"; m.ResultCount() != want {
		t.Errorf("ResultCount() = %q, want %q", m.ResultCount(), want)
	}
	if got := m.ChartCount(m.Chart.Buckets[0]); got != "3" {
		t.Errorf("ChartCount() = %q, want 3", got)
	}
	if got := m.ChartTotal(); got != "4" {
		t.Errorf("ChartTotal() = %q, want 4", got)
	}
	if got := m.ChartBucketClass(view.ChartBucket{Class: "invented"}); got != "badge-neutral" {
		t.Errorf("ChartBucketClass for an unvalidated class = %q, want badge-neutral", got)
	}
	if got := m.ChartBucketClass(m.Chart.Buckets[0]); got != "badge-ok" {
		t.Errorf("ChartBucketClass = %q, want badge-ok", got)
	}
}
