//go:build unix

// Package loader: benchmarks of calling an external method against an in-
// process one.
package loader

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// BenchmarkProxyInvoke measures the whole cost of one call to an external
// method whose program does nothing: re-verifying the program (a directory
// check, a permission check and a SHA-256 of the file), starting a
// process, one request in on stdin and one response out on file
// descriptor 3.
//
// BenchmarkInProcessBaseline is its control, the same trivial method
// called with no boundary at all. The difference is the price of running
// third-party code in a process of its own, which is the number to quote.
// Neither measures a real method's own work or any network round trip.
func BenchmarkProxyInvoke(b *testing.B) {
	dir := programDir(b)
	oneMethodProgram(b, dir, "loadertest.bench.run", false, `cat >/dev/null
printf '{"changed":true}\n' >&3`)
	d := loadOne(b, dir, "loadertest.bench.run", testOptions())
	rc := newRecordingContext()
	device := newSSHDevice()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Invoke(ctx, rc, device, nil); err != nil {
			b.Fatalf("Invoke: %v", err)
		}
	}
}

// BenchmarkInProcessBaseline is BenchmarkProxyInvoke's control: the same
// do-nothing method, called directly.
func BenchmarkInProcessBaseline(b *testing.B) {
	method := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}
	rc := newRecordingContext()
	device := newSSHDevice()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := method(ctx, rc, device, nil); err != nil {
			b.Fatal(err)
		}
	}
}
