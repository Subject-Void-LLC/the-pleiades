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

// MockEvaluator is a two-method stand-in for auth.Evaluator's token
// validation, the only part AuthMiddleware depends on.
type MockEvaluator struct {
	ValidToken string
}

func (m *MockEvaluator) ValidateToken(ctx context.Context, tokenStr string) (*auth.Identity, error) {
	if tokenStr == m.ValidToken {
		return &auth.Identity{Subject: "test-user"}, nil
	}
	return nil, errors.New("invalid token")
}

// contextWithIdentity attaches id to req's context the way AuthMiddleware
// would, for a test that needs an already-authenticated request without
// standing up a token issuer.
func contextWithIdentity(req *http.Request, id *auth.Identity) context.Context {
	return context.WithValue(req.Context(), api.IdentityKeyForTest, id)
}

func TestAuthMiddleware_ReleaseGate(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{name: "valid token", authHeader: "Bearer super-secret-token", wantStatus: http.StatusOK},
		{name: "missing header", authHeader: "", wantStatus: http.StatusUnauthorized},
		{name: "invalid token", authHeader: "Bearer bad-token", wantStatus: http.StatusUnauthorized},
		{name: "no scheme", authHeader: "super-secret-token", wantStatus: http.StatusUnauthorized},
		{name: "scheme with no token", authHeader: "Bearer ", wantStatus: http.StatusUnauthorized},
		{name: "scheme without separator", authHeader: "Bearersuper-secret-token", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme case", authHeader: "bearer super-secret-token", wantStatus: http.StatusUnauthorized},
		{name: "different scheme", authHeader: "Basic dXNlcjpwYXNz", wantStatus: http.StatusUnauthorized},
	}

	evaluator := &MockEvaluator{ValidToken: "super-secret-token"}
	handler := api.AuthMiddleware(evaluator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := api.IdentityFromContext(r.Context()); !ok {
			t.Error("handler reached without an identity in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/secured", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}

// TestTraceIDFromContext_ReportsAbsence proves the helper distinguishes
// "no span is recording" from a trace ID that happens to be all zeros. A
// caller that could not tell them apart would stamp a meaningless
// all-zero trace ID onto published events.
func TestTraceIDFromContext_ReportsAbsence(t *testing.T) {
	if id, ok := api.TraceIDFromContext(context.Background()); ok {
		t.Errorf("a bare context reported trace ID %q, want no trace ID", id)
	}
}

// TestIdentityFromContext_RejectsTypedNil proves a nil *auth.Identity
// stored under the identity key reports absent rather than handing a
// caller a nil pointer that reads as present.
func TestIdentityFromContext_RejectsTypedNil(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := contextWithIdentity(req, nil)
	if _, ok := api.IdentityFromContext(ctx); ok {
		t.Error("a nil identity reported as present")
	}
}
