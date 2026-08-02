package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRouter initializes the chi mux with standard telemetry middleware.
func NewRouter() *chi.Mux {
	r := chi.NewRouter()

	// Install Telemetry & Logging Middleware
	r.Use(TraceIDMiddleware)
	r.Use(StructuredLoggerMiddleware)
	r.Use(MetricsMiddleware)

	// Base System Endpoints
	r.Get("/healthz", healthzHandler)
	r.Handle("/metrics", promhttp.Handler())

	return r
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}
