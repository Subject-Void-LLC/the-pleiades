package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
)

func BenchmarkAuthMiddleware(b *testing.B) {
	evaluator := &MockEvaluator{ValidToken: "super-secret-token"}
	middleware := api.AuthMiddleware(evaluator)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer super-secret-token")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}
