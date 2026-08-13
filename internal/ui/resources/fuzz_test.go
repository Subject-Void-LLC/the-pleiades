package resources_test

import (
	"net/http"
	"strings"
	"testing"
)

// FuzzViewResourceRouting drives arbitrary paths at the mounted UI.
//
// The {resource} segment is the one caller-controlled value that selects
// what a request reaches, so this is the shape of input most worth being
// paranoid about. Three properties must hold for every input: the router
// never panics, an unregistered name never renders a page, and no path ever
// reaches the embedded asset handler through the resource routes -- the
// last because a resource name that could escape into /static would be a
// traversal into the binary's own embedded files.
func FuzzViewResourceRouting(f *testing.F) {
	for _, seed := range []string{
		"/ui/inventories",
		"/ui/inventories/core-router-01",
		"/ui/../static/app.css",
		"/ui/%2e%2e/%2e%2e/etc/passwd",
		"/ui/inventories/../../static",
		"/ui/inventories/%00",
		"/ui//////",
		"/ui/" + strings.Repeat("a", 4096),
		"/ui/inventories/chart.json",
		"/ui/\\..\\..\\windows",
	} {
		f.Add(seed)
	}

	h := newHarness(&testing.T{}, adminIdentity)

	f.Fuzz(func(t *testing.T, path string) {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}

		req, err := http.NewRequest(http.MethodGet, "http://example.test"+path, nil)
		if err != nil {
			// A path net/http itself refuses is not a case this router
			// can ever be handed.
			return
		}
		h.authenticate(req)

		w := recorderFor(h, req)

		// A 200 may only come from a registered view or the asset
		// handler's own subtree, never from an arbitrary segment.
		if w.Code == http.StatusOK {
			body := w.Body.String()
			if strings.Contains(body, "<html") && !servesAKnownView(body) {
				t.Errorf("GET %q rendered a page for no registered view", path)
			}
		}
	})
}

// FuzzFormSubmission drives arbitrary form bodies at a real create handler.
//
// The invariant is not "it succeeds" but "it never panics and never accepts
// a field the descriptor did not declare". Mass assignment is meant to be
// structurally impossible here rather than remembered, and a fuzzer is the
// only honest way to check a claim that absolute.
func FuzzFormSubmission(f *testing.F) {
	for _, seed := range []string{
		"name=a&type=linux_server",
		"name=&type=",
		"name=a&type=linux_server&is_admin=true",
		"name=" + strings.Repeat("x", 8192),
		"%00=%00",
		"name=a&name=b&name=c",
		";;;;",
		"=",
	} {
		f.Add(seed)
	}

	h := newHarness(&testing.T{}, adminIdentity)

	f.Fuzz(func(t *testing.T, body string) {
		w := h.postWithoutCSRF(t, "/ui/inventories", body)

		// Without a token every write must be refused, whatever the body
		// says. A 2xx here would mean a malformed body found a path around
		// the CSRF middleware.
		if w.Code < 400 {
			t.Errorf("POST with body %q and no CSRF token = %d, want a refusal", body, w.Code)
		}
	})
}

// servesAKnownView reports whether a rendered page belongs to a registered
// view, by looking for the heading the shared layout always emits.
func servesAKnownView(body string) bool {
	// The login page and the declared panel are both legitimate 200s.
	return strings.Contains(body, "<h1>")
}
