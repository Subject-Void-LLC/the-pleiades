package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
)

func TestOpenAPIPaths_OnePathItemPerPattern(t *testing.T) {
	paths := openAPIPaths()

	wantPatterns := map[string]bool{}
	for _, ep := range apispec.Endpoints {
		wantPatterns[ep.Pattern] = true
	}
	if len(paths) != len(wantPatterns) {
		t.Fatalf("openAPIPaths() has %d path item(s), want %d (one per distinct Pattern)", len(paths), len(wantPatterns))
	}
	for pattern := range wantPatterns {
		if _, ok := paths[pattern]; !ok {
			t.Errorf("openAPIPaths() missing path item for pattern %q", pattern)
		}
	}
}

func TestOpenAPIPaths_DeviceRouteHasBothMethods(t *testing.T) {
	paths := openAPIPaths()

	item, ok := paths["/inventory/devices/{name}"].(map[string]any)
	if !ok {
		t.Fatal("openAPIPaths() missing /inventory/devices/{name}")
	}
	for _, method := range []string{"get", "delete"} {
		if _, ok := item[method]; !ok {
			t.Errorf("/inventory/devices/{name} missing operation %q", method)
		}
	}
}

func TestOpenAPIPaths_EverySecurityScopeMatchesEndpoint(t *testing.T) {
	paths := openAPIPaths()

	for _, ep := range apispec.Endpoints {
		item, ok := paths[ep.Pattern].(map[string]any)
		if !ok {
			t.Fatalf("missing path item for %q", ep.Pattern)
		}
		op, ok := item[openAPIMethodKey(ep.Method)].(map[string]any)
		if !ok {
			t.Fatalf("missing operation %s %s", ep.Method, ep.Pattern)
		}

		security, ok := op["security"].([]any)
		if !ok || len(security) != 1 {
			t.Fatalf("%s %s: security = %#v, want exactly one requirement", ep.Method, ep.Pattern, op["security"])
		}
		req, ok := security[0].(map[string]any)
		if !ok {
			t.Fatalf("%s %s: security[0] is not an object", ep.Method, ep.Pattern)
		}
		scopes, ok := req["bearerAuth"].([]any)
		if !ok || len(scopes) != 1 || scopes[0] != string(ep.Scope) {
			t.Errorf("%s %s: bearerAuth scopes = %#v, want [%q]", ep.Method, ep.Pattern, req["bearerAuth"], ep.Scope)
		}

		if op["x-link-relation"] != string(ep.Rel) {
			t.Errorf("%s %s: x-link-relation = %v, want %q", ep.Method, ep.Pattern, op["x-link-relation"], ep.Rel)
		}
	}
}

func TestOpenAPIResponses_StreamJobLogsUsesEventStreamContentType(t *testing.T) {
	responses := openAPIResponses(apispec.StreamJobLogs)

	ok, exists := responses["200"].(map[string]any)
	if !exists {
		t.Fatal("StreamJobLogs missing a 200 response")
	}
	content, hasContent := ok["content"].(map[string]any)
	if !hasContent {
		// A 200 with no Schema (StreamJobLogs' own real shape) legitimately
		// carries no "content" key at all; nothing further to check.
		return
	}
	if _, hasJSON := content["application/json"]; hasJSON {
		t.Error("StreamJobLogs' 200 response should not be described as application/json")
	}
	if _, hasSSE := content["text/event-stream"]; !hasSSE {
		t.Error("StreamJobLogs' 200 response should be described as text/event-stream if it carries a schema")
	}
}

// TestGenerateOpenAPI_ProducesValidJSON runs in a temp $PWD for the same
// reason run_test.go's TestRun_Idempotent does: writeSchema's wellKnownDir
// is a fixed, repo-root-relative constant, so exercising the real
// generateOpenAPI entry point (rather than just openAPIPaths' own pure
// logic, covered above) means chdir'ing somewhere writeSchema's second
// copy can land harmlessly.
func TestGenerateOpenAPI_ProducesValidJSON(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd(): %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir(%s): %v", tmp, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origWD); err != nil {
			t.Fatalf("os.Chdir(%s) (restore): %v", origWD, err)
		}
	})

	if err := generateOpenAPI("out"); err != nil {
		t.Fatalf("generateOpenAPI() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join("out", "schemas", "openapi.json"))
	if err != nil {
		t.Fatalf("reading generated openapi.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("generated openapi.json is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi.json: openapi = %v, want \"3.1.0\"", doc["openapi"])
	}
	if _, ok := doc["paths"]; !ok {
		t.Error("openapi.json missing top-level \"paths\"")
	}
}

// TestOpenAPIPaths_EveryDeclaredRequestSchemaIsPublished is the regression
// guard for a defect Phase 22 found: apispec.Endpoint had carried
// RequestContentType and RequestSchema since the route table was written,
// more than twenty endpoints declared them, and this generator emitted none
// of them.
//
// Every one of those schemas was correctly declared and never read by
// anything downstream, which is FAILURE_PATTERNS.md #116's shape in the
// documentation generator. The consequence was concrete: the published
// OpenAPI document described how to call every endpoint and not what to
// send to any of them, so a client generated from it could read a
// credential and could not create one.
func TestOpenAPIPaths_EveryDeclaredRequestSchemaIsPublished(t *testing.T) {
	paths := openAPIPaths()

	published := 0
	for _, ep := range apispec.Endpoints {
		if ep.RequestContentType == "" || len(ep.RequestSchema) == 0 {
			continue
		}

		item, ok := paths[ep.Pattern].(map[string]any)
		if !ok {
			t.Fatalf("%s has no path item", ep.Pattern)
		}
		method, ok := item[openAPIMethodKey(ep.Method)].(map[string]any)
		if !ok {
			t.Fatalf("%s %s has no method object", ep.Method, ep.Pattern)
		}
		body, ok := method["requestBody"].(map[string]any)
		if !ok {
			t.Errorf("%s %s declares a request schema that the document does not publish", ep.Method, ep.Pattern)
			continue
		}

		content, ok := body["content"].(map[string]any)
		if !ok {
			t.Errorf("%s %s has a request body with no content", ep.Method, ep.Pattern)
			continue
		}
		if _, ok := content[ep.RequestContentType]; !ok {
			t.Errorf("%s %s publishes its body under a content type other than %q",
				ep.Method, ep.Pattern, ep.RequestContentType)
		}
		published++
	}

	if published == 0 {
		t.Fatal("no endpoint declares a request schema, so this test proves nothing")
	}
}

// TestOpenAPIRequestBody_RequiredFollowsTheSchema covers the one derived
// value in a request body, from both sides.
//
// A body is required when its schema names a required field and optional
// when it does not. Getting the second case wrong would be the visible one:
// launching a template that opens nothing is a POST with no body at all,
// and a document marking that body required would make a legal call look
// illegal to every generated client.
func TestOpenAPIRequestBody_RequiredFollowsTheSchema(t *testing.T) {
	tests := []struct {
		name string
		ep   apispec.Endpoint
		want any
	}{
		{
			name: "no body at all",
			ep:   apispec.Endpoint{},
			want: nil,
		},
		{
			name: "a schema with no content type is not published",
			ep:   apispec.Endpoint{RequestSchema: map[string]any{"type": "object"}},
			want: nil,
		},
		{
			name: "all fields optional",
			ep: apispec.Endpoint{
				RequestContentType: "application/json",
				RequestSchema:      map[string]any{"type": "object"},
			},
			want: false,
		},
		{
			name: "at least one field required",
			ep: apispec.Endpoint{
				RequestContentType: "application/json",
				RequestSchema:      map[string]any{"type": "object", "required": []any{"name"}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := openAPIRequestBody(tt.ep)
			if tt.want == nil {
				if body != nil {
					t.Fatalf("openAPIRequestBody() = %v, want nothing published", body)
				}
				return
			}
			if body == nil {
				t.Fatal("openAPIRequestBody() published nothing for an endpoint declaring a body")
			}
			if body["required"] != tt.want {
				t.Errorf("required = %v, want %v", body["required"], tt.want)
			}
		})
	}
}
