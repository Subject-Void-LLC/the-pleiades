// Package web: an appearance change made on a record's page returns to that
// page, by the address the page was opened at.
package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

// returnValue finds the hidden return field an appearance form carries.
var returnValue = regexp.MustCompile(`name="` + returnField + `" value="([^"]*)"`)

// TestThemeChange_ReturnsToTheRecordItWasMadeOn opens the page of a record
// whose name holds a "/", reads the return address the page's theme form
// carries, posts the theme change with it, and expects to land back on the
// same page. Rebuilt from the decoded path, the address named
// /ui/gadgets/sw/1, which is the action "1" of the record "sw".
func TestThemeChange_ReturnsToTheRecordItWasMadeOn(t *testing.T) {
	p := newRecordProbe(t)
	page := "/ui/" + gadgetView + "/" + url.PathEscape("sw/1")

	rec := p.get(t, page+"?tab=details")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", page, rec.Code)
	}
	m := returnValue.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("the page at %s carries no %s field", page, returnField)
	}
	carried := html.UnescapeString(m[1])
	if carried != page+"?tab=details" {
		t.Errorf("the page carries the return address %q, want %q", carried, page+"?tab=details")
	}

	form := url.Values{returnField: {carried}, "theme": {"dark"}, session.CSRFField: {p.csrf(t)}}
	r := httptest.NewRequest(http.MethodPost, "/ui/theme", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := p.serve(p.authed(r))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /ui/theme = %d, want 303: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != page+"?tab=details" {
		t.Errorf("the theme change returned to %s, want %s", got, page+"?tab=details")
	}
}
