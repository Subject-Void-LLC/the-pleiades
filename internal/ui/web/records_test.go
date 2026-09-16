package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers the record-level handlers: detail, the forms, the write
// paths, the chart endpoint, the stream page, and the record actions. The
// conformance suite in internal/ui/resources drives the same handlers against
// every real resource; what it cannot reach is a resource shaped to exercise
// all of them at once, which is what the views below are for.

// gadgetView carries every optional part of a descriptor at once: sections,
// a record action, a chart and a stream. One view proving they compose is
// worth more than four proving they exist.
const gadgetView = "gadgets"

// declaredView is registered without handlers, so the declared panel has
// something to render.
const declaredView = "sprockets"

// sectionErr is what the failing section returns, so the test can assert the
// page survives a section that could not load.
var sectionErr = errors.New("deliberate section failure")

// actionCalls records what runAction handed the action's Submit function.
var actionCalls struct {
	sync.Mutex
	id    string
	group string
}

// rowActionCalls records what runRowAction handed the row action's Submit,
// which is the only way to prove BOTH ids arrived rather than one of them.
var rowActionCalls struct {
	sync.Mutex
	parentID string
	rowID    string
	label    string
}

// pinnedRow is the row the gadget view's row action withholds itself from,
// so the per-row Applies gate is visible in the rendering rather than only
// in a unit test of the resolver.
const pinnedRow = "pinned"

// failingRow is the row whose Submit returns an error, so the handler's
// failure branch has something to reach.
const failingRow = "boom"

// refusedRow is the row whose Submit refuses for a reason the operator can
// act on, which the handler must answer differently from failingRow.
const refusedRow = "depended-on"

// refusalText is the store's own sentence, which the refusal page shows and
// the fault page must not.
const refusalText = "an injector depends on this input"

var registerRecordViews = sync.OnceFunc(func() {
	view.MustRegister(view.Descriptor{
		Name:     gadgetView,
		Title:    "Gadgets",
		NavLabel: "GADGETS",
		NavOrder: 20,
		Summary:  "A view carrying every optional descriptor part at once.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		// A refresh whose Active predicate treats the record named
		// "done" as terminal, so one view exercises both the polling
		// and the stopping halves.
		Refresh: &view.RefreshSpec{
			Interval: 5 * time.Second,
			Active:   func(row view.Row) bool { return row.Cells["name"] != "done" },
		},
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, Required: true,
				MaxLen: 64, Autocomplete: "off", InList: true, InForm: true, MobilePrimary: true},
		},
		// A per-record edit field, exactly the seam Templates
		// (internal/ui/resources/templates) is built on: the record named
		// "broken" cannot resolve its own fields at all, and every other
		// one gets one extra control alongside the static "name" field.
		// This is Descriptor.FieldsFor, not the "vary" action's own
		// per-action FieldsFor above -- the two are deliberately
		// independent seams, and this view exercises both.
		FieldsFor: func(_ context.Context, r view.Resolve) ([]view.Field, error) {
			if r.ID == "broken" {
				return nil, errors.New("deliberate record-field resolution failure")
			}
			return []view.Field{
				{Name: "extra", Label: "EXTRA", Kind: view.KindText, Autocomplete: "off", InForm: true},
			}, nil
		},
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
		Sections: []view.Section{
			{
				Status:  view.StatusImplemented,
				Title:   "Related records",
				Summary: "Rows loaded from another port.",
				Fields:  []view.Field{{Name: "label", Label: "LABEL", Kind: view.KindText, InList: true}},
				Empty:   "Nothing related yet.",
				Rows: func(_ context.Context, parentID string) ([]view.Row, error) {
					// The parent id is echoed so the test can prove it
					// reached the section rather than being dropped.
					return []view.Row{
						{ID: "r1", Cells: view.Cells{"label": "parent=" + parentID}},
						{ID: pinnedRow, Cells: view.Cells{"label": "withheld"}},
						{ID: failingRow, Cells: view.Cells{"label": "fails"}},
						{ID: refusedRow, Cells: view.Cells{"label": "refused"}},
					}, nil
				},
				// The row half of the section write path. Its endpoint is
				// reached from nowhere else in this descriptor, which is
				// the case worth covering: a relation the affordance
				// generator is never asked about is never permitted, and
				// the control is then withheld from everybody silently.
				RowActions: []view.RowAction{{
					Name:     "detach",
					Label:    "Detach",
					Endpoint: &apispec.SetCredentialTypeInputs,
					Confirm:  "This removes the row. It cannot be undone from here.",
					Applies:  func(row view.Row, _ view.RowPosition) bool { return row.ID != pinnedRow },
					Submit: func(_ context.Context, parentID, rowID string, _ view.Values) (string, view.FieldErrors, error) {
						if rowID == failingRow {
							return "", nil, errors.New("deliberate row action failure")
						}
						if rowID == refusedRow {
							return "", nil, view.Refuse(errors.New(refusalText))
						}
						rowActionCalls.Lock()
						rowActionCalls.parentID, rowActionCalls.rowID = parentID, rowID
						rowActionCalls.Unlock()
						return "", nil, nil
					},
				}, {
					// A PROMPTING row control, which is the other half of
					// the row path: it draws a form prefilled from the row
					// and submits back to the same address.
					Name:     "relabel",
					Label:    "Relabel",
					Heading:  "Relabel row",
					Endpoint: &apispec.SetCredentialTypeInjectors,
					Fields: []view.Field{
						{Name: "label", Label: "LABEL", Kind: view.KindText,
							Required: true, MaxLen: 12, Autocomplete: "off", InForm: true},
						// A select, so the prompt's option resolution runs
						// on this path too: a row form fetches its choices
						// before rendering, exactly as a record action's
						// does, and no template performs I/O.
						{Name: "tier", Label: "TIER", Kind: view.KindSelect, InForm: true,
							Options: func(context.Context) ([]view.Option, error) {
								return []view.Option{{Label: "Primary", Value: "primary"}}, nil
							}},
					},
					Form: func(_ context.Context, _, rowID string) (map[string]string, error) {
						if rowID == failingRow {
							return nil, errors.New("deliberate row prefill failure")
						}
						return map[string]string{"label": "stored-" + rowID}, nil
					},
					Submit: func(_ context.Context, parentID, rowID string, v view.Values) (string, view.FieldErrors, error) {
						if v.Get("label") == "taken" {
							errs := view.FieldErrors{}
							errs.Add("label", "That label is already used.")
							return "", errs, nil
						}
						rowActionCalls.Lock()
						rowActionCalls.parentID, rowActionCalls.rowID = parentID, rowID
						rowActionCalls.label = v.Get("label")
						rowActionCalls.Unlock()
						return "", nil, nil
					},
				}},
			},
			{
				Status: view.StatusImplemented,
				Title:  "Broken section",
				Fields: []view.Field{{Name: "label", Label: "LABEL", Kind: view.KindText, InList: true}},
				Empty:  "Could not be loaded.",
				Rows: func(context.Context, string) ([]view.Row, error) {
					return nil, sectionErr
				},
			},
		},
		Actions: []view.RecordAction{{
			Name:     "run",
			Label:    "Run",
			Heading:  "Run gadget",
			Endpoint: &apispec.LaunchTemplate,
			Fields: []view.Field{
				{Name: "group", Label: "TARGET GROUP", Kind: view.KindText,
					Required: true, MaxLen: 64, Autocomplete: "off", InForm: true},
			},
			Submit: func(_ context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
				if v.Get("group") == "forbidden" {
					errs := view.FieldErrors{}
					errs.Add("group", "That group cannot be targeted.")
					return "", errs, nil
				}
				actionCalls.Lock()
				actionCalls.id, actionCalls.group = id, v.Get("group")
				actionCalls.Unlock()
				return "", nil, nil
			},
		}, {
			// An action whose prompt is PREFILLED from the record, which
			// is the shape a form that replaces something the record
			// already holds has to have. Without it the form renders
			// empty and saving it writes the blanks over what was stored.
			Name:     "rebind",
			Label:    "Rebind",
			Heading:  "Rebind gadget",
			Endpoint: &apispec.SetTemplateCredentials,
			Fields: []view.Field{
				{Name: "bound", Label: "BOUND", Kind: view.KindText, Autocomplete: "off", InForm: true},
			},
			Form: func(_ context.Context, id string) (map[string]string, error) {
				if id == "broken" {
					return nil, errors.New("deliberate prefill failure")
				}
				if id == "leaky" {
					// A key the form does not render, which must be
					// refused rather than silently dropped.
					return map[string]string{"bound": "current", "gone": "x"}, nil
				}
				return map[string]string{"bound": "current-" + id}, nil
			},
			Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
				return "", nil, nil
			},
		}, {
			// A second action whose prompt is resolved from the record,
			// which is what a launch form needs: the record named "alpha"
			// opens one control, and the one named "done" fails to resolve
			// at all. The failing branch is the point -- an empty form and
			// a form that could not be built must not look the same when
			// the button underneath runs production work.
			Name:  "vary",
			Label: "Vary",
			// A different endpoint from the action above, because the two
			// would otherwise share a relation and a view refuses that: a
			// caller could not tell the resulting affordances apart.
			Heading:  "Vary gadget",
			Endpoint: &apispec.CopyTemplate,
			FieldsFor: func(_ context.Context, id string) ([]view.Field, error) {
				if id == "done" {
					return nil, errors.New("deliberate field-resolution failure")
				}
				return []view.Field{
					{Name: "limit", Label: "LIMIT", Kind: view.KindText, Autocomplete: "off", InForm: true},
				}, nil
			},
			Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
				return "", nil, nil
			},
		}},
		Chart: &view.ChartSpec{
			Title:   "Gadget outcomes",
			Caption: "Counts by state, every state listed even when zero.",
			Data: func(context.Context) (view.ChartData, error) {
				return view.ChartData{Buckets: []view.ChartBucket{
					{Label: "Completed", Count: 2, Class: "badge-ok"},
				}}, nil
			},
		},
		Stream: &view.StreamSpec{
			Title:       "Live output",
			PathPattern: "/api/v1/jobs/{id}/logs",
		},
		// One download, gated per record, so the route has both a record
		// that has something to give and one that does not. It is the
		// first route in this application to serve a body that is neither
		// HTML nor JSON.
		Downloads: []view.DownloadSpec{{
			Name:        "report",
			Label:       "Report (CSV)",
			Summary:     "What this gadget did.",
			ContentType: "text/csv; charset=utf-8",
			Filename:    "report-{id}.csv",
			Available:   func(_ context.Context, id string) bool { return id != "done" },
			Write: func(_ context.Context, w io.Writer, id string) error {
				_, err := io.WriteString(w, "id\n"+id+"\n")
				return err
			},
		}},
		Handlers: view.MustBind[string](probeReader{}, probeWriter{}, view.Projector[string]{
			Row:  func(s string) view.Row { return view.Row{ID: s, Cells: view.Cells{"name": s}} },
			Form: func(s string) map[string]string { return map[string]string{"name": s} },
			Bind: func(v view.Values) (string, view.FieldErrors) { return v.Get("name"), view.FieldErrors{} },
		}),
	})

	view.MustRegister(view.Descriptor{
		Name:     declaredView,
		Title:    "Sprockets",
		NavLabel: "SPROCKETS",
		NavOrder: 30,
		Summary:  "Declared, so the honest panel has something to render.",
		Status:   view.StatusDeclared,
		IDField:  "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
		},
	})
})

// newRecordProbe is newProbe with the record views registered.
func newRecordProbe(t *testing.T) *probe {
	t.Helper()
	registerRecordViews()
	return newProbe(t, allowAll{}, permitEverything{})
}

// get issues an authenticated GET and returns the recorder.
func (p *probe) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	return p.serve(p.authed(httptest.NewRequest(http.MethodGet, target, nil)))
}

// post issues an authenticated, CSRF-carrying form POST.
func (p *probe) post(t *testing.T, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set(session_CSRFHeader, p.csrf(t))
	return p.serve(p.authed(r))
}

// session_CSRFHeader is the header name the CSRF middleware reads. Spelled
// out rather than imported so this file's imports stay about the handlers.
const session_CSRFHeader = "X-CSRF-Token"

// TestDetail_RendersFieldsAndSections proves the record page composes the
// record's own fields with its related tables, and that a section receives
// the parent identifier it was opened for.
//
// A section is reachable at its own tab rather than stacked under the
// record's fields, so this asserts both halves of that: the tab is offered on
// the record page, and following it renders the section. Asserting only the
// first would pass for a tab strip that links nowhere, which is the failure
// this arrangement could plausibly have.
func TestDetail_RendersFieldsAndSections(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tab=related-records") {
		t.Fatalf("the record page offers no tab for its section: %s", rec.Body.String())
	}

	rec = p.get(t, "/ui/"+gadgetView+"/alpha?tab=related-records")
	if rec.Code != http.StatusOK {
		t.Fatalf("section tab status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if !strings.Contains(body, "Related records") {
		t.Error("the section heading did not render")
	}
	// The parent id has to reach the section, or a related table would show
	// every record's children on every record's page.
	if !strings.Contains(body, "parent=alpha") {
		t.Error("the section did not receive the parent identifier")
	}
}

// TestDetail_UnknownTabFallsBackToTheRecord proves a tab slug nobody
// recognises renders the record rather than an empty page.
//
// The value arrives in a query parameter, which means it arrives from
// whatever somebody pasted into an address bar or from a link made against a
// section that has since been renamed. Neither should produce a blank page.
func TestDetail_UnknownTabFallsBackToTheRecord(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha?tab=no-such-section")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "detail-list") {
		t.Error("an unrecognised tab did not fall back to the record's own fields")
	}
}

// TestDetail_SurvivesASectionThatCannotLoad is the deliberate degradation.
// The record's own fields are the answer to "what is this", and losing them
// because a related table could not be read would turn a partial outage into
// a total one, on the page somebody opened precisely because something is
// already wrong.
func TestDetail_SurvivesASectionThatCannotLoad(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha?tab=broken-section")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 despite a failing section", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Could not be loaded.") {
		t.Error("the failing section did not render its empty state")
	}
	// The port's own error text must not reach the page.
	if strings.Contains(body, sectionErr.Error()) {
		t.Error("the section's error text leaked into the response")
	}
	// A failing section must not take the rest of the record's navigation
	// with it: the healthy section is still offered as a tab beside it.
	if !strings.Contains(body, "tab=related-records") {
		t.Error("one failing section suppressed a healthy one")
	}
}

// TestDetail_OffersTheRecordAction is the assertion the whole record-action
// feature rests on. A declared action that never renders is a feature nobody
// can reach, and nothing else in the suite would notice.
func TestDetail_OffersTheRecordAction(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/ui/"+gadgetView+"/alpha/run") {
		t.Error("the record action link did not render on the detail page")
	}
}

func TestDetail_UnknownRecordIsNotFound(t *testing.T) {
	p := newRecordProbe(t)

	if rec := p.get(t, "/ui/nosuchview/alpha"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unregistered resource", rec.Code)
	}
}

// TestDeclaredView_RendersTheHonestPanel proves a declared view needs no
// per-view work: the shared template renders the panel from Status alone,
// and the view does not secretly work.
func TestDeclaredView_RendersTheHonestPanel(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+declaredView)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sprockets") {
		t.Error("the declared panel did not name the view")
	}
	// A declared view must not render a create control, because there is
	// nothing behind it.
	if strings.Contains(body, "/ui/"+declaredView+"/new") {
		t.Error("a declared view offered a create link")
	}
}

func TestForms_RenderForCreateAndEdit(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("create", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/new")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "New Gadgets") {
			t.Error("the create form did not render its heading")
		}
	})

	t.Run("edit prefills from storage", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/alpha/edit")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Edit Gadgets") {
			t.Error("the edit form did not render its heading")
		}
		// A form that did not prefill would silently blank every field the
		// user did not retype.
		if !strings.Contains(body, `value="alpha"`) {
			t.Error("the edit form did not prefill the stored value")
		}
	})

	t.Run("edit renders the record's own dynamic fields", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/alpha/edit")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `name="extra"`) {
			t.Error("the edit form does not render the control Descriptor.FieldsFor resolved for this record")
		}
	})

	// This asserted the opposite until the seam was widened for
	// Credentials, whose fields are declared by the type being chosen in
	// the form rather than by anything a stored record names. A view that
	// only wants per-record fields, like Templates, gets the old behaviour
	// by reading Resolve.ID and returning nothing when it is empty; that is
	// a decision for the resolver now, not a rule the caller enforces.
	t.Run("create resolves the dynamic fields too, with an empty id", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/new")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `name="extra"`) {
			t.Error("the create form does not render the control FieldsFor resolved for an empty id")
		}
	})

	// The no-JavaScript half of a dependent form: a driving control's value
	// arrives on the query string, and the form has to come back with that
	// control still holding it or the choice has to be made twice.
	t.Run("a create form prefills a declared field from the query string", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/new?name=seeded-by-query")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "seeded-by-query") {
			t.Error("the create form dropped a declared field's value from the query string")
		}
	})

	t.Run("a FieldsFor failure is a server error, not a blank form", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/broken/edit")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 when the record's own fields cannot be resolved", rec.Code)
		}
	})
}

func TestWrites_UpdateAndDeleteRedirect(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("update", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha", "name=renamed")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update accepts the record's own dynamic field", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha", "name=alpha&extra=set")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update reports a FieldsFor failure as a server error", func(t *testing.T) {
		// update resolves the record's own fields independently of
		// editForm, before it can even parse the submission against them,
		// so this is its own code path to the same failure, not a repeat
		// of the GET-side assertion above.
		rec := p.post(t, "/ui/"+gadgetView+"/broken", "name=broken")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 when the record's own fields cannot be resolved", rec.Code)
		}
	})

	// A browser form supports GET and POST only, so delete arrives as a POST
	// carrying an override rather than as a DELETE.
	t.Run("delete via the method override", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha", "_method=DELETE")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "/ui/"+gadgetView {
			t.Errorf("Location = %q, want the collection", loc)
		}
	})

	t.Run("delete via the real method", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/ui/"+gadgetView+"/alpha", nil)
		r.Header.Set(session_CSRFHeader, p.csrf(t))
		if rec := p.serve(p.authed(r)); rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
	})
}

// TestChartData_ReturnsDomainJSON pins the contract that keeps the endpoint
// free of a charting library's schema: buckets with labels and counts, never
// an ECharts option document.
func TestChartData_ReturnsDomainJSON(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/chart.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`"buckets"`, `"label"`, `"count"`, "Completed"} {
		if !strings.Contains(body, want) {
			t.Errorf("chart JSON missing %s: %s", want, body)
		}
	}
}

// TestChartData_IsRefusedForAViewWithoutAChart proves the route resolves per
// resource rather than existing globally, so a view that declares no chart
// answers 404 rather than an empty document.
func TestChartData_IsRefusedForAViewWithoutAChart(t *testing.T) {
	p := newRecordProbe(t)

	if rec := p.get(t, "/ui/"+testView+"/chart.json"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a chartless view", rec.Code)
	}
}

// TestStream_RendersOnlyWhereDeclared covers the same per-resource
// resolution for the log page.
func TestStream_RendersOnlyWhereDeclared(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha/logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// The page must point at the declared, same-origin stream path, which is
	// what lets connect-src stay at 'self' and the session cookie reach it.
	if !strings.Contains(rec.Body.String(), "/api/v1/jobs/alpha/logs") {
		t.Error("the stream page did not carry the resolved stream URL")
	}

	if rec := p.get(t, "/ui/"+testView+"/alpha/logs"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a view declaring no stream", rec.Code)
	}
}

// TestActionForm_PromptsBeforeRunning is the safety property of the GET half
// of a record action: opening the link must ask a question, never perform the
// operation.
func TestActionForm_PromptsBeforeRunning(t *testing.T) {
	p := newRecordProbe(t)

	// actionCalls is a package-level spy, so what this test asserts is a
	// PRECONDITION -- that nothing had run at the moment of the GET below --
	// and a precondition has to be established here rather than tidied up by
	// whoever wrote the last test to touch it. TestRunAction_SubmitsAndRedirects
	// clears it on entry too and leaves "alpha" behind on exit, which is
	// invisible in source order (it is declared after this test) and shows up
	// only on the second iteration of -count>1, where iteration one's write is
	// still sitting here.
	actionCalls.Lock()
	actionCalls.id, actionCalls.group = "", ""
	actionCalls.Unlock()

	rec := p.get(t, "/ui/"+gadgetView+"/alpha/run")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Run gadget") {
		t.Error("the action form did not render its heading")
	}
	if !strings.Contains(body, "TARGET GROUP") {
		t.Error("the action form did not render its prompted field")
	}

	actionCalls.Lock()
	ran := actionCalls.id != ""
	actionCalls.Unlock()
	if ran {
		t.Error("opening the action form performed the action")
	}
}

func TestActionForm_UnknownActionIsNotFound(t *testing.T) {
	p := newRecordProbe(t)

	if rec := p.get(t, "/ui/"+gadgetView+"/alpha/nosuchaction"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an undeclared action", rec.Code)
	}
}

// TestRunAction_SubmitsAndRedirects covers the POST half: the record id and
// the narrowed values reach Submit, and the caller is redirected rather than
// left on a form that would re-run on refresh.
func TestRunAction_SubmitsAndRedirects(t *testing.T) {
	p := newRecordProbe(t)

	actionCalls.Lock()
	actionCalls.id, actionCalls.group = "", ""
	actionCalls.Unlock()

	rec := p.post(t, "/ui/"+gadgetView+"/alpha/run", "group=routers")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	actionCalls.Lock()
	gotID, gotGroup := actionCalls.id, actionCalls.group
	actionCalls.Unlock()
	if gotID != "alpha" {
		t.Errorf("Submit saw id %q, want alpha", gotID)
	}
	if gotGroup != "routers" {
		t.Errorf("Submit saw group %q, want routers", gotGroup)
	}
}

// TestRunAction_ValidationFailureIs422WithTheFieldMarked proves an action
// form redisplays like every other form rather than losing what was typed.
func TestRunAction_ValidationFailureIs422WithTheFieldMarked(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("a missing required field", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha/run", "group=")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `aria-invalid="true"`) {
			t.Error("the offending control was not marked invalid")
		}
	})

	// A refusal from Submit itself renders beside the control it blames,
	// rather than as a page-level failure with the input left unmarked.
	t.Run("a refusal from the port", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha/run", "group=forbidden")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "That group cannot be targeted.") {
			t.Error("the port's field message did not reach the form")
		}
	})
}

// TestRunAction_RefusesAnUndeclaredField is the same structural guarantee
// the create and edit forms make: an action's Values expose only its own
// declared fields, so mass assignment is impossible rather than remembered.
func TestRunAction_RefusesAnUndeclaredField(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.post(t, "/ui/"+gadgetView+"/alpha/run", "group=routers&actor=root")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestRecordAction_PerRecordFieldsAreResolvedAndEnforced covers an action
// whose controls differ from record to record: the shape a launch form
// needs, since a template declares which of its fields a launch may
// override and every other one is locked.
func TestRecordAction_PerRecordFieldsAreResolvedAndEnforced(t *testing.T) {
	p := newRecordProbe(t)

	// The record's own control is rendered, and the static action's is not:
	// the form is built from the record rather than from one declaration
	// shared by every record.
	rec := p.get(t, "/ui/"+gadgetView+"/alpha/vary")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET the per-record prompt = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `name="limit"`) {
		t.Error("the form does not render the control this record resolved")
	}
	if strings.Contains(rec.Body.String(), `name="group"`) {
		t.Error("the form renders a control belonging to a different action")
	}

	// The submission is narrowed to the resolved set, not to a declaration:
	// a value the form never offered is refused rather than ignored.
	if got := p.post(t, "/ui/"+gadgetView+"/alpha/vary", "limit=edge-01&group=smuggled"); got.Code != http.StatusBadRequest {
		t.Errorf("posting a control the form never offered = %d, want 400", got.Code)
	}

	// A prompt that cannot be resolved fails the request rather than
	// rendering an empty form. An empty form is not "this record opens
	// nothing", it is "we could not find out", and the two must not look
	// the same.
	if got := p.get(t, "/ui/"+gadgetView+"/done/vary"); got.Code != http.StatusInternalServerError {
		t.Errorf("GET a prompt that could not be resolved = %d, want 500", got.Code)
	}
	if got := p.post(t, "/ui/"+gadgetView+"/done/vary", "limit=x"); got.Code != http.StatusInternalServerError {
		t.Errorf("POST to an action whose prompt could not be resolved = %d, want 500", got.Code)
	}
}

// TestRowAction_CarriesBothIdentifiers is the row half's central claim: a
// control on one row of a section reaches Submit with the record it hangs
// off AND the row it was drawn on.
//
// Both, because either one alone is a plausible implementation that passes a
// looser test. A handler that forwarded only the record id would remove
// whichever element the Submit happened to pick; one that forwarded only the
// row id could not find the document to remove it from.
func TestRowAction_CarriesBothIdentifiers(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.post(t, "/ui/"+gadgetView+"/alpha/detach/r1", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/ui/"+gadgetView+"/alpha" {
		t.Errorf("Location = %q, want the record it acted on", got)
	}

	rowActionCalls.Lock()
	defer rowActionCalls.Unlock()
	if rowActionCalls.parentID != "alpha" {
		t.Errorf("Submit received parent %q, want %q", rowActionCalls.parentID, "alpha")
	}
	if rowActionCalls.rowID != "r1" {
		t.Errorf("Submit received row %q, want %q", rowActionCalls.rowID, "r1")
	}
}

// TestRowAction_RendersOnlyWhereItApplies proves the per-row gate reaches the
// rendering, and that it is a per-ROW decision rather than a per-section one.
//
// The section declares one row action and loads three rows, one of which the
// action withholds itself from. A control drawn on all three and a control
// drawn on none would both be wrong, and only counting them tells those apart
// from the correct answer.
func TestRowAction_RendersOnlyWhereItApplies(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha?tab=related-records")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, row := range []string{"r1", failingRow} {
		if !strings.Contains(body, "/ui/"+gadgetView+"/alpha/detach/"+row) {
			t.Errorf("no row control posts to %q, so the row half never rendered", row)
		}
	}
	if strings.Contains(body, "/ui/"+gadgetView+"/alpha/detach/"+pinnedRow) {
		t.Errorf("a row control rendered for %q, which its Applies withholds it from", pinnedRow)
	}

	// The control is a confirmation, so what renders in the cell is the
	// dialog opener and the posting form is inside the dialog. Both halves
	// matter: an opener with no dialog is a button that does nothing.
	if !strings.Contains(body, "data-dialog-open=") {
		t.Error("the confirming control rendered no dialog opener")
	}
	if !strings.Contains(body, "This removes the row.") {
		t.Error("the dialog does not carry the action's own confirmation text")
	}
}

// TestRowAction_AppliesIsNotASafetyGate pins a property that is deliberate
// and would otherwise be assumed the other way round.
//
// Applies decides what to DRAW. A caller posting the URL by hand never passed
// through the renderer, and a row can stop qualifying between the page
// rendering and the button being pressed, so the handler does not re-consult
// it: Submit is the authority. Asserting this stops a later reader from
// reading the withheld control as a guard and writing a Submit that trusts
// the control it was reached from.
func TestRowAction_AppliesIsNotASafetyGate(t *testing.T) {
	p := newRecordProbe(t)

	if rec := p.post(t, "/ui/"+gadgetView+"/alpha/detach/"+pinnedRow, ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	rowActionCalls.Lock()
	defer rowActionCalls.Unlock()
	if rowActionCalls.rowID != pinnedRow {
		t.Errorf("Submit received row %q, want %q: the handler must not re-gate on Applies",
			rowActionCalls.rowID, pinnedRow)
	}
}

// TestRowAction_RefusesWhatItCannotResolve covers the three ways the route
// can be reached without a control behind it.
func TestRowAction_RefusesWhatItCannotResolve(t *testing.T) {
	p := newRecordProbe(t)

	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"an action name no section declares", "/ui/" + gadgetView + "/alpha/nosuch/r1", http.StatusNotFound},
		{"a record action's name, which takes no row", "/ui/" + gadgetView + "/alpha/run/r1", http.StatusNotFound},
		{"a resource that does not exist", "/ui/nosuch/alpha/detach/r1", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := p.post(t, tc.target, ""); rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// TestRowAction_ReportsAFailingSubmit proves a store refusal reaches the
// caller as a failure rather than as a redirect that looks like success.
//
// A row action does not prompt, so it has no form to redisplay a field error
// on. That makes the error path the whole of its failure reporting, and a
// handler that redirected regardless would tell an operator their input was
// removed when it was not.
func TestRowAction_ReportsAFailingSubmit(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.post(t, "/ui/"+gadgetView+"/alpha/detach/"+failingRow, "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestRowAction_IsNotReachableByGET is the same rule the record action path
// already keeps: a state change never happens on a GET, because a link
// prefetcher or a corporate scanner eventually runs one for somebody.
func TestRowAction_IsNotReachableByGET(t *testing.T) {
	p := newRecordProbe(t)

	rowActionCalls.Lock()
	rowActionCalls.parentID, rowActionCalls.rowID = "", ""
	rowActionCalls.Unlock()

	if rec := p.get(t, "/ui/"+gadgetView+"/alpha/detach/r1"); rec.Code == http.StatusSeeOther {
		t.Errorf("a GET was answered with a redirect, so it ran the action")
	}

	rowActionCalls.Lock()
	defer rowActionCalls.Unlock()
	if rowActionCalls.rowID != "" {
		t.Errorf("a GET reached Submit with row %q", rowActionCalls.rowID)
	}
}

// TestRowAction_TellsTheOperatorWhatTheStoreRefused separates the two
// failures a row action has, which is the whole reason view.Refused exists.
//
// "An injector depends on this input" is a rule the person who pressed the
// button can satisfy by removing the injector first. A store that could not
// be reached is not. Answering both with the words "internal error" tells
// the first person nothing and tells them it was not their doing, and hides
// the sentence that would have resolved it in a log they cannot read.
func TestRowAction_TellsTheOperatorWhatTheStoreRefused(t *testing.T) {
	p := newRecordProbe(t)

	refused := p.post(t, "/ui/"+gadgetView+"/alpha/detach/"+refusedRow, "")
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a refusal = %d, want 422: %s", refused.Code, refused.Body.String())
	}
	if !strings.Contains(refused.Body.String(), refusalText) {
		t.Error("the refusal page does not carry the store's own reason")
	}
	if !strings.Contains(refused.Body.String(), "/ui/"+gadgetView+"/alpha") {
		t.Error("the refusal page offers no way back to the record")
	}

	// The other half. A fault must not borrow the refusal's rendering: its
	// message is not for the operator and may carry whatever the failure
	// happened to be holding.
	fault := p.post(t, "/ui/"+gadgetView+"/alpha/detach/"+failingRow, "")
	if fault.Code != http.StatusInternalServerError {
		t.Errorf("a fault = %d, want 500", fault.Code)
	}
	if strings.Contains(fault.Body.String(), "deliberate row action failure") {
		t.Error("a fault's own message reached the response body")
	}
}

// TestRecordAction_PromptIsPrefilledFromTheRecord is the seam's central
// claim, and the reason it matters is what happens without it.
//
// A form that REPLACES something the record already holds, rendered with
// every control empty, is not a blank form: it is the record's current
// state misrepresented as empty. The operator sees nothing selected, has
// nothing to retype, presses the button they were offered, and the save
// writes the blanks over what was stored. That shape shipped in this
// codebase before this hook existed.
func TestRecordAction_PromptIsPrefilledFromTheRecord(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha/rebind")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `value="current-alpha"`) {
		t.Errorf("the prompt did not render the record's current value:\n%s", rec.Body.String())
	}
}

// TestRecordAction_ARefusedPrefillFailsRatherThanRenderingEmpty covers the
// two ways a prefill can be wrong, and both must fail loudly.
//
// Rendering the form anyway is the one answer that must not happen: an
// empty control and a control holding nothing look identical, so a prefill
// that failed is indistinguishable from a record that holds nothing, and
// the next save cannot tell either.
func TestRecordAction_ARefusedPrefillFailsRatherThanRenderingEmpty(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("the hook itself fails", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/broken/rebind")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("the prefill names a control the form does not draw", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/leaky/rebind")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500: a value nothing renders is a value the next save discards", rec.Code)
		}
	})
}

// TestRecordAction_AnUnprefilledPromptStaysEmpty is the negative control.
//
// Without it, a prefill that leaked across actions would satisfy the test
// above, and every ADD form in the application would arrive carrying
// somebody else's values.
func TestRecordAction_AnUnprefilledPromptStaysEmpty(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha/run")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "current-alpha") {
		t.Error("an action declaring no prefill rendered another action's values")
	}
	if !strings.Contains(rec.Body.String(), `name="group"`) {
		t.Fatal("the unprefilled prompt did not render at all, so this proves nothing")
	}
}

// TestRowAction_PromptIsDrawnAtTheAddressItPostsTo covers the row path's
// prompting half at the handler, which the resource-level suite reaches
// only through one concrete resource.
//
// The GET and the POST share an address deliberately: the form is drawn
// where it submits, so the row the operator was shown and the row the write
// lands on cannot drift apart.
func TestRowAction_PromptIsDrawnAtTheAddressItPostsTo(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha/relabel/r1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="stored-r1"`) {
		t.Errorf("the prompt did not render the row's stored value:\n%s", body)
	}
	if !strings.Contains(body, `action="/ui/`+gadgetView+`/alpha/relabel/r1"`) {
		t.Errorf("the form does not post back to the address it was drawn at:\n%s", body)
	}
}

// TestRowAction_PromptSubmissionCarriesBothIdsAndTheValues is the write
// half: a prompting control has to reach Submit with the record, the row
// AND what the form was told, or it is an edit that does not know what it
// is editing.
func TestRowAction_PromptSubmissionCarriesBothIdsAndTheValues(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.post(t, "/ui/"+gadgetView+"/alpha/relabel/r1", "label=renamed")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	rowActionCalls.Lock()
	defer rowActionCalls.Unlock()
	if rowActionCalls.parentID != "alpha" || rowActionCalls.rowID != "r1" {
		t.Errorf("Submit received (%q, %q), want (alpha, r1)", rowActionCalls.parentID, rowActionCalls.rowID)
	}
	if rowActionCalls.label != "renamed" {
		t.Errorf("Submit received label %q, want %q", rowActionCalls.label, "renamed")
	}
}

// TestRowAction_AFailedPromptRedisplaysWhatWasTyped covers both ways a
// prompting row control can come back with the form still on screen, and
// the property that matters is the same for both.
//
// A redisplay that cleared the controls would make somebody retype a form to
// find out what was wrong with it, which is its own accessibility problem
// and is why every other form in this UI echoes the submission back.
func TestRowAction_AFailedPromptRedisplaysWhatWasTyped(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("the shared validation refuses it", func(t *testing.T) {
		// Over MaxLen, which the declaration sets to 12.
		rec := p.post(t, "/ui/"+gadgetView+"/alpha/relabel/r1", "label=far-too-long-to-accept")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "far-too-long-to-accept") {
			t.Error("the redisplay cleared what was typed")
		}
	})

	t.Run("the resource itself refuses it", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha/relabel/r1", "label=taken")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "That label is already used.") {
			t.Error("the field error from the resource did not reach the form")
		}
		if !strings.Contains(body, `value="taken"`) {
			t.Error("the redisplay cleared what was typed")
		}
	})
}

// TestRowAction_PromptRefusesWhatItCannotDraw covers the edges of the
// prompting path.
func TestRowAction_PromptRefusesWhatItCannotDraw(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("a prefill that fails", func(t *testing.T) {
		// Rendering the form anyway is the one answer that must not
		// happen: an empty control and a control holding nothing look
		// identical, and the next save cannot tell either.
		if rec := p.get(t, "/ui/"+gadgetView+"/alpha/relabel/"+failingRow); rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("a GET on a control with no form", func(t *testing.T) {
		// 404 rather than a redirect, unlike the record action path: a row
		// control with no form is a button and never a link, so nothing on
		// any page draws a GET here.
		if rec := p.get(t, "/ui/"+gadgetView+"/alpha/detach/r1"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("a control the form never offered", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha/relabel/r1", "label=fine&smuggled=x")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400: a submission may not carry a control the form did not draw", rec.Code)
		}
	})
}

// TestRowAction_OnADeclaredViewSaysSoRatherThan404 keeps the row path
// consistent with every other handler here.
//
// A registered-but-unimplemented view answers with the panel that says so,
// because 404 is the one reply that makes a declared view indistinguishable
// from a view that does not exist, and being able to tell those apart is the
// entire reason a declared view is registered.
func TestRowAction_OnADeclaredViewSaysSoRatherThan404(t *testing.T) {
	p := newRecordProbe(t)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			target := "/ui/" + declaredView + "/alpha/relabel/r1"
			var rec *httptest.ResponseRecorder
			if method == http.MethodGet {
				rec = p.get(t, target)
			} else {
				rec = p.post(t, target, "")
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 with the declared panel", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "not implemented") {
				t.Errorf("the reply is not the declared panel:\n%s", rec.Body.String())
			}
		})
	}
}

// TestDownload_ServesTheDeclaredFormatAndRefusesTheRest covers the route a
// download is addressed at, which is the first in this application to serve
// a body that is neither HTML nor JSON.
//
// The three refusals matter as much as the success. A view declaring no
// downloads, a format it does not declare, and a record whose Available
// says no all 404 rather than serving an empty file under a confident name:
// a browser saves whatever comes back, so a 200 carrying the wrong thing is
// worse than an error the caller can see.
func TestDownload_ServesTheDeclaredFormatAndRefusesTheRest(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("the declared format is served as a file", func(t *testing.T) {
		rec := p.get(t, "/ui/"+gadgetView+"/widget/download/report")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
			t.Errorf("Content-Type = %q, want the declared one", got)
		}
		// The disposition is what makes this a download rather than a page
		// of text, and the filename is what stops three records' reports
		// landing in a folder under one name.
		if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="report-widget.csv"` {
			t.Errorf("Content-Disposition = %q", got)
		}
		// Declared and never sniffed, matching the stance the static
		// handler already takes: a browser deciding for itself that a file
		// is script is what this header exists to stop.
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if body := rec.Body.String(); !strings.Contains(body, "widget") {
			t.Errorf("the body does not carry the record: %q", body)
		}
	})

	t.Run("a record with nothing to give is a 404", func(t *testing.T) {
		if rec := p.get(t, "/ui/"+gadgetView+"/done/download/report"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404: a record whose Available says no must not save an empty file", rec.Code)
		}
	})

	t.Run("an undeclared format is a 404", func(t *testing.T) {
		if rec := p.get(t, "/ui/"+gadgetView+"/widget/download/invented"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("a view declaring no downloads is a 404", func(t *testing.T) {
		if rec := p.get(t, "/ui/"+declaredView+"/anything/download/report"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

// TestDownload_TheRecordPageOffersOnlyWhatTheRecordHas is the chooser half:
// the control is drawn where it would work and absent where it would not.
//
// Absent rather than disabled. A disabled control says "this is yours, but
// not now", where the truth for a record with nothing to give -- an expired
// log window, a job that wrote no journal -- is that waiting will not bring
// it back.
func TestDownload_TheRecordPageOffersOnlyWhatTheRecordHas(t *testing.T) {
	p := newRecordProbe(t)

	offered := p.get(t, "/ui/"+gadgetView+"/widget").Body.String()
	if !strings.Contains(offered, "/download/report") {
		t.Error("a record with something to download offers no link to it")
	}
	if !strings.Contains(offered, "Report (CSV)") {
		t.Error("the download control does not name the artefact it produces")
	}

	withheld := p.get(t, "/ui/"+gadgetView+"/done").Body.String()
	if strings.Contains(withheld, "/download/report") {
		t.Error("a record with nothing to download still offers the link, which can only produce an empty file")
	}
}
