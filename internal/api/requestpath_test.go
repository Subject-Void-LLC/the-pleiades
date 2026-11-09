// Package api_test: the request log and trace record a path as the client
// escaped it.
package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestRequestLog_RecordsThePathAsSent holds the request log line and the
// server span to the path the client escaped. Logged decoded, a request
// for the device "sw/facts" read as one for the facts of the device "sw",
// a different route on a different record.
func TestRequestLog_RecordsThePathAsSent(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Tracer = tp.Tracer("requestpath")
		cfg.Routes = []api.Route{
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: ok},
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}/facts", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: ok},
		}
	})

	const sent = api.APIVersionPrefix + "/inventory/devices/sw%2Ffacts"
	if rr := get(t, router, http.MethodGet, sent); rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", sent, rr.Code)
	}

	line := decodeLogLine(t, logBuf.Bytes())
	if line["path"] != sent {
		t.Errorf("log line path is %v, want %q as sent", line["path"], sent)
	}
	if line["route"] != api.APIVersionPrefix+"/inventory/devices/{name}" {
		t.Errorf("log line route is %v, want the device route", line["route"])
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	var traced string
	for _, attr := range spans[0].Attributes() {
		if attr.Key == "url.path" {
			traced = attr.Value.AsString()
		}
	}
	if traced != sent {
		t.Errorf("span url.path is %q, want %q as sent", traced, sent)
	}
}
