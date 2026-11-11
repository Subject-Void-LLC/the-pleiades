// Package telemetry_test: the telemetry boundary over the production
// export path, OTLP over HTTP to a real receiver.
package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// otlpReceiver is an OTLP/HTTP trace receiver: it decodes each export
// request the way a collector does and keeps the raw bodies, so a test can
// assert both on the decoded spans and on every byte that crossed the wire.
type otlpReceiver struct {
	// mu guards the fields below against the exporter's goroutine.
	mu sync.Mutex
	// bodies are the raw request bodies.
	bodies [][]byte
	// requests are the decoded export requests.
	requests []*collectortrace.ExportTraceServiceRequest
}

// ServeHTTP accepts one export request.
func (o *otlpReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.URL.Path != "/v1/traces" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var req collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad protobuf", http.StatusBadRequest)
		return
	}
	o.mu.Lock()
	o.bodies = append(o.bodies, body)
	o.requests = append(o.requests, &req)
	o.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
	out, _ := proto.Marshal(&collectortrace.ExportTraceServiceResponse{})
	_, _ = w.Write(out)
}

// TestSetup_OTLPExportCarriesOnlyThePolicy is the boundary on the
// production path: a span carrying an unlisted key, recorded error text
// and a free status description is exported over OTLP/HTTP to a receiver,
// and the bytes the receiver got hold the allowlisted identifier and none
// of the values.
func TestSetup_OTLPExportCarriesOnlyThePolicy(t *testing.T) {
	const (
		paramCanary  = "canaryotlpparam0016"
		errorCanary  = "canaryotlperror0017"
		statusCanary = "canaryotlpstatus0018"
		jobID        = "11111111-1111-4111-8111-111111111111"
	)
	receiver := &otlpReceiver{}
	srv := httptest.NewServer(receiver)
	t.Cleanup(srv.Close)

	provider, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName: "telemetry-boundary-test",
		Exporter:    telemetry.ExporterOTLP,
		Endpoint:    strings.TrimPrefix(srv.URL, "http://"),
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	_, span := provider.Tracer(telemetry.RunnerScope).Start(context.Background(), "runner.handle_message")
	span.SetAttributes(
		attribute.String("pleiades.job.id", jobID),
		attribute.String("pleiades.params.enable_password", paramCanary),
	)
	span.RecordError(errors.New("device said " + errorCanary))
	span.SetStatus(codes.Error, "login failed "+statusCanary)
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if len(receiver.requests) == 0 {
		t.Fatal("the receiver got no export request, so nothing below proves anything")
	}
	wire := bytes.Join(receiver.bodies, nil)
	for _, canary := range []string{paramCanary, errorCanary, statusCanary} {
		if bytes.Contains(wire, []byte(canary)) {
			t.Errorf("%q crossed the wire to the collector", canary)
		}
	}
	if !bytes.Contains(wire, []byte(jobID)) {
		t.Error("the allowlisted job id did not reach the collector")
	}

	var spans int
	for _, req := range receiver.requests {
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, s := range ss.GetSpans() {
					spans++
					if s.GetName() != "runner.handle_message" {
						t.Errorf("span name = %q", s.GetName())
					}
					if s.GetStatus().GetMessage() != "failure description withheld by telemetry policy" {
						t.Errorf("status message = %q, want the withheld phrase", s.GetStatus().GetMessage())
					}
					for _, kv := range s.GetAttributes() {
						if kv.GetKey() == "pleiades.params.enable_password" {
							t.Error("an unlisted key reached the collector")
						}
					}
				}
			}
		}
	}
	if spans != 1 {
		t.Errorf("the collector decoded %d spans, want 1", spans)
	}
}
