package main

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
)

// generateOpenAPI emits outDir/openapi.json (OpenAPI 3.1) and the
// identical bytes into internal/api/wellknown (writeSchema's own
// two-copy convention), built entirely from apispec.Endpoints. Every
// operation's method, pattern, scope, and link relation come off the same
// Endpoint value cmd/controller/main.go turns into an api.Route, so this
// document cannot describe a route the server serves differently. It can,
// however, describe a route the server never registered at all: nothing
// compares this slice against cmd/controller's own Routes table (see
// internal/apispec's package doc for why, and for what closing it takes).
// Because Scope and Rel are already mandatory per Endpoint
// (apispec.Endpoint mirrors api.Route's own validateRoutes requirement),
// this document carries two artifacts nobody hand-maintains elsewhere:
// which scope each operation requires (the closest thing this codebase
// has to a roles-by-scopes authorization matrix, since scopes, not
// roles, are what a route actually checks -- see
// docs/09-control-plane-and-api.md), and the link relation each response
// advertises through its own hypermedia affordances.
func generateOpenAPI(outDir string) error {
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Pleiades control plane API",
			"description": "Every route declared in internal/apispec, the package cmd/controller/main.go also builds its real router from, served under " + api.APIVersionPrefix + ". A listed route's method, pattern, scope, and link relation are the ones the server enforces. That a listed route is registered at all is not checked by anything, so confirm a route against a running controller before building on it.",
			"version":     "unreleased",
		},
		"servers": []any{
			map[string]any{"url": api.APIVersionPrefix, "description": "Relative to the controller's own origin."},
		},
		"security": []any{
			map[string]any{"bearerAuth": []any{}},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
					"description":  "See docs/10-running-in-production.md for token issuance. A route with no matching scope in the token, and no admin role, is refused with 403.",
				},
			},
		},
		"paths": openAPIPaths(),
	}

	return writeSchema(outDir, "openapi.json", doc)
}

func openAPIPaths() map[string]any {
	paths := map[string]any{}
	for _, ep := range apispec.Endpoints {
		methodObj := map[string]any{
			"operationId": ep.Name,
			"summary":     ep.Summary,
			"description": ep.Description,
			"parameters":  openAPIParameters(ep.Params),
			"responses":   openAPIResponses(ep),
			"security": []any{
				map[string]any{"bearerAuth": []any{string(ep.Scope)}},
			},
			"x-link-relation": string(ep.Rel),
		}
		if len(ep.Params) == 0 {
			delete(methodObj, "parameters")
		}
		if body := openAPIRequestBody(ep); body != nil {
			methodObj["requestBody"] = body
		}

		pathItem, ok := paths[ep.Pattern].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
			paths[ep.Pattern] = pathItem
		}
		pathItem[openAPIMethodKey(ep.Method)] = methodObj
	}
	return paths
}

// openAPIRequestBody renders an endpoint's declared request schema, or nil
// when it accepts no body.
//
// # Why this exists
//
// apispec.Endpoint has carried RequestContentType and RequestSchema since
// the route table was written, more than twenty endpoints declare them, and
// until Phase 22 this generator emitted none of them. Every one of those
// schemas was correctly declared and never read by anything downstream,
// which is FAILURE_PATTERNS.md #116's exact shape, here in the documentation
// generator rather than in an execution path.
//
// The consequence was concrete rather than cosmetic: the published OpenAPI
// document described how to CALL every endpoint and not what to send to any
// of them, so a client generated from it could read a credential and could
// not create one.
//
// # required
//
// It is read off the schema itself rather than declared separately, because
// a second place to say which fields are required is a second place for the
// answer to be wrong. A schema with no "required" key produces a body with
// no required list, which is correct for the several endpoints whose fields
// are all optional (a launch that supplies nothing is a POST with no body).
func openAPIRequestBody(ep apispec.Endpoint) map[string]any {
	if ep.RequestContentType == "" || len(ep.RequestSchema) == 0 {
		return nil
	}

	// Required when the schema names a required field. An endpoint whose
	// fields are all optional accepts a request with no body at all, and
	// marking that required would make a legal call look illegal.
	required, _ := ep.RequestSchema["required"].([]any)

	return map[string]any{
		"required": len(required) > 0,
		"content": map[string]any{
			ep.RequestContentType: map[string]any{
				"schema": ep.RequestSchema,
			},
		},
	}
}

func openAPIMethodKey(method string) string {
	switch method {
	case "GET":
		return "get"
	case "POST":
		return "post"
	case "PUT":
		return "put"
	case "PATCH":
		return "patch"
	case "DELETE":
		return "delete"
	default:
		return method
	}
}

func openAPIParameters(params []apispec.Param) []any {
	out := make([]any, len(params))
	for i, p := range params {
		out[i] = map[string]any{
			"name":        p.Name,
			"in":          p.In,
			"required":    p.Required,
			"description": p.Description,
			"schema":      map[string]any{"type": p.Type},
		}
	}
	return out
}

func openAPIResponses(ep apispec.Endpoint) map[string]any {
	out := map[string]any{}
	contentType := ep.ResponseContentType
	if contentType == "" {
		contentType = "application/json"
	}

	for _, r := range ep.Responses {
		resp := map[string]any{"description": r.Description}
		if r.Schema != nil {
			resp["content"] = map[string]any{
				contentType: map[string]any{"schema": r.Schema},
			}
		}
		out[fmt.Sprintf("%d", r.Status)] = resp
	}
	return out
}
