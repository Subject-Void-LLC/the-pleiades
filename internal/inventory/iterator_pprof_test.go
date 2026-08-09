package inventory_test

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// heapProfileHeaderPattern matches runtime/pprof's debug=1 legacy text
// format header line for a heap profile: "heap profile: N: BYTES [N:
// BYTES] @ heap/RATE". The first N: BYTES pair is in-use objects/bytes at
// the moment WriteTo was called; the bracketed pair is cumulative
// alloc-since-start. Capture group 1 (in-use bytes) is what this test
// tracks across checkpoints.
var heapProfileHeaderPattern = regexp.MustCompile(`^heap profile: \d+: (\d+) \[`)

// heapInUseBytes forces a GC, then captures a real pprof heap profile and
// returns its in-use byte count. It deliberately calls
// pprof.Lookup("heap").WriteTo(w, 1), not pprof.WriteHeapProfile: the
// latter always writes the gzip/protobuf format (debug=0), which is not
// parseable here without a new dependency. Debug=1 is Go's own documented
// legacy text format, produced by a single, stable fmt.Fprintf in the
// runtime/pprof standard library.
func heapInUseBytes(t *testing.T) int64 {
	t.Helper()
	// Two GC cycles, not one: runtime.MemProfile's records are flushed on
	// GC-cycle boundaries and can lag by up to one cycle, so a single GC
	// can under-report an allocation made just before it. Under-reporting
	// biases this test toward a false pass, not a flake, but a second
	// cycle is cheap insurance against that bias specifically.
	runtime.GC()
	runtime.GC()
	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 1); err != nil {
		t.Fatalf("writing heap profile: %v", err)
	}
	line, _, _ := bytes.Cut(buf.Bytes(), []byte("\n"))
	m := heapProfileHeaderPattern.FindSubmatch(line)
	if m == nil {
		t.Fatalf("heap profile header did not match the expected format: %q", line)
	}
	inUse, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		t.Fatalf("parsing heap profile in-use bytes from %q: %v", m[1], err)
	}
	return inUse
}

// TestIteratorHeapProfileStaysFlat is the literal Release Gate proof
// ("A test queries a group of 50,000 mock devices, and pprof proves that
// memory allocation remains completely flat"). TestIteratorMemoryFlatline
// (iterator_test.go) already proves the same property via
// runtime.MemStats, a coarser two-point before/after signal; this test
// uses the actual pprof heap-profiling mechanism, sampled at five
// checkpoints across one real 50,000-device stream.
//
// A spread-among-checkpoints check alone cannot tell "flat and small" from
// "flat and huge": if the whole 50,000-row result set were held resident
// (e.g. a regression that dropped the batch Limit), every checkpoint would
// sit on the same elevated plateau together, spread near zero, and a
// spread-only assertion would pass regardless. This test therefore also
// captures a baseline right when iteration starts and asserts every
// checkpoint's growth from that baseline stays under a ceiling far below
// what materializing the whole stream would cost, which is what actually
// proves "flat," not merely "stable."
//
// runtime.MemProfileRate is set to 1 (exact accounting) only around the
// iteration itself, not the 50,000-row seed above it: pprof.Lookup("heap")
// is backed by Go's *sampled* memory profiler (512 KiB/sample by default),
// and at the default rate, sampling noise alone could make a tight "stays
// flat" threshold flake in CI even on otherwise-correct code, so this test
// buys exactness at the cost of extra CPU, but only for the part of the
// test the assertion actually depends on.
func TestIteratorHeapProfileStaysFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping heap profile flatline test in short mode")
	}

	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	const numDevices = 50000
	const checkpointEvery = 10000
	// Materializing the whole 50,000-row stream (a Limit-dropped
	// regression) costs many megabytes; a healthy batched stream's own
	// measured growth is well under 1 MB. 10 MB sits between the two,
	// generous headroom above real variance while still failing hard on
	// the regression this test exists to catch.
	const maxGrowthFromBaselineMB = 10.0

	builders := make([]*ent.DeviceCreate, numDevices)
	for i := 0; i < numDevices; i++ {
		builders[i] = client.Device.Create().
			SetName(fmt.Sprintf("pprof-router-%d", i)).
			SetType("cisco_router").
			SetProperties(map[string]interface{}{
				"host": "10.0.0.1",
			})
	}
	bulkCreateDevices(t, ctx, client, builders)

	factory := inventory.NewItemFactory()
	repo := inventory.NewEntRepository(client, factory)

	iter, err := repo.GetGroup(ctx, pkginventory.Selector{})
	if err != nil {
		t.Fatalf("failed to get group iterator: %v", err)
	}
	defer iter.Close()

	originalRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = originalRate }()

	baseline := heapInUseBytes(t)

	var checkpoints []int64
	count := 0
	for iter.Next(ctx) {
		if iter.Item() == nil {
			t.Fatalf("iterator returned nil item")
		}
		count++
		if count%checkpointEvery == 0 {
			checkpoints = append(checkpoints, heapInUseBytes(t))
		}
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	if count != numDevices {
		t.Fatalf("expected to iterate %d devices, got %d", numDevices, count)
	}
	const wantCheckpoints = numDevices / checkpointEvery
	if len(checkpoints) != wantCheckpoints {
		t.Fatalf("expected %d checkpoints, got %d: %v", wantCheckpoints, len(checkpoints), checkpoints)
	}

	t.Logf("pprof heap in-use bytes: baseline %d, at each 10k-device checkpoint: %v", baseline, checkpoints)

	for i, b := range checkpoints {
		growthMB := float64(b-baseline) / 1024 / 1024
		if growthMB > maxGrowthFromBaselineMB {
			t.Fatalf("pprof heap profile shows allocation is NOT flat! Checkpoint %d grew %.2f MB from baseline (max %.2f MB): checkpoints %v, baseline %d",
				i+1, growthMB, maxGrowthFromBaselineMB, checkpoints, baseline)
		}
	}

	// Also compare checkpoints against each other (skipping the first as
	// allocator/GC warm-up): a real, separate signal from the
	// baseline-growth check above, since a slow, unbounded-in-the-limit
	// leak that never crosses the baseline ceiling within this one run
	// would still show up as rising spread between checkpoints.
	settled := checkpoints[1:]
	minBytes, maxBytes := settled[0], settled[0]
	for _, b := range settled {
		if b < minBytes {
			minBytes = b
		}
		if b > maxBytes {
			maxBytes = b
		}
	}
	spreadMB := float64(maxBytes-minBytes) / 1024 / 1024
	t.Logf("spread across checkpoints 2-%d: %.2f MB", len(checkpoints), spreadMB)
	if spreadMB > 5.0 {
		t.Fatalf("pprof heap profile shows allocation is NOT flat! Spread of %.2f MB across checkpoints %v", spreadMB, checkpoints)
	}
}
