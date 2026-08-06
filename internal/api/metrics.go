package api

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the RED instruments (Rate, Errors, Duration) this
// package's MetricsMiddleware records, plus an in-flight gauge.
//
// PATTERNS.md's RED Method entry asks for all three signals. The previous
// implementation had only Rate, as a single counter labeled by raw URL
// path, registered into Prometheus's default registry from a package-level
// promauto call at init time. That made Errors and Duration unanswerable,
// gave the counter an unbounded label set, and meant two routers in one
// process (or two tests in one package) fought over one global registry.
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

// NewMetrics registers the RED instruments on reg and returns them. The
// registry is a parameter, not prometheus.DefaultRegisterer, so a process
// owns its own metrics and a test can assert against an isolated set.
//
// It panics if reg already holds a collector with these names, which is
// prometheus.Registerer.MustRegister's own behavior: a duplicate
// registration is a programming error discovered at startup, not a runtime
// condition worth returning.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total HTTP requests, labeled by matched route, method, and response status code.",
			},
			// "route" is the chi route pattern, never the raw path: see
			// routePattern's own comment for the cardinality reasoning.
			[]string{"route", "method", "code"},
		),
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "http_request_duration_seconds",
				Help: "HTTP request latency in seconds, labeled by matched route, method, and response status code.",
				// The default client_golang buckets top out at 10s, which
				// is fine for ordinary API calls but useless for the SSE
				// log stream, a request that stays open for as long as an
				// operator watches a job. The extra decade keeps those
				// visible instead of collapsing them all into +Inf.
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
			},
			[]string{"route", "method", "code"},
		),
		inFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "http_requests_in_flight",
				Help: "HTTP requests currently being served.",
			},
		),
	}
	reg.MustRegister(m.requests, m.duration, m.inFlight)
	return m
}

// observe records one completed request against all three RED signals.
func (m *Metrics) observe(route, method string, status int, latency time.Duration) {
	code := strconv.Itoa(status)
	m.requests.WithLabelValues(route, method, code).Inc()
	m.duration.WithLabelValues(route, method, code).Observe(latency.Seconds())
}
