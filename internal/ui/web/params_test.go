// Package web: every UI route answers a path whose escape does not decode
// with 400, whichever parameter holds it.
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRoutes_AnswerAnUndecodableEscapeWith400 sends each route a RawPath
// holding a broken escape in each parameter it reads. net/http never hands
// a handler such a path (it answers 400 itself), so this is the backstop
// for a request built some other way, and the answer must match: 400, not
// a 404 claiming no such record, and never the value echoed.
func TestRoutes_AnswerAnUndecodableEscapeWith400(t *testing.T) {
	p := newRecordProbe(t)
	for _, tc := range []struct {
		name    string
		method  string
		rawPath string
		form    string
	}{
		{"list resource", http.MethodGet, "/ui/gad%zz", ""},
		{"chart resource", http.MethodGet, "/ui/gad%zz/chart.json", ""},
		{"detail id", http.MethodGet, "/ui/" + gadgetView + "/a%zz", ""},
		{"edit id", http.MethodGet, "/ui/" + gadgetView + "/a%zz/edit", ""},
		{"stream id", http.MethodGet, "/ui/" + gadgetView + "/a%zz/logs", ""},
		{"download resource", http.MethodGet, "/ui/gad%zz/alpha/download/report", ""},
		{"download id", http.MethodGet, "/ui/" + gadgetView + "/a%zz/download/report", ""},
		{"download format", http.MethodGet, "/ui/" + gadgetView + "/alpha/download/rep%zz", ""},
		{"update id", http.MethodPost, "/ui/" + gadgetView + "/a%zz", "name=alpha"},
		{"delete id", http.MethodPost, "/ui/" + gadgetView + "/a%zz", "_method=DELETE"},
		{"action resource", http.MethodGet, "/ui/gad%zz/alpha/run", ""},
		{"action name", http.MethodGet, "/ui/" + gadgetView + "/alpha/r%zz", ""},
		{"action form id", http.MethodGet, "/ui/" + gadgetView + "/a%zz/run", ""},
		{"run action id", http.MethodPost, "/ui/" + gadgetView + "/a%zz/run", "group=core"},
		{"row action resource", http.MethodGet, "/ui/gad%zz/alpha/detach/r1", ""},
		{"row action name", http.MethodGet, "/ui/" + gadgetView + "/alpha/det%zz/r1", ""},
		{"row action id", http.MethodGet, "/ui/" + gadgetView + "/a%zz/detach/r1", ""},
		{"row action row", http.MethodGet, "/ui/" + gadgetView + "/alpha/detach/r%zz", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The decoded path is the raw one with the broken escape
			// dropped, so httptest.NewRequest accepts it; chi routes on
			// the RawPath set below.
			r := httptest.NewRequest(tc.method, strings.ReplaceAll(tc.rawPath, "%zz", ""), strings.NewReader(tc.form))
			r.URL.RawPath = tc.rawPath
			if tc.method == http.MethodPost {
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set(session_CSRFHeader, p.csrf(t))
			}
			w := p.serve(p.authed(r))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s %s = %d, want 400: %s", tc.method, tc.rawPath, w.Code, w.Body.String())
			}
			// The parameter read's own answer, not some later 400 such as
			// a form that would not parse.
			if !strings.Contains(w.Body.String(), "malformed path") {
				t.Errorf("%s %s answered %q, want the parameter read's 400", tc.method, tc.rawPath, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "%zz") {
				t.Errorf("the 400 echoes the caller's value: %s", w.Body.String())
			}
		})
	}
}
