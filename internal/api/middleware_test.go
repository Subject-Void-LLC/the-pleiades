package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

type MockEvaluator struct {
	ValidToken string
}

func (m *MockEvaluator) ValidateToken(ctx context.Context, tokenStr string) (*auth.Identity, error) {
	if tokenStr == m.ValidToken {
		return &auth.Identity{Subject: "test-user"}, nil
	}
	return nil, errors.New("invalid token")
}

func TestAuthMiddleware_ReleaseGate(t *testing.T) {
	evaluator := &MockEvaluator{ValidToken: "super-secret-token"}
	middleware := api.AuthMiddleware(evaluator)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler logic here
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	t.Run("Valid Token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/secured", nil)
		req.Header.Set("Authorization", "Bearer super-secret-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %v", rr.Code)
		}
	})

	t.Run("Missing Token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/secured", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("Invalid Token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/secured", nil)
		req.Header.Set("Authorization", "Bearer bad-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("Malformed Header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/secured", nil)
		req.Header.Set("Authorization", "super-secret-token")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %v", rr.Code)
		}
	})
}
