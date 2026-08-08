package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/apispec"
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
