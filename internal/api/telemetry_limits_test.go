// Package api_test: an unauthenticated request cannot size the span the
// production pipeline exports for it.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
)

// TestRouter_ALongPathCannotSizeTheExportedSpan sends a long unmatched path
// through a router whose tracer comes from telemetry.Setup with the stdout
// exporter, the pipeline cmd/controller builds. url.path is recorded before
// authentication and rate limiting, so the span limit Setup installs is
// what keeps a caller from deciding how large every exported span is.
//
// The stdout exporter writes to os.Stdout and its writer is unexported
// outside the telemetry package, so the test points os.Stdout at a pipe
// for the life of the provider.
func TestRouter_ALongPathCannotSizeTheExportedSpan(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	realStdout := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = realStdout })

	provider, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName: "api-limits-test",
		Exporter:    telemetry.ExporterStdout,
	})
	os.Stdout = realStdout
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	captured := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(reader)
		captured <- b
	}()

	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Tracer = provider.Tracer(telemetry.APIScope)
	})
	long := "/" + strings.Repeat("p", 20000)
	if rr := get(t, router, http.MethodGet, long); rr.Code != http.StatusNotFound {
		t.Fatalf("GET of a long unmatched path = %d, want 404", rr.Code)
	}

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	_ = writer.Close()
	out := <-captured

	var span struct {
		Name       string
		Attributes []struct {
			Key   string
			Value struct{ Value any }
		}
	}
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&span); err != nil {
		t.Fatalf("decoding the exported span: %v (output %d bytes)", err, len(out))
	}
	if span.Name != "GET unmatched" {
		t.Errorf("span name = %q, want %q", span.Name, "GET unmatched")
	}
	var path string
	for _, kv := range span.Attributes {
		if kv.Key == "url.path" {
			path, _ = kv.Value.Value.(string)
		}
	}
	if path == "" || len(path) > 1024 {
		t.Errorf("exported url.path is %d bytes, want it present and at most 1024", len(path))
	}
	if len(out) > 8192 {
		t.Errorf("the exported span is %d bytes for one request", len(out))
	}
}
