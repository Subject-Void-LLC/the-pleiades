package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// The benchmarks below exist to price one thing: what RBAC-aware
// hypermedia costs per response.
//
// Read them against BenchmarkAPIMiddleware_SecuredRoute (authz_bench_test.go),
// which is the same router and the same middleware chain with a handler
// that only writes a status code. That is the correct baseline, and it is
// worth stating plainly which comparison is NOT valid:
// BenchmarkRespondWithoutLinks below calls Respond directly, outside the
// router, so the gap between it and BenchmarkRespondWithLinks is mostly
// the middleware chain rather than the links. It is included to isolate
// the encoder, not to price the feature.
//
// The number that actually answers "what do links cost" is the slope
// across affordance counts in BenchmarkRespondWithLinks, because that is
// the only thing varying between its sub-benchmarks.

// benchRouter builds a router with routeCount affordances on one pattern,
// so the per-response cost can be measured against the width of a
// resource's own affordance set rather than the size of the whole table.
func benchRouter(b *testing.B, routeCount int) http.Handler {
	b.Helper()

	methods := []string{http.MethodGet, http.MethodDelete, http.MethodPut, http.MethodPost, http.MethodPatch}
	rels := []auth.LinkRel{auth.RelSelf, auth.RelDelete, auth.RelUpdate, auth.RelCreate, auth.RelExecute}
	scopes := []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite, auth.ScopeRunbookExecute, auth.ScopeJobRead, auth.ScopeInventoryRead}

	routes := make([]api.Route, 0, routeCount)
	for i := 0; i < routeCount; i++ {
		routes = append(routes, api.Route{
			Method:  methods[i%len(methods)],
			Pattern: "/inventory/devices/{name}",
			Scope:   scopes[i%len(scopes)],
			Rel:     rels[i%len(rels)],
			Handler: func(w http.ResponseWriter, r *http.Request) {
				p := payload{Count: 1, Name: "bench"}
				api.Respond(w, r, http.StatusOK, &p)
			},
		})
	}

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(b),
		Routes:    routes,
	})
	if err != nil {
		b.Fatalf("NewRouter: %v", err)
	}
	return router
}

// BenchmarkRespondWithLinks measures a full response through the seam,
// including one admission probe per affordance declared on the resource.
//
// It scales with affordances-per-pattern, not with the size of the route
// table, which is the Pattern Entry Gate's own scalability claim: the
// builder indexes by pattern once at construction, so a table of a
// thousand routes costs the same per response as a table of two, provided
// no single resource declares a thousand actions. That is the bound worth
// stating, and it is the one a REST resource never approaches.
func BenchmarkRespondWithLinks(b *testing.B) {
	for _, affordances := range []int{1, 2, 4} {
		b.Run(strconv.Itoa(affordances), func(b *testing.B) {
			router := benchRouter(b, affordances)
			req := httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/edge-01", nil)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)
			}
		})
	}
}

// BenchmarkRespondWithoutLinks is the baseline: the identical payload
// marshaled and written with no link builder in context, so the delta
// against BenchmarkRespondWithLinks is the price of hypermedia and nothing
// else.
func BenchmarkRespondWithoutLinks(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/inventory/devices/edge-01", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		p := payload{Count: 1, Name: "bench"}
		api.Respond(rr, req, http.StatusOK, &p)
	}
}

// BenchmarkRespondEncodeOnly isolates the encoding cost alone, so the
// baseline above can itself be read against something.
func BenchmarkRespondEncodeOnly(b *testing.B) {
	p := payload{Count: 1, Name: "bench"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(&p); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOptionsHandler prices the pre-flight, which a UI issues once
// per resource it renders controls for and is therefore on a hotter path
// than any single resource read.
func BenchmarkOptionsHandler(b *testing.B) {
	router := benchRouter(b, 2)
	req := httptest.NewRequest(http.MethodOptions, api.APIVersionPrefix+"/inventory/devices/edge-01", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}

// BenchmarkRespondWithLinksParallel exists to prove the claim linkBuilder's
// own doc comment makes: it is built once and never mutated, so every
// in-flight request reads it concurrently with no synchronization. Run
// under -race, a lazy cache or a per-request map write would surface here
// and nowhere else.
func BenchmarkRespondWithLinksParallel(b *testing.B) {
	router := benchRouter(b, 2)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/edge-01", nil)
		for pb.Next() {
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
		}
	})
}
