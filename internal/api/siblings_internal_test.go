// Package api: a link to a record whose id equals a fixed segment the route
// table registers beside that id routes to the record, not to the other
// handler.
package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestHrefFor_ALinkToAValueEqualToAFixedSegmentReachesItsOwnRoute routes
// real requests through chi, where /things/new is registered beside
// /things/{id}. The record named "new", requested as /things/%6Eew, is
// served by {id}; its link must name the same route, and following it must
// reach {id} again rather than the fixed "new" route.
func TestHrefFor_ALinkToAValueEqualToAFixedSegmentReachesItsOwnRoute(t *testing.T) {
	statics := staticSiblings([]string{"/things/{id}", "/things/new"})
	router := chi.NewRouter()
	router.Get("/things/new", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the fixed route"))
	})
	router.Get("/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		href, err := hrefFor(r, "/things/{id}", statics["/things/{id}"])
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(href))
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	get := func(path string) string {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	link := get("/things/%6Eew")
	if link != "/things/%6Eew" {
		t.Fatalf("the record named \"new\" links to %s, want /things/%%6Eew", link)
	}
	if again := get(link); again != link {
		t.Fatalf("following %s reached %q, not the record's own route", link, again)
	}
	if got := get("/things/news"); got != "/things/news" {
		t.Errorf("a name beside the fixed segment links to %s, want it as it is", got)
	}
}

// TestStaticSiblings_ReadsEachShapeOfPattern tables the derivation: a
// sibling under a parameter of the prefix, a parameter with a pattern, and
// what is not a sibling (a deeper fixed segment, another prefix).
func TestStaticSiblings_ReadsEachShapeOfPattern(t *testing.T) {
	statics := staticSiblings([]string{
		"/devices/{name}/facts/{key}",
		"/devices/{device}/facts/summary",
		"/things/{id:[0-9]+}",
		"/things/new",
		"/things/new/deeper",
		"/other/{id}",
	})
	for _, tc := range []struct {
		pattern, key, sibling string
		want                  bool
	}{
		{"/devices/{name}/facts/{key}", "key", "summary", true},
		{"/devices/{name}/facts/{key}", "name", "summary", false},
		{"/things/{id:[0-9]+}", "id", "new", true},
		{"/other/{id}", "id", "new", false},
	} {
		if got := statics[tc.pattern][tc.key][tc.sibling]; got != tc.want {
			t.Errorf("staticSiblings: %s's {%s} has sibling %q = %v, want %v", tc.pattern, tc.key, tc.sibling, got, tc.want)
		}
	}
}
