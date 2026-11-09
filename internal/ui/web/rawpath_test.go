// Package web: a path holding a raw | is judged by its segments as chi
// splits them, and returned to by the address it was opened at.
package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRawBytePath_IsReadAsChiSplitsIt opens a record page whose path holds
// an escaped slash beside a raw |. Go keeps that RawPath and chi routes on
// it, one segment; EscapedPath would re-escape the decoded path and split
// the slash. The dot-segment refusal must judge chi's segment, a name
// holding "/../", not refuse a ".." that only the re-escaped form has; and
// the theme form's return address must keep the one segment.
func TestRawBytePath_IsReadAsChiSplitsIt(t *testing.T) {
	p := newRecordProbe(t)
	const sent = "/ui/" + gadgetView + "/x%2F..%2Fy|z"

	rec := p.serve(p.authed(httptest.NewRequest(http.MethodGet, sent, nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: one segment, a name holding \"/../\", is no dot segment", sent, rec.Code)
	}
	m := returnValue.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("the page at %s carries no %s field", sent, returnField)
	}
	if got, want := html.UnescapeString(m[1]), "/ui/"+gadgetView+"/x%2F..%2Fy%7Cz"; got != want {
		t.Errorf("the page carries the return address %q, want %q", got, want)
	}
}
