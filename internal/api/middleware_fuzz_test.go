package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
)

func FuzzAuthMiddleware(f *testing.F) {
	evaluator := &MockEvaluator{ValidToken: "super-secret-token"}
	middleware := api.AuthMiddleware(evaluator)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	f.Add("Bearer super-secret-token")
	f.Add("Bearer bad-token")
	f.Add("Basic dXNlcjpwYXNz")
	f.Add("")
	f.Add("Bearersuper-secret-token")
	f.Add("Bearer ")

	f.Fuzz(func(t *testing.T, authHeader string) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", authHeader)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		// The fuzz test ensures no panics occur with arbitrary headers
	})
}
