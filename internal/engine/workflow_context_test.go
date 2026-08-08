package engine_test

import (
	"reflect"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestInProcessWorkflowContext_MergeAndRead confirms a merged stats
// payload round-trips through Read, nested nodeID then deviceID.
func TestInProcessWorkflowContext_MergeAndRead(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()

	if err := wc.Merge("precheck", "host1", map[string]interface{}{"needs_reboot": true}); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	got, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	want := map[string]interface{}{
		"precheck": map[string]interface{}{
			"host1": map[string]interface{}{"needs_reboot": true},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

// TestInProcessWorkflowContext_ControllerSideEmptyDeviceID confirms a
// controller-side task (no target device) merges cleanly under the empty
// string device key, exactly as Executor calls it for a task with no
// Params["target"].
func TestInProcessWorkflowContext_ControllerSideEmptyDeviceID(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()

	if err := wc.Merge("precheck", "", map[string]interface{}{"ok": true}); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	got, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	byDevice, ok := got["precheck"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected precheck to be a map, got %#v", got["precheck"])
	}
	if _, ok := byDevice[""]; !ok {
		t.Fatalf("expected an entry under the empty device key, got %#v", byDevice)
	}
}

// TestInProcessWorkflowContext_OverwritesSamePair confirms a second Merge
// for the identical nodeID/deviceID pair replaces the first, matching
// PLAN.md Section 27's "ephemeral aggregation" always reflecting the
// latest report, not an accumulating history.
func TestInProcessWorkflowContext_OverwritesSamePair(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()

	_ = wc.Merge("precheck", "host1", map[string]interface{}{"attempt": 1})
	_ = wc.Merge("precheck", "host1", map[string]interface{}{"attempt": 2})

	got, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	byDevice := got["precheck"].(map[string]interface{})
	stats := byDevice["host1"].(map[string]interface{})
	if stats["attempt"] != 2 {
		t.Fatalf("expected the second Merge to win, got %#v", stats)
	}
}

// TestInProcessWorkflowContext_ReadIsIsolatedFromLaterMerges confirms Read
// returns a snapshot, not a live view: mutating the map Read returned, or
// merging again afterward, must never change a previously returned
// result.
func TestInProcessWorkflowContext_ReadIsIsolatedFromLaterMerges(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()
	_ = wc.Merge("precheck", "host1", map[string]interface{}{"needs_reboot": true})

	first, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Mutate the returned snapshot directly.
	first["precheck"].(map[string]interface{})["host1"].(map[string]interface{})["needs_reboot"] = false

	// And merge new data for an unrelated pair afterward.
	_ = wc.Merge("precheck", "host2", map[string]interface{}{"needs_reboot": false})

	second, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	byDevice := second["precheck"].(map[string]interface{})
	host1Stats := byDevice["host1"].(map[string]interface{})
	if host1Stats["needs_reboot"] != true {
		t.Fatalf("expected host1's original merge to be unaffected by mutating a prior Read result, got %#v", host1Stats)
	}
}

// TestInProcessWorkflowContext_MutatingCallerStatsMapAfterMerge confirms
// Merge is not holding onto the caller's own map reference indefinitely in
// a way that a later mutation to the original could reach back in. Given
// Merge's signature accepts a plain map, this documents the actual
// contract: Merge stores exactly the map value it is handed, so a caller
// that wants isolation must not reuse or mutate a stats map after handing
// it to Merge. Executor (executor.go) never does; it builds one stats
// map per call and never mutates it again afterward.
func TestInProcessWorkflowContext_MutatingCallerStatsMapAfterMerge(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()
	stats := map[string]interface{}{"needs_reboot": true}
	_ = wc.Merge("precheck", "host1", stats)

	stats["needs_reboot"] = false

	got, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	byDevice := got["precheck"].(map[string]interface{})
	host1Stats := byDevice["host1"].(map[string]interface{})
	if host1Stats["needs_reboot"] != false {
		t.Fatalf("expected Merge to store the exact map handed to it, got %#v", host1Stats)
	}
}

// TestInProcessWorkflowContext_ConcurrentMerge exercises many goroutines
// merging distinct nodeID/deviceID pairs at once, the same fan-out shape
// Executor's device-level worker pool produces, and confirms every merge
// survives with no data race (run with -race).
func TestInProcessWorkflowContext_ConcurrentMerge(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			deviceID := "host" + string(rune('a'+i%26))
			_ = wc.Merge("precheck", deviceID, map[string]interface{}{"i": i})
		}(i)
	}
	wg.Wait()

	got, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	byDevice := got["precheck"].(map[string]interface{})
	if len(byDevice) == 0 {
		t.Fatalf("expected at least one merged device entry, got none")
	}
}

// TestInProcessWorkflowContext_ReadEmpty confirms Read on a
// WorkflowContext with no Merge calls yet returns an empty, non-nil map
// rather than an error, so Executor can always call Eval against it
// unconditionally, even before the first task has run.
func TestInProcessWorkflowContext_ReadEmpty(t *testing.T) {
	wc := engine.NewInProcessWorkflowContext()
	got, err := wc.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty map, got %#v", got)
	}
}
