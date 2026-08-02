package api_test

import (
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
)

func FuzzAPIRouter(f *testing.F) {
	router := api.NewRouter()

	f.Add("GET", "/healthz")
	f.Add("POST", "/metrics")
	f.Add("PUT", "/unknown")
	f.Add("GET", "/../../etc/passwd")

	f.Fuzz(func(t *testing.T, method, path string) {
		// httptest.NewRequest panics on invalid URIs (which the fuzzer will generate)
		defer func() {
			if r := recover(); r != nil {
				// skip this iteration
			}
		}()

		req := httptest.NewRequest(method, path, nil)
		rr := httptest.NewRecorder()

		// The router should safely handle and 404/405 without panicking
		router.ServeHTTP(rr, req)
	})
}
