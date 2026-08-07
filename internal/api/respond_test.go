package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// This file covers the encoder seam that replaced Phase 13's
// response-rewriting middleware. Several tests here are regression tests
// for defects that middleware actually had, recorded as
// FAILURE_PATTERNS.md #70 through #73 and reproduced against the real code
// before it was deleted; each names its entry.

// payload is a response DTO carrying a large integer, which is the value
// FAILURE_PATTERNS.md #71 records being corrupted in transit.
type payload struct {
	api.LinkSet

	Count int64  `json:"count"`
	Name  string `json:"name"`
}

// failingGenerator reports that the authorization decision could not be
// reached, the outage case FAILURE_PATTERNS.md #73 is about.
type failingGenerator struct{}

func (failingGenerator) Permitted(context.Context, *auth.Identity, []auth.Affordance) ([]auth.LinkRel, error) {
	return nil, io.ErrUnexpectedEOF
}

// inventingGenerator returns a relation it was never offered, the shape a
// buggy or hostile third-party implementation would take.
type inventingGenerator struct{}

func (inventingGenerator) Permitted(_ context.Context, _ *auth.Identity, candidates []auth.Affordance) ([]auth.LinkRel, error) {
	rels := make([]auth.LinkRel, 0, len(candidates)+1)
	for _, c := range candidates {
		rels = append(rels, c.Rel)
	}
	return append(rels, auth.LinkRel("root-shell")), nil
}

// respondRouter builds a router whose single route responds through
// Respond, so the seam is exercised in its real position rather than
// called directly.
func respondRouter(t *testing.T, generator auth.HATEOASGenerator, status int, body payload) http.Handler {
	t.Helper()
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   generator,
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/thing/{id}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf,
				Handler: func(w http.ResponseWriter, r *http.Request) {
					p := body
					api.Respond(w, r, status, &p)
				}},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestRespond_ForwardsEveryStatusCode(t *testing.T) {
	// IMPLEMENTATION.md's Phase 13 checklist asserted the code this
	// replaced "captures the status code but never forwards it," so "any
	// handler returning a non-200 status is currently reported as 200."
	// That claim did not reproduce (FAILURE_PATTERNS.md #74). The property
	// is nonetheless the one this seam has to hold, so it is asserted here
	// directly rather than inherited from a claim about deleted code.
	for _, status := range []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusAccepted,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusInternalServerError,
	} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			router := respondRouter(t, allowAllGenerator(t), status, payload{Count: 1, Name: "x"})
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

			if rr.Code != status {
				t.Errorf("handler wrote %d, client observed %d", status, rr.Code)
			}
		})
	}
}

func TestRespond_PreservesIntegersPastFloat64Precision(t *testing.T) {
	// FAILURE_PATTERNS.md #71: the deleted middleware decoded every body
	// into map[string]interface{}, where encoding/json represents each
	// number as a float64, then re-encoded it. 9007199254740993 came back
	// as 9007199254740992. The seam marshals the typed value once and
	// never decodes it, so there is no round trip to lose anything in.
	const beyondFloat64 int64 = 9007199254740993

	router := respondRouter(t, allowAllGenerator(t), http.StatusOK, payload{Count: beyondFloat64, Name: "x"})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

	// Decode into json.Number, not interface{}, or the test would
	// reintroduce the very precision loss it is checking for.
	dec := json.NewDecoder(rr.Body)
	dec.UseNumber()
	var got struct {
		Count json.Number `json:"count"`
	}
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Count.String() != strconv.FormatInt(beyondFloat64, 10) {
		t.Errorf("count arrived as %s, want %d", got.Count.String(), beyondFloat64)
	}
}

func TestRespond_AbsentLinksMeansCouldNotComputeAndEmptyMeansNothingPermitted(t *testing.T) {
	// FAILURE_PATTERNS.md #73: an authorization backend outage must not be
	// indistinguishable from a caller who is legitimately allowed nothing.
	// The wire carries three states and this asserts two of them are
	// distinct, which a []Link with omitempty could not express.
	t.Run("generator fails: key absent", func(t *testing.T) {
		router := respondRouter(t, failingGenerator{}, http.StatusOK, payload{Name: "x"})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

		if rr.Code != http.StatusOK {
			t.Errorf("status is %d, want 200: the resource's own data is not wrong just because its affordances are unknown", rr.Code)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		if _, present := raw["_links"]; present {
			t.Errorf("_links is present after a generator failure; absence is what distinguishes 'could not compute' from 'you may do nothing': %s", rr.Body.String())
		}
	})

	t.Run("nothing permitted: empty array", func(t *testing.T) {
		gen, err := auth.NewAdmissionHATEOASGenerator(auth.AdmissionChain{denyAllRule{}})
		if err != nil {
			t.Fatalf("building generator: %v", err)
		}
		router := respondRouter(t, gen, http.StatusOK, payload{Name: "x"})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		links, present := raw["_links"]
		if !present {
			t.Fatalf("_links is absent when the decision succeeded and permitted nothing: %s", rr.Body.String())
		}
		if string(links) != "[]" {
			t.Errorf("_links is %s, want []", string(links))
		}
	})
}

// denyAllRule refuses everything, so the generator succeeds and permits
// nothing, which is a different fact from failing to decide.
type denyAllRule struct{}

func (denyAllRule) Check(context.Context, *auth.Identity, auth.AdmissionRequest) (auth.Effect, error) {
	return auth.EffectDeny, nil
}

func TestRespond_NeverAdvertisesARelationTheRouterDoesNotServe(t *testing.T) {
	// The port returns relation names rather than links precisely so an
	// implementation cannot invent a URL. linksFor re-intersects against
	// its own candidates, which is where that guarantee is enforced.
	router := respondRouter(t, inventingGenerator{}, http.StatusOK, payload{Name: "x"})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

	if got := rr.Body.String(); strings.Contains(got, "root-shell") {
		t.Errorf("a relation the route table never declared reached the response body: %s", got)
	}
}

func TestRespond_SetsAccurateContentLength(t *testing.T) {
	router := respondRouter(t, allowAllGenerator(t), http.StatusOK, payload{Count: 7, Name: "x"})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

	declared := rr.Header().Get("Content-Length")
	if declared == "" {
		t.Fatal("no Content-Length header")
	}
	n, err := strconv.Atoi(declared)
	if err != nil {
		t.Fatalf("Content-Length %q is not a number: %v", declared, err)
	}
	// The deleted middleware grew the body after a handler had already set
	// a length, so this asserts the length describes what was actually
	// written, links included.
	if n != rr.Body.Len() {
		t.Errorf("Content-Length is %d but the body is %d bytes", n, rr.Body.Len())
	}
}

func TestRespondError_CarriesNoLinks(t *testing.T) {
	// Enumerating affordances on a refusal would hand an unauthorized
	// caller a map of what exists, the same reasoning /readyz applies to
	// dependency names.
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/thing/{id}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf,
				Handler: func(w http.ResponseWriter, r *http.Request) {
					api.RespondError(w, r, http.StatusForbidden, "forbidden")
				}},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/thing/abc", nil))

	if rr.Code != http.StatusForbidden {
		t.Errorf("status is %d, want 403", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "_links") {
		t.Errorf("error response carries affordances: %s", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type is %q, want application/json", ct)
	}
}

func TestRespond_WorksWithNoLinkBuilderInContext(t *testing.T) {
	// A handler a test calls directly, or one mounted outside NewRouter,
	// still has to be able to answer. Absence of the builder is not an
	// error condition, it just means no affordances can be computed.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/thing/abc", nil)

	p := payload{Count: 3, Name: "direct"}
	api.Respond(rr, req, http.StatusOK, &p)

	if rr.Code != http.StatusOK {
		t.Errorf("status is %d, want 200", rr.Code)
	}
	var got payload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Name != "direct" || got.Count != 3 {
		t.Errorf("payload did not round trip: %+v", got)
	}
}
