package apispec_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
)

func noopHandler(http.ResponseWriter, *http.Request) {}

// allHandlers builds a complete handler set, so each test can remove or
// add exactly the one thing it is about.
func allHandlers() map[string]http.HandlerFunc {
	h := make(map[string]http.HandlerFunc, len(apispec.Endpoints))
	for _, e := range apispec.Endpoints {
		h[e.Name] = noopHandler
	}
	return h
}

func TestRoutes_PairsEveryEndpointWithItsHandler(t *testing.T) {
	routes, err := apispec.Routes(allHandlers())
	if err != nil {
		t.Fatalf("Routes() = %v, want nil", err)
	}
	if len(routes) != len(apispec.Endpoints) {
		t.Fatalf("Routes() returned %d routes, want %d", len(routes), len(apispec.Endpoints))
	}

	// Order matches Endpoints, so routes mount in the order the generated
	// document lists them.
	for i, e := range apispec.Endpoints {
		got := routes[i]
		if got.Method != e.Method || got.Pattern != e.Pattern {
			t.Errorf("routes[%d] = %s %s, want %s %s", i, got.Method, got.Pattern, e.Method, e.Pattern)
		}
		if got.Scope != e.Scope {
			t.Errorf("routes[%d].Scope = %q, want %q", i, got.Scope, e.Scope)
		}
		if got.Rel != e.Rel {
			t.Errorf("routes[%d].Rel = %q, want %q", i, got.Rel, e.Rel)
		}
		if got.Handler == nil {
			t.Errorf("routes[%d] carries no handler", i)
		}
	}
}

// The failure this function exists to prevent: an Endpoint declared and
// documented, but never mounted, which the generated OpenAPI document
// advertises and the server answers with 404.
func TestRoutes_RejectsAnEndpointWithNoHandler(t *testing.T) {
	handlers := allHandlers()
	delete(handlers, apispec.GetDevice.Name)

	_, err := apispec.Routes(handlers)
	if err == nil {
		t.Fatal("Routes() = nil, want an error naming the unmounted endpoint")
	}
	if !strings.Contains(err.Error(), apispec.GetDevice.Name) {
		t.Errorf("Routes() = %q, want it to name %q", err, apispec.GetDevice.Name)
	}
	if !strings.Contains(err.Error(), "no handler") {
		t.Errorf("Routes() = %q, want it to say which direction failed", err)
	}
}

// A nil handler under a real name is the same defect as a missing key,
// and is easier to introduce: a method value on a nil receiver field
// reads as present until it is called.
func TestRoutes_RejectsANilHandler(t *testing.T) {
	handlers := allHandlers()
	handlers[apispec.GetJob.Name] = nil

	_, err := apispec.Routes(handlers)
	if err == nil || !strings.Contains(err.Error(), apispec.GetJob.Name) {
		t.Fatalf("Routes() = %v, want an error naming %q", err, apispec.GetJob.Name)
	}
}

// The reverse direction, and the more dangerous one: a route that exists
// and enforces a scope but is invisible to every generated description of
// this API.
func TestRoutes_RejectsAHandlerWithNoEndpoint(t *testing.T) {
	handlers := allHandlers()
	handlers["retire_everything"] = noopHandler

	_, err := apispec.Routes(handlers)
	if err == nil {
		t.Fatal("Routes() = nil, want an error naming the undeclared handler")
	}
	if !strings.Contains(err.Error(), "retire_everything") {
		t.Errorf("Routes() = %q, want it to name retire_everything", err)
	}
	if !strings.Contains(err.Error(), "no endpoint") {
		t.Errorf("Routes() = %q, want it to say which direction failed", err)
	}
}

// Both directions wrong at once must report both, so a caller fixing one
// does not have to re-run to discover the other.
func TestRoutes_ReportsBothDirectionsTogether(t *testing.T) {
	handlers := allHandlers()
	delete(handlers, apispec.DeleteDevice.Name)
	handlers["ghost_route"] = noopHandler

	_, err := apispec.Routes(handlers)
	if err == nil {
		t.Fatal("Routes() = nil, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, apispec.DeleteDevice.Name) || !strings.Contains(msg, "ghost_route") {
		t.Errorf("Routes() = %q, want it to name both problems", msg)
	}
}

func TestRoutes_RejectsAnEmptyHandlerSet(t *testing.T) {
	_, err := apispec.Routes(nil)
	if err == nil {
		t.Fatal("Routes(nil) = nil, want an error")
	}
	for _, e := range apispec.Endpoints {
		if !strings.Contains(err.Error(), e.Name) {
			t.Errorf("Routes(nil) = %q, want it to name %q", err, e.Name)
		}
	}
}
