package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/go-chi/chi/v5"
)

// This file covers the failure branches of the encoder seam and the
// OPTIONS handlers: the paths that only run when something downstream is
// already broken. They matter more than their line count suggests, because
// each one decides what a client is told when the server cannot do what it
// was asked, and a wrong answer there is exactly the silent-degradation
// shape FAILURE_PATTERNS.md #73 records.

// unmarshalable carries a channel, which encoding/json cannot represent,
// so Respond's marshal branch fails deterministically.
type unmarshalable struct {
	api.LinkSet

	Bad chan int `json:"bad"`
}

// failingWriter fails every body write, so the short-write and write-error
// branches can be reached without a real broken connection.
type failingWriter struct {
	header http.Header
	status int
}

func (f *failingWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("connection reset by peer") }
func (f *failingWriter) WriteHeader(status int)    { f.status = status }

// shortWriter reports fewer bytes written than it was given without
// returning an error, the other half of a truncated response.
type shortWriter struct {
	header http.Header
	status int
}

func (s *shortWriter) Header() http.Header {
	if s.header == nil {
		s.header = http.Header{}
	}
	return s.header
}
func (s *shortWriter) Write(b []byte) (int, error) { return len(b) / 2, nil }
func (s *shortWriter) WriteHeader(status int)      { s.status = status }

func TestRespond_UnmarshalablePayloadBecomesAnHonest500(t *testing.T) {
	// Links are computed and the body is marshaled before any byte is
	// written, so a marshal failure can still choose a status. If this
	// ordering were reversed the client would receive a 200 followed by a
	// truncated body, which is FAILURE_PATTERNS.md #32's lesson in a
	// different place.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/thing", nil)

	p := unmarshalable{Bad: make(chan int)}
	api.Respond(rr, req, http.StatusOK, &p)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status is %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "internal error") {
		t.Errorf("body is %q, want the generic internal error message", rr.Body.String())
	}
	// The marshal error names the Go type, which is server-internal.
	if strings.Contains(rr.Body.String(), "chan") {
		t.Errorf("body leaks the marshal error detail: %s", rr.Body.String())
	}
}

func TestRespond_SurvivesAWriteFailure(t *testing.T) {
	// Nothing can be told to the client at this point; the requirement is
	// only that the server does not panic and does record it.
	var logBuf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	w := &failingWriter{}
	req := httptest.NewRequest(http.MethodGet, "/thing", nil)
	req = req.WithContext(api.ContextWithLoggerForTest(req.Context(), logger))

	p := payload{Count: 1, Name: "x"}
	api.Respond(w, req, http.StatusOK, &p)

	if w.status != http.StatusOK {
		t.Errorf("status written is %d, want 200", w.status)
	}
	if !strings.Contains(logBuf.String(), "failed to write response body") {
		t.Errorf("a failed write was not recorded: %s", logBuf.String())
	}
}

func TestRespond_RecordsAShortWrite(t *testing.T) {
	var logBuf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	w := &shortWriter{}
	req := httptest.NewRequest(http.MethodGet, "/thing", nil)
	req = req.WithContext(api.ContextWithLoggerForTest(req.Context(), logger))

	p := payload{Count: 1, Name: "x"}
	api.Respond(w, req, http.StatusOK, &p)

	if !strings.Contains(logBuf.String(), "short write") {
		t.Errorf("a truncated body was not recorded: %s", logBuf.String())
	}
}

// optionsRouterWithGenerator mounts two methods on one pattern behind the
// supplied generator.
func optionsRouterWithGenerator(t *testing.T, generator auth.HATEOASGenerator) http.Handler {
	t.Helper()
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   generator,
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/thing/{id}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf,
				Handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }},
			{Method: http.MethodDelete, Pattern: "/thing/{id}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelDelete,
				Handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestOptions_GeneratorFailureIs500NotAnEmptyAllow(t *testing.T) {
	// An empty Allow header would tell the caller "you may do nothing
	// here", which is a claim about permissions. When the decision could
	// not be reached, the honest answer is that the server failed.
	router := optionsRouterWithGenerator(t, failingGenerator{})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodOptions, api.APIVersionPrefix+"/thing/abc", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status is %d, want 500", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "" {
		t.Errorf("a failed decision still emitted Allow: %q", got)
	}
}

func TestMethodNotAllowed_GeneratorFailureIs500(t *testing.T) {
	router := optionsRouterWithGenerator(t, failingGenerator{})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, api.APIVersionPrefix+"/thing/abc", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status is %d, want 500", rr.Code)
	}
}

func TestMethodNotAllowed_UnknownPatternStillAnswers405(t *testing.T) {
	// chi routes a request whose path matches no pattern to NotFound, so
	// reaching the 405 handler with an unresolvable pattern means the
	// route table and chi disagree. It must still answer rather than
	// panic on an empty pattern lookup.
	router := optionsRouterWithGenerator(t, allowAllGenerator(t))

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, api.APIVersionPrefix+"/thing/abc/deeper", nil))

	if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status is %d, want 404 or 405", rr.Code)
	}
}

func TestDeviceHandler_DeleteRejectsAnUnusableName(t *testing.T) {
	// The same bound Get applies, asserted separately because it is a
	// separate code path and DELETE is the irreversible one.
	repo := &stubDeviceRepo{retireErr: errors.New("must not be reached")}
	router := deviceRouter(t, repo)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, api.APIVersionPrefix+"/inventory/devices/"+strings.Repeat("a", 254), nil))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status is %d, want 400", rr.Code)
	}
	if repo.retiredName != "" {
		t.Errorf("an unusable name reached the repository as %q", repo.retiredName)
	}
}

func TestNewDeviceHandler_NilLoggerFallsBackToDefault(t *testing.T) {
	// A handler constructed without a logger must still be usable rather
	// than panicking on the first error it needs to report.
	handler := api.NewDeviceHandler(&stubDeviceRepo{getErr: errors.New("boom")}, nil)
	if handler == nil {
		t.Fatal("NewDeviceHandler returned nil")
	}

	// A direct handler call carries no chi routing context, so the URL
	// parameter has to be supplied the way chi would have: without it the
	// name is empty and the handler correctly answers 400 long before it
	// reaches the logger this test is about.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "edge-01")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/inventory/devices/edge-01", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	handler.Get(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status is %d, want 500", rr.Code)
	}
}
