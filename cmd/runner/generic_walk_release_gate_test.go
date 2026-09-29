// Release gate for Phase 111's Walk tier: a dispatched generic device is
// rebuilt on the Runner as its real type, so a method that reads its
// accessors works there, over real NATS, a real Runner and its real
// per-task child process.
package main_test

import (
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// untilCompleted reads the job's events until its task.completed, and
// returns every event before it and the completion.
func (w *jobWatch) untilCompleted(t *testing.T, within time.Duration) ([]wire.JobEvent, wire.JobEvent) {
	t.Helper()
	var events []wire.JobEvent
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		msgs, err := w.consumer.Fetch(1, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			t.Fatalf("fetch log events: %v", err)
		}
		for msg := range msgs.Messages() {
			var wrapped event.Event
			if err := json.Unmarshal(msg.Data(), &wrapped); err != nil {
				t.Fatal(err)
			}
			var evt wire.JobEvent
			if err := json.Unmarshal(wrapped.Data, &evt); err != nil {
				t.Fatal(err)
			}
			if evt.Task == "task.completed" {
				return events, evt
			}
			events = append(events, evt)
		}
	}
	t.Fatal("timed out waiting for task.completed")
	return nil, wire.JobEvent{}
}

// apiDispatch is a dispatch of an onboarded generic_http device whose API
// is srv, reached with its pinned certificate and a bearer credential.
func apiDispatch(srv *httptest.Server, extra map[string]any) wire.DispatchPayload {
	props := map[string]any{
		generic.BaseURLProperty:  srv.URL + "/api",
		generic.HTTPAuthProperty: "bearer",
		devicetls.CAPEMProperty:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})),
	}
	for k, v := range extra {
		props[k] = v
	}
	// Bound to the dispatched properties, extra included, as the
	// Controller's stored, onboarded discovery is.
	found := inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}}
	found.Binding = generic.Binding(generic.TypeHTTP, inventory.NewProperties(props))
	if _, overridden := extra[inventory.DiscoveredProperty]; !overridden {
		props[inventory.DiscoveredProperty] = found.Property()
	}
	jobID := uuid.New().String()
	return wire.DispatchPayload{
		JobID:     jobID,
		RunbookID: "api",
		// A device of its own per dispatch. The address-only dispatch
		// below fails on purpose, and the Runner retries a failed dispatch
		// under the device's lock; a later dispatch of the same device
		// then races those retries for the lock and can lose all five of
		// its deliveries, which strands the job (FAILURE_PATTERNS 396).
		// That is a real defect, and not the one this gate is about.
		DeviceID:         "api-gate-" + jobID[:8],
		DeviceName:       "api1",
		DeviceHost:       "127.0.0.1",
		Capabilities:     []capability.Name{capability.NameNetworkAddressable, capability.NameHTTPAPI},
		Secrets:          map[string]string{wire.SecretPassword: "walk-gate-token"},
		DeviceType:       generic.TypeHTTP,
		DeviceProperties: props,
	}
}

// run publishes payload and returns the job's events and completion.
func (h *releaseGateHarness) run(t *testing.T, payload wire.DispatchPayload) ([]wire.JobEvent, wire.JobEvent) {
	t.Helper()
	watch := h.watchJob(t, payload.JobID)
	h.publish(t, topology.DispatchSubject(payload.DeviceID), payload)
	return watch.untilCompleted(t, 60*time.Second)
}

// TestGenericWalkReleaseGate_DeviceAccessorsReachTheRunner: http.request's
// device mode runs on a Runner against a dispatched generic_http device,
// reaching the device's own API with its own credential and pinned
// certificate. The same dispatch without the device's type and properties,
// as an older Controller sends it, is refused by name; and a device
// allowed deprecated TLS says so in the job log.
func TestGenericWalkReleaseGate_DeviceAccessorsReachTheRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Walk-tier generic device gate, which runs real NATS and a real Runner, in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInHomeDir, map[string]string{
		"api.yaml": "id: api\ntasks:\n  - name: read the interfaces\n    fqcn: http.request\n    params:\n      url: /interfaces\n",
	})
	var authorized atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/interfaces" && r.Header.Get("Authorization") == "Bearer walk-gate-token" {
			authorized.Add(1)
		}
	})
	srv := httptest.NewTLSServer(handler)
	defer srv.Close()

	_, done := h.run(t, apiDispatch(srv, nil))
	if done.Status == "failed" || authorized.Load() != 1 {
		t.Fatalf("the device-mode call did not reach the API: %s (%q), %d authorized requests", done.Status, done.EventData.Message, authorized.Load())
	}

	older := apiDispatch(srv, nil)
	older.DeviceType, older.DeviceProperties = "", nil
	if _, done := h.run(t, older); done.Status != "failed" || !strings.Contains(done.EventData.Message, "not available where this task runs") {
		t.Errorf("an address-only dispatch: %s %q", done.Status, done.EventData.Message)
	}

	legacy := httptest.NewUnstartedServer(handler)
	legacy.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS10}
	legacy.StartTLS()
	defer legacy.Close()
	events, done := h.run(t, apiDispatch(legacy, map[string]any{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true}))
	if done.Status == "failed" {
		t.Fatalf("the deprecated-TLS device failed: %q", done.EventData.Message)
	}
	warned := false
	for _, e := range events {
		warned = warned || (e.Task == "task.warning" && strings.Contains(e.EventData.Message, "RFC 8996"))
	}
	if !warned {
		t.Errorf("the job log carried no warning: %+v", events)
	}
	if _, done := h.run(t, apiDispatch(legacy, nil)); done.Status != "failed" {
		t.Error("a device not allowed deprecated TLS reached a TLS 1.0 API")
	}
}
