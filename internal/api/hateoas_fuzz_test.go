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

// These targets deliberately do not wrap the request in a recover().
// IMPLEMENTATION.md flags a blanket recover() in Phase 14's fuzz tests as
// structurally preventing them from reporting the panics they exist to
// find, and the same applies here: a panic inside the router is the
// finding, not something to absorb.

// fuzzLinkRouter is the router both targets drive: two methods on one
// parameterized pattern, so a fuzzed parameter reaches href construction
// and the affordance table both.
func fuzzLinkRouter(tb testing.TB) http.Handler {
	tb.Helper()
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(tb),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf,
				Handler: func(w http.ResponseWriter, r *http.Request) {
					p := payload{Name: "fuzz"}
					api.Respond(w, r, http.StatusOK, &p)
				}},
			{Method: http.MethodDelete, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelDelete,
				Handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }},
		},
	})
	if err != nil {
		tb.Fatalf("NewRouter: %v", err)
	}
	return router
}

// FuzzHrefConstruction drives arbitrary URL parameters through the real
// router and asserts the invariants an href must hold no matter what a
// caller sends: it parses, it stays inside the versioned prefix, it never
// gains a path segment, and it carries no raw character that would change
// a URL's meaning.
func FuzzHrefConstruction(f *testing.F) {
	for _, seed := range []string{
		"edge-01",
		`"evil"`,
		"<script>alert(1)</script>",
		"../../etc/passwd",
		"a/b/c",
		"100%off",
		"ünïcøde",
		strings.Repeat("a", 300),
		"",
		"\x00",
	} {
		f.Add(seed)
	}

	router := fuzzLinkRouter(f)

	f.Fuzz(func(t *testing.T, name string) {
		target := api.APIVersionPrefix + "/inventory/devices/" + url.PathEscape(name)

		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			// An input that cannot even be expressed as a request URL is
			// not a finding about this package.
			t.Skip()
		}

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			// A rejected or unmatched request carries no links to check.
			return
		}

		var body struct {
			Links []struct {
				Href string `json:"href"`
			} `json:"_links"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not valid JSON for name %q: %v: %s", name, err, rr.Body.String())
		}

		const prefix = api.APIVersionPrefix + "/inventory/devices/"
		for _, l := range body.Links {
			if !strings.HasPrefix(l.Href, prefix) {
				t.Fatalf("href %q for name %q escaped the route's own prefix", l.Href, name)
			}
			if _, err := url.Parse(l.Href); err != nil {
				t.Fatalf("href %q for name %q does not parse: %v", l.Href, name, err)
			}
			if strings.Contains(strings.TrimPrefix(l.Href, prefix), "/") {
				t.Fatalf("href %q for name %q gained a path segment", l.Href, name)
			}
			if strings.ContainsAny(l.Href, "\x00\r\n \"<>") {
				t.Fatalf("href %q for name %q carries a raw character with meaning to a URL or HTML parser", l.Href, name)
			}
		}
	})
}

// FuzzRespondEnvelope drives arbitrary payload content and status codes
// through the seam, asserting the response is always well-formed JSON with
// exactly the status the handler asked for.
func FuzzRespondEnvelope(f *testing.F) {
	f.Add("name", int64(0), 200)
	f.Add("", int64(-1), 404)
	f.Add(`{"nested":"json"}`, int64(9007199254740993), 500)
	f.Add("\x00\x01\x02", int64(1)<<62, 201)
	f.Add(strings.Repeat("x", 10000), int64(0), 204)

	f.Fuzz(func(t *testing.T, name string, count int64, status int) {
		// Constrain the status to the range net/http can actually write;
		// anything else is a programming error in a caller, not a
		// property of this seam.
		if status < 100 || status > 599 {
			t.Skip()
		}

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)

		p := payload{Count: count, Name: name}
		api.Respond(rr, req, status, &p)

		if rr.Code != status {
			t.Fatalf("handler asked for %d, client observed %d", status, rr.Code)
		}

		// 204 and 304 carry no body by definition; everything else must
		// be decodable, because a client will try.
		if status == http.StatusNoContent || status == http.StatusNotModified {
			return
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("body is not valid JSON: %v: %q", err, rr.Body.String())
		}
		if _, ok := got["count"]; !ok {
			t.Fatalf("body lost the payload's own field: %q", rr.Body.String())
		}
	})
}
