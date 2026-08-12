package web

import (
	"context"
	"errors"
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
					return []view.Row{{ID: "r1", Cells: view.Cells{"label": "parent=" + parentID}}}, nil
				},
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
func TestDetail_RendersFieldsAndSections(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
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

// TestDetail_SurvivesASectionThatCannotLoad is the deliberate degradation.
// The record's own fields are the answer to "what is this", and losing them
// because a related table could not be read would turn a partial outage into
// a total one, on the page somebody opened precisely because something is
// already wrong.
func TestDetail_SurvivesASectionThatCannotLoad(t *testing.T) {
	p := newRecordProbe(t)

	rec := p.get(t, "/ui/"+gadgetView+"/alpha")
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
	if !strings.Contains(body, "parent=alpha") {
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
}

func TestWrites_UpdateAndDeleteRedirect(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("update", func(t *testing.T) {
		rec := p.post(t, "/ui/"+gadgetView+"/alpha", "name=renamed")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
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
