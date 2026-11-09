// Tests that the UI serves no page for a record id no URL can address, and
// returns a write on one to its list rather than to a resolved-away path.
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRoutes_RefuseADotSegment: a request naming a record "." or ".." (sent
// unresolved, as only a non-browser client can) is 404 through the real
// router, however the segment is escaped, and so is one naming the empty id
// with an empty segment. Each used to render that record's page, every
// link of which was resolved to some other page or was empty.
func TestRoutes_RefuseADotSegment(t *testing.T) {
	p := newRecordProbe(t)
	for _, target := range []string{
		"/ui/" + gadgetView + "/..",
		"/ui/" + gadgetView + "/.",
		"/ui/" + gadgetView + "/%2E%2E",
		"/ui/" + gadgetView + "/%2e",
		"/ui/" + gadgetView + "/alpha/../edit",
		"/ui/" + gadgetView + "/alpha/detach/..",
		"/ui/" + gadgetView + "//edit",
	} {
		if rec := p.get(t, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
}

// TestRefuseDotSegments_PassesEveryOtherPath holds the middleware to its own
// claim: a path with no dot segment and no empty interior segment reaches
// the handler behind it, unchanged. That includes a%2F..%2Fb, which is one
// segment holding dots between escaped slashes, and the trailing slash of
// the index. Whether the handler then finds the record a%2F..%2Fb names is
// the handler's to answer, not this middleware's.
func TestRefuseDotSegments_PassesEveryOtherPath(t *testing.T) {
	h := New(Config{})
	var reached string
	guarded := h.refuseDotSegments(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.EscapedPath()
	}))
	for _, target := range []string{"/ui/", "/ui/gadgets/a%2F..%2Fb", "/ui/gadgets/alpha", "/ui/gadgets/alpha/edit", "/ui/gadgets/..x", "/ui/gadgets/a.b"} {
		reached = ""
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if reached != target {
			t.Errorf("GET %s reached the handler as %q, want it passed on unchanged (status %d)", target, reached, rec.Code)
		}
	}
	for _, target := range []string{"/ui/gadgets/..", "/ui/gadgets/%2e%2E/edit", "/ui/gadgets//edit"} {
		reached = ""
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if reached != "" || rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d and reached %q, want a 404 that reaches nothing", target, rec.Code, reached)
		}
	}
}

// TestRoutes_RefuseADotSegmentBeforeSignIn: the refusal runs before the
// session is checked, so an anonymous request gets the same plain 404,
// rendered without a session and naming nothing, while an anonymous
// request for a real page is still sent to sign in.
func TestRoutes_RefuseADotSegmentBeforeSignIn(t *testing.T) {
	p := newRecordProbe(t)
	rec := p.serve(httptest.NewRequest(http.MethodGet, "/ui/"+gadgetView+"/..", nil))
	if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != "not found" {
		t.Errorf("anonymous GET of a dot segment = %d %q, want a plain 404", rec.Code, rec.Body.String())
	}
	rec = p.serve(httptest.NewRequest(http.MethodGet, "/ui/"+gadgetView+"/alpha", nil))
	if rec.Code == http.StatusOK || rec.Code == http.StatusNotFound {
		t.Errorf("anonymous GET of a record = %d, want it sent to sign in", rec.Code)
	}
}

// TestResourcePath_ReturnsAnUnaddressableRecordToItsList: a write's redirect
// to a record named ".." used to be path.Join's /ui, and one named "." the
// list by accident. Each now returns to the list on purpose, and any other
// id is one escaped segment.
func TestResourcePath_ReturnsAnUnaddressableRecordToItsList(t *testing.T) {
	for id, want := range map[string]string{
		"..":    "/ui/devices",
		".":     "/ui/devices",
		"":      "/ui/devices",
		"sw/1":  "/ui/devices/sw%2F1",
		"../..": "/ui/devices/..%2F..",
	} {
		if got := resourcePath("/ui", "devices", id); got != want {
			t.Errorf("resourcePath(%q) = %q, want %q", id, got, want)
		}
	}
}
