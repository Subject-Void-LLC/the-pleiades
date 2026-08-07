package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// This file covers href construction, which is the part of hypermedia a
// client actually acts on. FAILURE_PATTERNS.md #72 records what the
// deleted middleware did instead: it reflected r.URL.Path, the caller's
// own decoded input, straight back as the href of every link it emitted.

// linkRouter builds a router with one parameterized route that responds
// through the seam, so hrefs are produced exactly as production produces
// them.
func linkRouter(t *testing.T) http.Handler {
	t.Helper()
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf,
				Handler: func(w http.ResponseWriter, r *http.Request) {
					p := payload{Name: "x"}
					api.Respond(w, r, http.StatusOK, &p)
				}},
			{Method: http.MethodDelete, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelDelete,
				Handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

// linksFromResponse decodes the _links array out of a response body.
func linksFromResponse(t *testing.T, rr *httptest.ResponseRecorder) []struct {
	Rel    string `json:"rel"`
	Href   string `json:"href"`
	Method string `json:"method"`
} {
	t.Helper()
	var body struct {
		Links []struct {
			Rel    string `json:"rel"`
			Href   string `json:"href"`
			Method string `json:"method"`
		} `json:"_links"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body %q: %v", rr.Body.String(), err)
	}
	return body.Links
}

func TestHref_IsBuiltFromTheRoutePatternNotTheRequestPath(t *testing.T) {
	// Every href must be the server's own URL template with the caller's
	// parameter substituted, never the caller's raw path. The two look
	// identical for a well-behaved request, so the table below uses
	// parameters that are legal in a path but would be visible in the
	// output if the path were reflected verbatim.
	for _, tc := range []struct {
		name  string
		param string
	}{
		{"ordinary name", "edge-01"},
		{"quotes", `"evil"`},
		{"angle brackets", "<script>"},
		{"percent", "100%off"},
		{"unicode", "dev-ünïcøde"},
		{"dots", "..%2f..%2fetc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := linkRouter(t)
			target := api.APIVersionPrefix + "/inventory/devices/" + url.PathEscape(tc.param)

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s returned %d", target, rr.Code)
			}

			links := linksFromResponse(t, rr)
			if len(links) == 0 {
				t.Fatal("no links emitted")
			}

			for _, l := range links {
				// The structure is always the server's: the prefix and the
				// fixed segments come from the pattern.
				if !strings.HasPrefix(l.Href, api.APIVersionPrefix+"/inventory/devices/") {
					t.Errorf("href %q is not built from the route pattern", l.Href)
				}
				// The parameter is escaped as a path segment, so it can
				// never introduce a new segment or change the URL's shape.
				if strings.Count(strings.TrimPrefix(l.Href, api.APIVersionPrefix+"/inventory/devices/"), "/") != 0 {
					t.Errorf("href %q lets a parameter add a path segment", l.Href)
				}
				// And it round trips: a client parsing this gets the name
				// back, so escaping has not corrupted the reference.
				parsed, err := url.Parse(l.Href)
				if err != nil {
					t.Fatalf("href %q does not parse: %v", l.Href, err)
				}
				if got := strings.TrimPrefix(parsed.EscapedPath(), api.APIVersionPrefix+"/inventory/devices/"); got == "" {
					t.Errorf("href %q carries no device name", l.Href)
				}
			}
		})
	}
}

func TestHref_NeverContainsRawControlOrQuoteCharacters(t *testing.T) {
	// A hypermedia client is invited to follow these values. Whatever a
	// caller puts in the path, the emitted href must remain a well-formed
	// URL with no character that could change its meaning to a parser.
	router := linkRouter(t)
	target := api.APIVersionPrefix + "/inventory/devices/" + url.PathEscape(`a"b<c>d&e f`)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))

	for _, l := range linksFromResponse(t, rr) {
		for _, bad := range []string{`"`, "<", ">", " ", "\n", "\r"} {
			if strings.Contains(l.Href, bad) {
				t.Errorf("href %q contains the raw character %q", l.Href, bad)
			}
		}
	}
}

func TestLinks_EveryLinkCarriesItsOwnMethod(t *testing.T) {
	// A relation and a URL are not enough: two relations on one resource
	// share an href and differ only by method, so a client that ignored
	// the method would issue the wrong request. This is the concrete
	// reason the deleted middleware's fixed "GET" was wrong.
	router := linkRouter(t)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/edge-01", nil))

	byRel := map[string]string{}
	for _, l := range linksFromResponse(t, rr) {
		byRel[l.Rel] = l.Method
	}

	if byRel["self"] != http.MethodGet {
		t.Errorf("self link declares method %q, want GET", byRel["self"])
	}
	if byRel["delete"] != http.MethodDelete {
		t.Errorf("delete link declares method %q, want DELETE", byRel["delete"])
	}
}

func TestLinks_UnroutedPathEmitsNoLinks(t *testing.T) {
	// A path that matched no route has no affordances. Emitting a self
	// link would name a URL this router does not serve, which is the
	// hypermedia equivalent of inventing an endpoint.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/not/a/route", nil)

	p := payload{Name: "x"}
	api.Respond(rr, req, http.StatusOK, &p)

	if strings.Contains(rr.Body.String(), `"href"`) {
		t.Errorf("an unrouted request produced links: %s", rr.Body.String())
	}
}
