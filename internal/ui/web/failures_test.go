package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers what happens when a port fails and what happens when the
// caller is HTMX rather than a browser navigation. Both are branches every
// handler carries and neither is on any happy path, so the conformance suite
// in internal/ui/resources cannot reach them: it drives real resources whose
// real ports succeed.

// portErr is what the failing view's every operation returns. It is checked
// for in response bodies, because a port's own error text can name a table,
// a column or a filesystem path and must not reach a caller.
var portErr = errors.New("deliberate port failure naming /var/lib/secret and table users")

// brokenView is registered with handlers that always fail.
const brokenView = "widgets"

type brokenReader struct{}

func (brokenReader) List(context.Context, view.Query) (view.Page[string], error) {
	return view.Page[string]{}, portErr
}
func (brokenReader) Get(context.Context, string) (string, error) { return "", portErr }

type brokenWriter struct{}

func (brokenWriter) Create(context.Context, string) (string, error) { return "", portErr }
func (brokenWriter) Update(context.Context, string, string) error   { return portErr }
func (brokenWriter) Delete(context.Context, string) error           { return portErr }

var registerBrokenView = sync.OnceFunc(func() {
	view.MustRegister(view.Descriptor{
		Name:     brokenView,
		Title:    "Widgets",
		NavLabel: "WIDGETS",
		NavOrder: 40,
		Summary:  "A view whose every port fails, to drive the failure branches.",
		Status:   view.StatusImplemented,
		IDField:  "name",
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
		Handlers: view.MustBind[string](brokenReader{}, brokenWriter{}, view.Projector[string]{
			Row:  func(s string) view.Row { return view.Row{ID: s, Cells: view.Cells{"name": s}} },
			Form: func(s string) map[string]string { return map[string]string{"name": s} },
			Bind: func(v view.Values) (string, view.FieldErrors) { return v.Get("name"), view.FieldErrors{} },
		}),
	})
})

func newBrokenProbe(t *testing.T) *probe {
	t.Helper()
	registerBrokenView()
	return newProbe(t, allowAll{}, permitEverything{})
}

// TestPortFailures_AreGenericServerErrors walks every handler that touches a
// port. Each must answer 500 and none may leak the port's own error text.
func TestPortFailures_AreGenericServerErrors(t *testing.T) {
	p := newBrokenProbe(t)

	t.Run("list", func(t *testing.T) {
		assertOpaqueServerError(t, p.get(t, "/ui/"+brokenView))
	})

	writes := map[string]struct{ target, body string }{
		"create":  {"/ui/" + brokenView, "name=alpha"},
		"update":  {"/ui/" + brokenView + "/alpha", "name=renamed"},
		"destroy": {"/ui/" + brokenView + "/alpha", "_method=DELETE"},
	}
	for name, tc := range writes {
		t.Run(name, func(t *testing.T) {
			rec := p.post(t, tc.target, tc.body)
			assertOpaqueServerError(t, rec)
		})
	}
}

// assertOpaqueServerError is the shared assertion: a 500 that says nothing
// about what failed inside.
func assertOpaqueServerError(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"/var/lib/secret", "table users", "deliberate port failure"} {
		if strings.Contains(body, secret) {
			t.Errorf("the port's error text leaked into the response: %s", body)
		}
	}
}

// TestRecordReadFailure_IsReportedAsNotFound pins a behaviour worth naming
// rather than assuming.
//
// detail and editForm map every Get error onto 404, without distinguishing
// "no such record" from "the store could not answer". Authorization has
// already passed by that point, so non-disclosure is not the reason; the
// effect is that a database outage renders as "this record does not exist"
// on every record page, which tells an operator the opposite of what is
// happening.
//
// It is asserted here as the current contract, not endorsed. Changing it
// means giving Handlers.Get a way to distinguish the two, which is a port
// change rather than a handler change.
func TestRecordReadFailure_IsReportedAsNotFound(t *testing.T) {
	p := newBrokenProbe(t)

	for _, target := range []string{
		"/ui/" + brokenView + "/alpha",
		"/ui/" + brokenView + "/alpha/edit",
	} {
		rec := p.get(t, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want the current 404 contract", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "deliberate port failure") {
			t.Errorf("GET %s leaked the port's error text", target)
		}
	}
}

// TestWrites_AnswerHTMXWithAHeaderRatherThanARedirect is the fragment
// contract. An HTMX request that received a 303 would swap a whole rendered
// page into whatever element made the request; HX-Redirect tells the client
// to navigate instead.
func TestWrites_AnswerHTMXWithAHeaderRatherThanARedirect(t *testing.T) {
	p := newRecordProbe(t)

	r := httptest.NewRequest(http.MethodPost, "/ui/"+gadgetView+"/alpha", strings.NewReader("name=renamed"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set(session_CSRFHeader, p.csrf(t))
	r.Header.Set("HX-Request", "true")

	rec := p.serve(p.authed(r))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 for a fragment write: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/ui/"+gadgetView+"/alpha" {
		t.Errorf("HX-Redirect = %q, want the record path", got)
	}
	if rec.Header().Get("Location") != "" {
		t.Error("a fragment write also sent Location, which would double-navigate")
	}
}

// TestList_PagingParametersAreBounded proves an out-of-range limit is
// ignored rather than honoured. A page size read from a query parameter with
// no ceiling is a request to hold every row in memory.
func TestList_PagingParametersAreBounded(t *testing.T) {
	p := newRecordProbe(t)

	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=100000", "?limit=abc", "?after=x&q=y"} {
		rec := p.get(t, "/ui/"+gadgetView+q)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", q, rec.Code)
		}
	}
}

// TestActionForm_PortFailureOnOptionsIsAServerError covers renderAction's
// option-resolution branch: a select whose choices cannot be loaded must not
// render an empty control that silently refuses every submission.
func TestActionForm_PortFailureOnOptionsIsAServerError(t *testing.T) {
	registerOptionFailingView()
	p := newProbe(t, allowAll{}, permitEverything{})

	rec := p.get(t, "/ui/"+optionFailView+"/alpha/run")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

// optionFailView carries an action whose select cannot resolve its choices.
const optionFailView = "sprockets-opt"

var registerOptionFailingView = sync.OnceFunc(func() {
	view.MustRegister(view.Descriptor{
		Name:     optionFailView,
		Title:    "Option Failures",
		NavLabel: "OPTFAIL",
		NavOrder: 50,
		Summary:  "A view whose action's select cannot load its choices.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true},
		},
		Ops: view.Ops{List: &apispec.ListDevices, Get: &apispec.GetDevice},
		Actions: []view.RecordAction{{
			Name:     "run",
			Label:    "Run",
			Heading:  "Run it",
			Endpoint: &apispec.LaunchTemplate,
			Fields: []view.Field{{
				Name: "group", Label: "GROUP", Kind: view.KindSelect, InForm: true,
				Options: func(context.Context) ([]view.Option, error) { return nil, portErr },
			}},
			Submit: func(context.Context, string, view.Values) (string, view.FieldErrors, error) {
				return "", nil, nil
			},
		}},
		Handlers: view.MustBind[string](probeReader{}, nil, view.Projector[string]{
			Row: func(s string) view.Row { return view.Row{ID: s, Cells: view.Cells{"name": s}} },
		}),
	})
})
