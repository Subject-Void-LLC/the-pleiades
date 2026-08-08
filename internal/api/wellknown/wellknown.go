// Package wellknown embeds the JSON documents this binary serves
// statically: the runbook and inventory JSON Schemas and the full module
// catalog at /.well-known/pleiades/*.json (internal/api/wellknown.go),
// and the OpenAPI document at /api/v1/openapi.json, deliberately outside
// that authenticated subtree for the same reason the other three are
// unauthenticated: none of the four carry any data about a specific
// deployment, only the product's own static shape. All four are
// generated and committed by tools/gendocs, which writes the identical
// bytes here and to docs/reference/schemas/ in one pass, so a browsed
// copy and a served copy can never diverge. Never hand-edited: if a
// schema reads wrong, the fix belongs in tools/gendocs or the data it
// reads, not in the JSON committed here.
package wellknown

import _ "embed"

//go:embed runbook.schema.json
var RunbookSchema []byte

//go:embed inventory.schema.json
var InventorySchema []byte

//go:embed module-catalog.json
var ModuleCatalog []byte

//go:embed openapi.json
var OpenAPISpec []byte
