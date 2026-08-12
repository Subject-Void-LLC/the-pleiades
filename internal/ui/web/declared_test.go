package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers the two shapes a handler meets that are neither success
// nor a port failure: a view that is registered but declared, and a form
// whose select cannot load its choices.

// TestDeclaredView_EveryRouteRendersThePanelRatherThanWorking is the
// guarantee that makes StatusDeclared safe to use.
//
// A declared view reserves a place in the navigation and says out loud what
// belongs there. What it must never do is half-work: no route may 500 on a
// nil handler, and no write route may quietly succeed. resolve renders the
// panel before it ever looks at Ops, which is why a descriptor with no
// endpoints at all still answers every route coherently.
func TestDeclaredView_EveryRouteRendersThePanelRatherThanWorking(t *testing.T) {
	p := newRecordProbe(t)

	reads := []string{
		"/ui/" + declaredView,
		"/ui/" + declaredView + "/new",
		"/ui/" + declaredView + "/alpha",
		"/ui/" + declaredView + "/alpha/edit",
	}
	for _, target := range reads {
		t.Run("GET "+target, func(t *testing.T) {
			rec := p.get(t, target)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			// The panel says what is missing, so it is useful rather than
			// merely apologetic.
			if !strings.Contains(rec.Body.String(), "Sprockets") {
				t.Error("the declared panel did not name the view")
			}
		})
	}

	writes := []struct{ target, body string }{
		{"/ui/" + declaredView, "name=alpha"},
		{"/ui/" + declaredView + "/alpha", "name=alpha"},
		{"/ui/" + declaredView + "/alpha", "_method=DELETE"},
	}
	for _, tc := range writes {
		t.Run("POST "+tc.target+" "+tc.body, func(t *testing.T) {
			rec := p.post(t, tc.target, tc.body)
			// Whatever it answers, it must not be a redirect: a 303 is what
			// a successful write looks like, and nothing was written.
			if rec.Code == http.StatusSeeOther || rec.Code == http.StatusNoContent {
				t.Fatalf("a declared view answered a write with %d, which reads as success", rec.Code)
			}
		})
	}

	// chart.json 404s, because chartData resolves the descriptor itself and
	// refuses a view declaring no chart. The logs route instead answers the
	// declared panel, because resolve renders it before it ever looks at the
	// operation. Both are coherent and they differ, which is worth pinning:
	// "this whole view is not implemented" is a better answer than "that
	// route does not exist", and only one of the two routes gives it.
	t.Run("GET chart.json", func(t *testing.T) {
		if rec := p.get(t, "/ui/"+declaredView+"/chart.json"); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
	t.Run("GET logs", func(t *testing.T) {
		rec := p.get(t, "/ui/"+declaredView+"/alpha/logs")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want the declared panel", rec.Code)
		}
		// It must be the panel, not a stream page wired to nothing.
		if strings.Contains(rec.Body.String(), "EventSource") ||
			strings.Contains(rec.Body.String(), "data-stream-url") {
			t.Error("a declared view rendered a live stream page")
		}
	})
}

// formOptionFailView has a real create form whose select cannot resolve its
// choices, which is the branch renderForm carries for every select field.
const formOptionFailView = "flanges"

var registerFormOptionFailView = sync.OnceFunc(func() {
	view.MustRegister(view.Descriptor{
		Name:     formOptionFailView,
		Title:    "Flanges",
		NavLabel: "FLANGES",
		NavOrder: 60,
		Summary:  "A view whose form select cannot load its choices.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true,
				InForm: true, MobilePrimary: true},
			{Name: "org", Label: "ORGANIZATION", Kind: view.KindSelect, InForm: true,
				Options: func(context.Context) ([]view.Option, error) { return nil, portErr }},
		},
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
		Handlers: view.MustBind[string](probeReader{}, probeWriter{}, view.Projector[string]{
			Row:  func(s string) view.Row { return view.Row{ID: s, Cells: view.Cells{"name": s}} },
			Form: func(s string) map[string]string { return map[string]string{"name": s} },
			Bind: func(v view.Values) (string, view.FieldErrors) { return v.Get("name"), view.FieldErrors{} },
		}),
	})
})

// TestForm_UnloadableChoicesAreAServerErrorNotAnEmptyControl is the lesson
// FAILURE_PATTERNS #101 records, enforced at the handler.
//
// A required select whose options cannot be loaded renders a control that
// refuses every submission, and the caller has no way to tell why. Answering
// 500 says the truth: the page could not be built.
func TestForm_UnloadableChoicesAreAServerErrorNotAnEmptyControl(t *testing.T) {
	registerFormOptionFailView()
	p := newProbe(t, allowAll{}, permitEverything{})

	for _, target := range []string{
		"/ui/" + formOptionFailView + "/new",
		"/ui/" + formOptionFailView + "/alpha/edit",
	} {
		t.Run(target, func(t *testing.T) {
			rec := p.get(t, target)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "deliberate port failure") {
				t.Error("the option source's error text leaked")
			}
		})
	}
}

// TestFragments_AnswerTheSameURLWithoutChrome is the live-update contract.
//
// A refresh is the same read from the same URL, distinguished only by the
// header HTMX sets. That is why declaring a refresh adds no route: there is
// no second endpoint to mount, to authorize, or to keep in step with the
// first.
func TestFragments_AnswerTheSameURLWithoutChrome(t *testing.T) {
	p := newRecordProbe(t)

	for name, target := range map[string]string{
		"list":   "/ui/" + gadgetView,
		"detail": "/ui/" + gadgetView + "/alpha",
	} {
		t.Run(name, func(t *testing.T) {
			full := p.get(t, target)
			if full.Code != http.StatusOK {
				t.Fatalf("full page: status = %d", full.Code)
			}
			if !strings.Contains(full.Body.String(), "<html") {
				t.Fatal("the full page is not a document")
			}

			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set("HX-Request", "true")
			frag := p.serve(p.authed(r))

			if frag.Code != http.StatusOK {
				t.Fatalf("fragment: status = %d: %s", frag.Code, frag.Body.String())
			}
			body := frag.Body.String()
			// No chrome, or the swap would nest a whole page inside the
			// element that asked for it.
			for _, chrome := range []string{"<html", "<body", "<nav", "<script"} {
				if strings.Contains(body, chrome) {
					t.Errorf("the fragment carried %s", chrome)
				}
			}
			if len(body) >= len(full.Body.String()) {
				t.Error("the fragment is not smaller than the page")
			}
		})
	}
}

// TestPolling_IsDeclaredInTheMarkupAndStopsWhenTerminal covers both halves of
// RefreshSpec, and the stopping half is the one that matters for cost: a
// finished job left open in a tab must not keep asking forever.
func TestPolling_IsDeclaredInTheMarkupAndStopsWhenTerminal(t *testing.T) {
	p := newRecordProbe(t)

	t.Run("a collection always polls", func(t *testing.T) {
		body := p.get(t, "/ui/"+gadgetView).Body.String()
		for _, want := range []string{`hx-get`, `hx-trigger="every 5s"`, `hx-swap="outerHTML"`, `data-poll="true"`} {
			if !strings.Contains(body, want) {
				t.Errorf("the list region is missing %s", want)
			}
		}
	})

	t.Run("a running record polls", func(t *testing.T) {
		body := p.get(t, "/ui/"+gadgetView+"/running").Body.String()
		if !strings.Contains(body, `hx-trigger="every 5s"`) {
			t.Error("a running record does not refresh itself")
		}
	})

	// The refreshed fragment simply carries no trigger, so the polling ends
	// there. Nothing cancels a timer, which is why this cannot leak.
	t.Run("a terminal record stops", func(t *testing.T) {
		body := p.get(t, "/ui/"+gadgetView+"/done").Body.String()
		if strings.Contains(body, "hx-trigger") {
			t.Error("a terminal record keeps polling, so a finished job costs requests forever")
		}
		if strings.Contains(body, `data-poll`) {
			t.Error("a terminal record still claims to be polled")
		}
	})
}

// TestPolledRegion_CarriesTheAttributesAccessibilityDependsOn pins the
// contract app.js reads. A polled swap must be identifiable, because focus
// must not follow it and its announcement must be deduplicated; a
// user-initiated swap must remain focusable.
func TestPolledRegion_CarriesTheAttributesAccessibilityDependsOn(t *testing.T) {
	p := newRecordProbe(t)

	body := p.get(t, "/ui/"+gadgetView).Body.String()
	if !strings.Contains(body, `data-poll="true"`) {
		t.Error("a polled region is not marked, so app.js would steal focus on every tick")
	}
	if !strings.Contains(body, "data-announce=") {
		t.Error("a swappable region carries no announcement, so a screen reader learns nothing changed")
	}
	if !strings.Contains(body, "data-focus-after-swap") {
		t.Error("the list region lost its focus target for user-initiated swaps")
	}
}

// TestUnregisteredResource_IsNotFoundEverywhere proves the {resource}
// parameter reaches a registry lookup and nothing else. It is never a
// filesystem path, so an unregistered name is a 404 rather than a traversal
// question.
func TestUnregisteredResource_IsNotFoundEverywhere(t *testing.T) {
	p := newRecordProbe(t)

	for _, target := range []string{
		"/ui/nope",
		"/ui/nope/new",
		"/ui/nope/alpha",
		"/ui/nope/alpha/edit",
		"/ui/nope/chart.json",
		"/ui/nope/alpha/logs",
		"/ui/nope/alpha/run",
		"/ui/..%2F..%2Fetc%2Fpasswd",
	} {
		if rec := p.get(t, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", target, rec.Code)
		}
	}
}

// TestPagedList_DoesNotRefreshItself is the regression test for a bug the
// first version of this shipped with.
//
// The handler declines to build a refresh URL when the reader has paged
// forward, and the model treats an absent URL as "do not poll". The first
// version instead treated it as "use the default", so a reader on page two
// had page one swapped underneath them every five seconds. The doc comment
// described the correct behaviour the whole time; only the code disagreed.
func TestPagedList_DoesNotRefreshItself(t *testing.T) {
	p := newRecordProbe(t)

	first := p.get(t, "/ui/"+gadgetView).Body.String()
	if !strings.Contains(first, "hx-trigger") {
		t.Fatal("the first page does not refresh, so this test proves nothing")
	}

	paged := p.get(t, "/ui/"+gadgetView+"?after=alpha").Body.String()
	if strings.Contains(paged, "hx-trigger") {
		t.Error("a paged list refreshes itself, so rows shift under a cursor chosen to hold them still")
	}
	if strings.Contains(paged, `data-poll`) {
		t.Error("a paged list is marked as polled")
	}
}

// TestList_CursorLinkRidesInsideTheSwappedRegion proves the pagination
// control refreshes with the rows it was computed from. Left outside, it
// would keep pointing past rows a refresh had already replaced.
func TestList_CursorLinkRidesInsideTheSwappedRegion(t *testing.T) {
	p := newRecordProbe(t)

	r := httptest.NewRequest(http.MethodGet, "/ui/"+gadgetView, nil)
	r.Header.Set("HX-Request", "true")
	fragment := p.serve(p.authed(r)).Body.String()

	full := p.get(t, "/ui/"+gadgetView).Body.String()
	if strings.Contains(full, "Next page") != strings.Contains(fragment, "Next page") {
		t.Error("the cursor link is outside the swapped region, so a refresh leaves it stale")
	}
}

// TestRefreshURL_CarriesTheReadersNarrowing proves a refresh keeps whatever
// the reader applied instead of silently widening back to everything on the
// next tick, and that it is rebuilt from parsed values rather than echoed
// from the raw query.
func TestRefreshURL_CarriesTheReadersNarrowing(t *testing.T) {
	p := newRecordProbe(t)

	for _, tc := range []struct {
		name   string
		query  string
		want   string
		absent string
	}{
		{
			name:  "a search survives",
			query: "?q=router",
			want:  "hx-get=\"/ui/" + gadgetView + "?q=router\"",
		},
		{
			name:  "a non-default limit survives",
			query: "?limit=5",
			want:  "hx-get=\"/ui/" + gadgetView + "?limit=5\"",
		},
		{
			name:  "a default limit is not repeated back",
			query: "",
			want:  "hx-get=\"/ui/" + gadgetView + "\"",
		},
		{
			// A parameter this handler ignored must not come back on every
			// tick: the refresh URL is built from what was parsed, not from
			// what was sent.
			name:   "an unread parameter is dropped",
			query:  "?utm_source=chat&nonsense=1",
			want:   "hx-get=\"/ui/" + gadgetView + "\"",
			absent: "utm_source",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := p.get(t, "/ui/"+gadgetView+tc.query).Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("refresh URL missing %s", tc.want)
			}
			if tc.absent != "" && strings.Contains(body, tc.absent) {
				t.Errorf("an unread parameter %q was reflected into the refresh URL", tc.absent)
			}
		})
	}
}
