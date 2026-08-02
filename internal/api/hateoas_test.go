package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
)

type MockHATEOASGenerator struct {
	Allowed []string
}

func (m *MockHATEOASGenerator) GetAllowedMethods(ctx context.Context, endpoint string) ([]string, error) {
	return m.Allowed, nil
}

func TestHATEOASMiddleware_ReleaseGate(t *testing.T) {
	generator := &MockHATEOASGenerator{Allowed: []string{"GET", "POST", "DELETE"}}
	middleware := api.HATEOASMiddleware(generator)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": "resource-xyz"}`))
	}))

	req := httptest.NewRequest("GET", "/api/v1/devices/123", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %v", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	links, ok := resp["_links"].([]interface{})
	if !ok {
		t.Fatalf("expected _links array in response")
	}

	foundSelf := false
	for _, l := range links {
		linkMap := l.(map[string]interface{})
		if linkMap["rel"] == "self" && linkMap["href"] == "/api/v1/devices/123" {
			foundSelf = true
		}
	}

	if !foundSelf {
		t.Errorf("expected _links to contain rel: self with href /api/v1/devices/123")
	}

	if len(links) != 3 {
		t.Errorf("expected 3 links (self, POST, DELETE), got %d", len(links))
	}
}
