package api

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type contextKey string

const traceIDKey contextKey = "trace_id"

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"path", "method"},
	)

	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
)

// TraceIDMiddleware injects a unique trace ID into every request context.
func TraceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		
		ctx := context.WithValue(r.Context(), traceIDKey, traceID)
		w.Header().Set("X-Trace-ID", traceID)
		
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// StructuredLoggerMiddleware logs the start and end of each request, embedding the trace ID.
func StructuredLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, _ := r.Context().Value(traceIDKey).(string)
		
		logger.Info("request started",
			slog.String("trace_id", traceID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)

		start := time.Now()
		
		// Wrap ResponseWriter to capture status code in a real system (simplified here)
		next.ServeHTTP(w, r)

		logger.Info("request completed",
			slog.String("trace_id", traceID),
			slog.Duration("latency", time.Since(start)),
		)
	})
}

// MetricsMiddleware tracks Prometheus metrics for incoming requests.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpRequestsTotal.WithLabelValues(r.URL.Path, r.Method).Inc()
		next.ServeHTTP(w, r)
	})
}
