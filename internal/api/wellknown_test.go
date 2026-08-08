package api_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api/wellknown"
)

// TestWellKnownRoutes proves every generated document is served at its
// real URL and is byte-identical to the embedded copy tools/gendocs
// wrote. /api/v1/openapi.json is the interesting case: it shares the
// versioned APIVersionPrefix a request under api.Use(cfg.Auth) is mounted
// beneath, yet registerOpenAPI mounts it directly on the root router,
// outside that subtree, in router.go before r.Route(APIVersionPrefix,
// ...) is ever reached. This test proves chi actually resolves the
// static "/api/v1/openapi.json" route to registerOpenAPI's handler
// rather than falling through into the APIVersionPrefix subrouter (which
// would 404 it, since no Route in cfg.Routes matches that pattern), a
// real risk with two registrations sharing one path prefix that a
// successful `go build` alone cannot catch.
func TestWellKnownRoutes(t *testing.T) {
	cases := []struct {
		path string
		want []byte
	}{
		{"/.well-known/pleiades/runbook.schema.json", wellknown.RunbookSchema},
		{"/.well-known/pleiades/inventory.schema.json", wellknown.InventorySchema},
		{"/.well-known/pleiades/module-catalog.json", wellknown.ModuleCatalog},
		{"/api/v1/openapi.json", wellknown.OpenAPISpec},
	}

	var buf bytes.Buffer
	router, _ := newTestRouter(t, &buf, nil)

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := get(t, router, http.MethodGet, tc.path)

			if rr.Code != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200; body: %s", tc.path, rr.Code, rr.Body.String())
			}
			if !bytes.Equal(rr.Body.Bytes(), tc.want) {
				t.Errorf("GET %s: body did not match the embedded wellknown copy", tc.path)
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("GET %s: Content-Type = %q, want \"application/json\"", tc.path, ct)
			}
		})
	}
}
