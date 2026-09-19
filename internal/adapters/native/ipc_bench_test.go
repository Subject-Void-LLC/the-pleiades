package native

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// BenchmarkIPCCollectionExecutor_SpawnLatency measures the per-task cost
// of the subprocess isolation boundary: one fork/exec of this binary, the
// child's own package init (which includes registering the whole
// Collection catalog), one JSON request in, one JSON response out.
//
// This exists because Phase 16 chose per-task isolation over per-job
// deliberately, and that choice is only defensible against a real number.
// A runbook with many Collection tasks against one device pays this cost
// once per task, so this benchmark is the input to any future argument for
// a warm-pool or per-job boundary instead. Recording it now means that
// argument starts from a measurement rather than an assumption.
//
// Note what it does NOT measure: a real Collection method's own work (the
// fixture method returns immediately) or any network I/O. This is purely
// the boundary's overhead, which is exactly the quantity in question.
func BenchmarkIPCCollectionExecutor_SpawnLatency(b *testing.B) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		b.Fatalf("newIPCCollectionExecutor: %v", err)
	}

	device := newWireDevice(wire.DispatchPayload{
		JobID:      "bench-job",
		DeviceID:   "bench-dev",
		DeviceName: "router1",
		DeviceHost: "10.0.0.1",
		SSHPort:    22,
		Secrets:    map[string]string{"username": "admin"},
	})
	desc := collection.Descriptor{Name: nativeIPCEchoMethodName}
	params := map[string]any{"message": "hello"}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := exec.invoke(ctx, desc, device, params, collection.ModeExecute); err != nil {
			b.Fatalf("invoke: %v", err)
		}
	}
}

// BenchmarkInvokeChild_InProcess is the control for the benchmark above:
// the identical Collection method invoked with no process boundary at all.
// The difference between the two is the true price of Section 17.5's
// isolation requirement, which is the number worth quoting, rather than
// the absolute spawn time (which says as much about the machine as about
// the design).
func BenchmarkInvokeChild_InProcess(b *testing.B) {
	req := wire.ChildRequest{
		FQCN:       nativeIPCEchoMethodName,
		JobID:      "bench-job",
		DeviceID:   "bench-dev",
		DeviceName: "router1",
		DeviceHost: "10.0.0.1",
		SSHPort:    22,
		Params:     map[string]any{"message": "hello"},
		Secrets:    map[string]string{"username": "admin"},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if resp := external.InvokeRequest(ctx, collection.Lookup, req); resp.Error != "" {
			b.Fatalf("InvokeRequest: %s", resp.Error)
		}
	}
}
