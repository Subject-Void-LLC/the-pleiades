package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
)

func BenchmarkHATEOASMiddleware(b *testing.B) {
	generator := &MockHATEOASGenerator{Allowed: []string{"GET", "POST", "DELETE"}}
	middleware := api.HATEOASMiddleware(generator)

	payload := []byte(`{"id":"123","status":"active","name":"router-01","properties":{"ip":"10.0.0.1"}}`)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))

	req := httptest.NewRequest("GET", "/api/v1/devices/123", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}
