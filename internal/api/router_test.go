package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/prometheus/client_golang/prometheus"
)

func TestAPIGateway_ReleaseGate(t *testing.T) {
	router := api.NewRouter()

	// 1. Verify /healthz returns ok and injects a Trace-ID
	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}

	traceID := rr.Header().Get("X-Trace-ID")
	if traceID == "" {
		t.Errorf("expected X-Trace-ID header to be set by middleware")
	}

	// 2. Verify Prometheus Metrics tracked the request
	// We check the specific metric counter
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() == "http_requests_total" {
			for _, m := range mf.GetMetric() {
				// We expect label values to match our request
				var labelsMatch bool
				for _, l := range m.GetLabel() {
					if l.GetName() == "path" && l.GetValue() == "/healthz" {
						labelsMatch = true
					}
				}
				if labelsMatch && m.GetCounter().GetValue() >= 1 {
					found = true
				}
			}
		}
	}

	if !found {
		t.Errorf("expected http_requests_total metric for /healthz to be >= 1")
	}

	// Also test the /metrics endpoint directly
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRr := httptest.NewRecorder()
	router.ServeHTTP(metricsRr, metricsReq)

	if !strings.Contains(metricsRr.Body.String(), `http_requests_total`) {
		t.Errorf("expected /metrics endpoint to expose http_requests_total")
	}
}
