package api

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/go-chi/chi/v5"
)

// wellKnownHandler serves one embedded, pre-marshaled JSON document
// verbatim. Deliberately unauthenticated and outside APIVersionPrefix,
// the same operational-endpoint reasoning this file's neighbors
// (health.go) already document for /healthz and /readyz: a schema
// carries no data about this deployment, only about the product's own
// static shape, so there is nothing here to protect and no caller to
// turn away.
func wellKnownHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSONBytes(w, r, http.StatusOK, body, loggerFrom(r))
	}
}

// registerWellKnown mounts the three /.well-known/pleiades/*.json routes
// documented in docs/reference/schemas/, all reads served from
// internal/api/wellknown's embedded copies (tools/gendocs writes both
// from one source, so they cannot disagree).
func registerWellKnown(r chi.Router) {
	r.Get("/.well-known/pleiades/runbook.schema.json", wellKnownHandler(wellknown.RunbookSchema))
	r.Get("/.well-known/pleiades/inventory.schema.json", wellKnownHandler(wellknown.InventorySchema))
	r.Get("/.well-known/pleiades/module-catalog.json", wellKnownHandler(wellknown.ModuleCatalog))
}

// registerOpenAPI mounts APIVersionPrefix+"/openapi.json", deliberately
// outside the r.Route(APIVersionPrefix, ...) subtree below (and so
// outside cfg.Auth) even though it shares that subtree's URL prefix: an
// OpenAPI document carries no data about a specific deployment, the same
// "nothing here to protect" reasoning registerWellKnown's own doc
// comment states for the other three generated documents, so requiring a
// bearer token to read the API's own shape would protect nothing while
// blocking exactly the tooling (a client generator, an API browser) this
// endpoint exists for.
func registerOpenAPI(r chi.Router) {
	r.Get(APIVersionPrefix+"/openapi.json", wellKnownHandler(wellknown.OpenAPISpec))
}
