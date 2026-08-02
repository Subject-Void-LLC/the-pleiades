package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
)

func FuzzHATEOASMiddleware(f *testing.F) {
	generator := &MockHATEOASGenerator{Allowed: []string{"GET"}}
	middleware := api.HATEOASMiddleware(generator)

	f.Add([]byte(`{"data": "valid json"}`))
	f.Add([]byte(`malformed json`))
	f.Add([]byte(`{"_links": "existing conflict"}`))

	f.Fuzz(func(t *testing.T, payload []byte) {
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(payload)
		}))

		req := httptest.NewRequest("GET", "/api", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	})
}
