// Tests that a lab id reaches the controller as the one id it is: escaped
// once in the path, decoded back to the id by a server routing by segment,
// and never sent as a dot segment the server would resolve to another
// route.
package cml_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
)

// escapeController is a CML controller routing by segment. It records every
// request target as it arrived, before routing, and the lab id each lab
// read decoded; its only lab is titled "..".
type escapeController struct {
	*httptest.Server
	mu   sync.Mutex
	uris []string
	lab  string
}

// newEscapeController starts the controller.
func newEscapeController(t *testing.T) *escapeController {
	t.Helper()
	e := &escapeController{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v0/system_information", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"2.7.2","ready":true}`))
	})
	mux.HandleFunc("POST /api/v0/authenticate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"tok"`))
	})
	mux.HandleFunc("GET /api/v0/labs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`["l1"]`))
	})
	mux.HandleFunc("GET /api/v0/labs/{lab}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("lab")
		e.mu.Lock()
		e.lab = id
		e.mu.Unlock()
		if id != "l1" && id != "a b" && id != "a/b" && id != "a?b" && id != "a#b" && id != "%2E%2E" && id != "%2F" {
			http.NotFound(w, r)
			return
		}
		title := id
		if id == "l1" {
			title = ".."
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"lab_title": title, "state": "STOPPED"})
	})
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.uris = append(e.uris, r.RequestURI)
		e.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(e.Close)
	return e
}

// take returns the request targets and the decoded lab id recorded since
// the last take, clearing them.
func (e *escapeController) take() ([]string, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	uris, lab := e.uris, e.lab
	e.uris, e.lab = nil, ""
	return uris, lab
}

// TestLab_SendsTheIDEscapedOnce: the lab id's segment is url.PathEscape's,
// exactly, and the controller decodes it back to the id. A ref of ".."
// used to go out as a dot segment, which the controller resolves to
// /api/v0/ rather than to a lab.
func TestLab_SendsTheIDEscapedOnce(t *testing.T) {
	e := newEscapeController(t)
	c, err := cml.New(e.URL, e.Client(), cml.Credentials{Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	e.take()
	for _, tc := range []struct {
		id      string
		refused bool
	}{
		{"a b", false},
		{"a/b", false},
		{"a?b", false},
		{"a#b", false},
		{"..", true},
		{"%2E%2E", false},
		{"%2F", false},
	} {
		lab, err := c.Lab(ctx, tc.id)
		uris, decoded := e.take()
		if tc.refused {
			if err == nil || len(uris) != 0 {
				t.Errorf("Lab(%q) sent %q, want a refusal before any request", tc.id, uris)
			}
			continue
		}
		if err != nil || lab.Title != tc.id {
			t.Errorf("Lab(%q) = %+v, %v", tc.id, lab, err)
			continue
		}
		if want := "/api/v0/labs/" + url.PathEscape(tc.id); len(uris) != 1 || uris[0] != want {
			t.Errorf("Lab(%q) was sent as %q, want %q", tc.id, uris, want)
		}
		if decoded != tc.id {
			t.Errorf("Lab(%q) reached the controller as lab %q", tc.id, decoded)
		}
	}
}

// TestFindLab_FindsADotTitleByItsTitle: no lab id is a dot segment, so a ref
// of ".." is looked for among the titles at once, and found there, without
// a request naming it as an id.
func TestFindLab_FindsADotTitleByItsTitle(t *testing.T) {
	e := newEscapeController(t)
	c, err := cml.New(e.URL, e.Client(), cml.Credentials{Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	e.take()
	lab, err := c.FindLab(ctx, "..")
	if err != nil || lab.ID != "l1" {
		t.Fatalf("FindLab(..) = %+v, %v, want lab l1 by its title", lab, err)
	}
	uris, _ := e.take()
	if want := []string{"/api/v0/labs", "/api/v0/labs/l1"}; len(uris) != 2 || uris[0] != want[0] || uris[1] != want[1] {
		t.Errorf("FindLab(..) sent %q, want %q", uris, want)
	}
}
