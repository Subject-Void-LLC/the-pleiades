// Tests that the UI serves no page for a record id no URL can address, and
// returns a write on one to its list rather than to a resolved-away path.
package web

import (
	"net/http"
	"testing"
)

// TestRoutes_RefuseADotSegment: a request naming a record "." or ".." (sent
// unresolved, as only a non-browser client can) is 404, however the segment
// is escaped. It used to render that record's page, every link of which
// path.Join resolved to some other page. A record whose one segment only
// holds dots between escaped slashes is still served.
func TestRoutes_RefuseADotSegment(t *testing.T) {
	p := newRecordProbe(t)
	for _, target := range []string{
		"/ui/" + gadgetView + "/..",
		"/ui/" + gadgetView + "/.",
		"/ui/" + gadgetView + "/%2E%2E",
		"/ui/" + gadgetView + "/%2e",
		"/ui/" + gadgetView + "/alpha/../edit",
		"/ui/" + gadgetView + "/alpha/detach/..",
	} {
		if rec := p.get(t, target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}
	for _, target := range []string{"/ui/" + gadgetView + "/a%2F..%2Fb", "/ui/" + gadgetView + "/alpha"} {
		if rec := p.get(t, target); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", target, rec.Code)
		}
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
