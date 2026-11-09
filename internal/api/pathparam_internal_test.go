// Package api: PathParam and its helpers against chi's two forms of a
// recorded value, still escaped from a RawPath and already decoded.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
)

// routedRequest is a request whose route context records key as value, the
// way chi records it: still escaped when rawPath is set, decoded otherwise.
func routedRequest(rawPath, key, value string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.URL.RawPath = rawPath
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestPathParam_DecodesOnlyWhatChiRecordedEscaped holds PathParam to chi's
// two forms: a value from a RawPath is decoded once, and a value from the
// decoded path is returned as it is, so a literal % survives.
func TestPathParam_DecodesOnlyWhatChiRecordedEscaped(t *testing.T) {
	for _, tc := range []struct {
		name, rawPath, value, want string
		wantErr                    bool
	}{
		{name: "escaped slash from a RawPath", rawPath: "/d/sw%2F1", value: "sw%2F1", want: "sw/1"},
		{name: "escaped percent from a RawPath", rawPath: "/d/sw%2F50%25", value: "sw%2F50%25", want: "sw/50%"},
		{name: "literal percent with no RawPath", value: "50%", want: "50%"},
		{name: "literal escape sequence with no RawPath", value: "a%2Fb", want: "a%2Fb"},
		{name: "broken escape from a RawPath", rawPath: "/d/a%zz", value: "a%zz", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PathParam(routedRequest(tc.rawPath, "name", tc.value), "name")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("PathParam() = %q, want an error for an escape that does not decode", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("PathParam() = %q, %v, want %q, nil", got, err, tc.want)
			}
		})
	}
}

// TestPathParam_HelpersAnswerAnUndecodableValue covers each helper's refusal:
// pathParam answers 400 itself, and pathID and pathUUID return an error the
// caller words.
func TestPathParam_HelpersAnswerAnUndecodableValue(t *testing.T) {
	broken := func() *http.Request { return routedRequest("/d/a%zz", "id", "a%zz") }

	w := httptest.NewRecorder()
	if v, ok := pathParam(w, broken(), "id"); ok || v != "" {
		t.Errorf("pathParam() = %q, %v, want \"\", false", v, ok)
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("pathParam answered %d, want 400", w.Code)
	}
	if _, err := pathID(broken(), "id"); err == nil {
		t.Error("pathID accepted an escape that does not decode")
	}
	if _, err := pathUUID(broken(), "id"); err == nil {
		t.Error("pathUUID accepted an escape that does not decode")
	}

	for _, raw := range []string{"0", "-3", "seven"} {
		if _, err := pathID(routedRequest("", "id", raw), "id"); err == nil {
			t.Errorf("pathID accepted %q, which is not a positive integer", raw)
		}
	}
	if id, err := pathID(routedRequest("/d/%31%32", "id", "%31%32"), "id"); err != nil || id != 12 {
		t.Errorf("pathID(%%31%%32) = %d, %v, want 12, nil: an escaped digit is the digit", id, err)
	}
	const job = "0190a8b4-5c3e-7f00-8000-000000000001"
	if got, err := pathUUID(routedRequest("", "id", job), "id"); err != nil || got != job {
		t.Errorf("pathUUID(%s) = %q, %v, want the id back", job, got, err)
	}
	if _, err := pathUUID(routedRequest("", "id", "not-a-uuid"), "id"); err == nil {
		t.Error("pathUUID accepted a value that is not a UUID")
	}
}

// TestRoutedPath_IsThePathChiMatched pins the path the 405 handler splits:
// RawPath when the request has one, else Path.
func TestRoutedPath_IsThePathChiMatched(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/d/sw%2F1", nil)
	if got := routedPath(r); got != "/d/sw%2F1" {
		t.Errorf("routedPath() = %q, want the RawPath", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/d/plain", nil)
	if got := routedPath(r); got != "/d/plain" {
		t.Errorf("routedPath() = %q, want the Path", got)
	}
}

// TestSentPath_IsWhatChiMatchedAndAValidEscaping holds SentPath to the path a
// request was routed on, and to a valid escaping of it: a raw | is encoded,
// every existing escape (%2F, %6E) is kept, and a request with no RawPath is
// its own default escaping. EscapedPath alone splits the first row's %2F.
func TestSentPath_IsWhatChiMatchedAndAValidEscaping(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"/devices/a%2Fb|c", "/devices/a%2Fb%7Cc"},
		{"/devices/sw%2F1", "/devices/sw%2F1"},
		{"/devices/%6Eew", "/devices/%6Eew"},
		{"/devices/plain", "/devices/plain"},
		{"/devices/50%25", "/devices/50%25"},
		{"/devices/caf%C3%A9", "/devices/caf%C3%A9"},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.target, nil)
		got := SentPath(r.URL)
		if got != tc.want {
			t.Errorf("SentPath(%s) = %q, want %q", tc.target, got, tc.want)
		}
		if back, err := url.PathUnescape(got); err != nil || back != r.URL.Path {
			t.Errorf("SentPath(%s) = %q decodes to %q, %v; want the request's Path %q", tc.target, got, back, err, r.URL.Path)
		}
	}
}
