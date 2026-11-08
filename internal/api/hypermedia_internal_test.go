package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// These are internal tests for the two unexported helpers that turn a
// request into a hypermedia link.
//
// They are worth testing directly rather than only through a rendered
// response, because both are total functions with edge cases a handler test
// never reaches: a request with no chi route context at all, a wildcard
// parameter, an identifier needing escaping, and a route context whose keys
// and values have fallen out of step. Each of those produces a wrong URL
// rather than an error, and a wrong URL in an _links array is a link a
// client follows to the wrong place.

func TestPatternMatches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"exact literal", "/jobs", "/jobs", true},
		{"one parameter", "/jobs/{id}", "/jobs/abc", true},
		{"parameter matches an escaped value", "/jobs/{id}", "/jobs/a%2Fb", true},
		{"trailing slash is ignored", "/jobs/{id}", "/jobs/abc/", true},
		{"leading slash is ignored", "jobs/{id}", "/jobs/abc", true},
		{"too few segments", "/jobs/{id}", "/jobs", false},
		{"too many segments", "/jobs/{id}", "/jobs/abc/logs", false},
		{"different literal", "/jobs/{id}", "/runbooks/abc", false},
		{"literal where a parameter is not", "/jobs/dispatch", "/jobs/abc", false},
		{"empty against empty", "/", "/", true},
		{"empty pattern against a path", "/", "/jobs", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := patternMatches(splitPath(tc.pattern), splitPath(tc.path))
			if got != tc.want {
				t.Errorf("patternMatches(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestSplitPath_DiscardsEmptySegments(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"/", 0},
		{"//", 0},
		{"/jobs", 1},
		{"/jobs/", 1},
		{"//jobs//abc//", 2},
	} {
		if got := splitPath(tc.in); len(got) != tc.want {
			t.Errorf("splitPath(%q) = %v, want %d segments", tc.in, got, tc.want)
		}
	}
}

// TestHrefFor_WithNoRouteContext covers the path a direct handler call takes:
// the pattern is returned unchanged rather than the function panicking on a
// nil context.
func TestHrefFor_WithNoRouteContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/jobs/abc", nil)

	got, err := hrefFor(r, "/jobs/{id}")
	if err != nil {
		t.Fatalf("hrefFor() = %v, want nil", err)
	}
	if got != "/jobs/{id}" {
		t.Errorf("hrefFor() = %q, want the pattern unchanged", got)
	}
}

// TestHrefFor_SubstitutesAndEscapes is the ordinary case, plus the one that
// matters: an identifier containing a slash must not become a second path
// segment in the link a client is handed.
func TestHrefFor_SubstitutesAndEscapes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		value   string
		pattern string
		want    string
	}{
		{"plain identifier", "id", "abc", "/jobs/{id}", "/jobs/abc"},
		{"identifier with a slash", "id", "a/b", "/jobs/{id}", "/jobs/a%2Fb"},
		{"identifier with a space", "name", "core router", "/devices/{name}", "/devices/core%20router"},
		{"traversal in an identifier", "name", "../..", "/devices/{name}", "/devices/..%2F.."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add(tc.key, tc.value)

			r := httptest.NewRequest(http.MethodGet, "/irrelevant", nil)
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

			got, err := hrefFor(r, tc.pattern)
			if err != nil {
				t.Fatalf("hrefFor() = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("hrefFor() = %q, want %q", got, tc.want)
			}
			if strings.Count(got, "/") != strings.Count(tc.pattern, "/") {
				t.Errorf("hrefFor() = %q, which has a different segment count from its pattern", got)
			}
		})
	}
}

// TestHrefFor_EscapesARoutedNameOnce routes real requests through chi and
// reads back the link built for each. chi matches on the request's escaped
// path whenever it has one (any name holding a / or a ;), so the parameter
// value it records is still escaped there and decoded otherwise. The link
// must name the record the request named either way: it used to escape the
// still-escaped value again, so /devices/sw%2F1 linked to /devices/sw%252F1,
// a different device.
func TestHrefFor_EscapesARoutedNameOnce(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/devices/{name}", func(w http.ResponseWriter, r *http.Request) {
		href, err := hrefFor(r, "/devices/{name}")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(href))
	})
	srv := httptest.NewServer(router)
	defer srv.Close()
	// The last three hold a literal %. With a slash beside it (a/%b,
	// a/%2Fb) they pass only while chi records the value escaped: a chi
	// that decoded it would hand over a/%b, which does not decode again, and
	// a/%2Fb, which decodes to a/b, so a release of chi that changed this
	// fails here rather than in a link. a%2Fb alone has no RawPath, so chi
	// routes it decoded and it must not be decoded twice.
	for _, name := range []string{"sw/1", "a b", "a?b", "a#b", "a;b", "a b/c", "%2E%2E", "%2F", "50%", "a%2Fb", "a/%b", "a/%2Fb"} {
		want := "/devices/" + url.PathEscape(name)
		resp, err := srv.Client().Get(srv.URL + want)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d %q, %v", want, resp.StatusCode, body, err)
			continue
		}
		if string(body) != want {
			t.Errorf("device %q requested as %s links to %s", name, want, body)
		}
	}
}

// TestHrefFor_RefusesAnUndecodableEscapedValue: a value taken from an
// escaped path that does not decode cannot be named in a link, and guessing
// would name some other record.
func TestHrefFor_RefusesAnUndecodableEscapedValue(t *testing.T) {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "a%zz")
	r := httptest.NewRequest(http.MethodGet, "/jobs/a%2Fb", nil)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	if got, err := hrefFor(r, "/jobs/{id}"); err == nil {
		t.Fatalf("hrefFor() = %q, want an error for a value that does not decode", got)
	}
}

// TestHrefFor_SkipsWildcardKeys. chi records a "*" key for a wildcard match;
// validateRoutes refuses wildcard patterns outright, so one appearing here
// means the request matched something this builder cannot describe, and
// substituting it would produce a nonsense URL.
func TestHrefFor_SkipsWildcardKeys(t *testing.T) {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("*", "anything/at/all")
	rctx.URLParams.Add("id", "abc")

	r := httptest.NewRequest(http.MethodGet, "/irrelevant", nil)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	got, err := hrefFor(r, "/jobs/{id}")
	if err != nil {
		t.Fatalf("hrefFor() = %v, want nil", err)
	}
	if got != "/jobs/abc" {
		t.Errorf("hrefFor() = %q, want the wildcard ignored", got)
	}
}

// TestHrefFor_RefusesAnInconsistentRouteContext. Keys and values falling out
// of step is a chi-internal invariant this package cannot repair, and
// guessing would emit a link pointing somewhere real and wrong.
func TestHrefFor_RefusesAnInconsistentRouteContext(t *testing.T) {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Keys = []string{"id", "orphan"}
	rctx.URLParams.Values = []string{"abc"}

	r := httptest.NewRequest(http.MethodGet, "/irrelevant", nil)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	if _, err := hrefFor(r, "/jobs/{id}"); err == nil {
		t.Fatal("hrefFor() accepted a route context with more keys than values")
	}
}
