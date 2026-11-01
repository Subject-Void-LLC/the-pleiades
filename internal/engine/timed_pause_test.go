// Tests of the timed pause action: what it takes, that it holds once for the
// whole run rather than once per device, that a check does not wait, and
// that a canceled run stops it.
package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestPauseDuration: seconds or minutes, a whole number from 1 to a day; a
// prompt, no duration, both, or anything else is refused, naming why.
func TestPauseDuration(t *testing.T) {
	for _, tc := range []struct {
		params map[string]any
		want   time.Duration
	}{
		{map[string]any{"seconds": 3}, 3 * time.Second},
		{map[string]any{"minutes": float64(2)}, 2 * time.Minute},
		{map[string]any{"minutes": 1440}, 24 * time.Hour},
	} {
		if got, err := engine.PauseDuration(tc.params); err != nil || got != tc.want {
			t.Errorf("%v: %v, %v", tc.params, got, err)
		}
	}
	for _, tc := range []struct {
		params map[string]any
		want   string
	}{
		{map[string]any{}, "waits for a person"},
		{map[string]any{"prompt": "go on?"}, "waits for a person to answer"},
		{map[string]any{"seconds": 1, "minutes": 1}, "not both"},
		{map[string]any{"seconds": 0}, "at least 1"},
		{map[string]any{"seconds": 1.5}, "whole number"},
		{map[string]any{"seconds": "5"}, "whole number"},
		{map[string]any{"minutes": 1441}, "longer than the most"},
		{map[string]any{"hours": 1}, `"hours" is not a parameter`},
	} {
		if _, err := engine.PauseDuration(tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want one saying %q", tc.params, err, tc.want)
		}
	}
}

// TestPause_HoldsOnceForTheWholeRun: under hosts: with two devices, a pause
// runs once with no device, holds for its duration, and changes nothing; the
// task after it runs on both devices.
func TestPause_HoldsOnceForTheWholeRun(t *testing.T) {
	devices := []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "d1", StubName: "d1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "d2", StubName: "d2", StubState: inventory.StateActive},
	}
	x := engine.NewExecutor(mapResolver{"all": devices}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 1)
	started := time.Now()
	result, err := x.Run(context.Background(), yamlRunbook(t, "id: p\nhosts: all\ntasks:\n  - name: hold\n    pause:\n      seconds: 1\n  - name: after\n    noop: {}\n"))
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	if held := time.Since(started); held < time.Second || held > 3*time.Second {
		t.Errorf("the run held %s, want about one second (once, not once per device)", held)
	}
	var pauses, after int
	for _, nr := range result.Nodes {
		switch nr.NodeID {
		case "tasks[0]":
			pauses++
			if nr.Device != "" || nr.Changed {
				t.Errorf("the pause ran on %q, changed %v", nr.Device, nr.Changed)
			}
		case "tasks[1]":
			after++
		}
	}
	if pauses != 1 || after != 2 {
		t.Errorf("%d pause results and %d after it, want 1 and 2", pauses, after)
	}
}

// TestPause_ACheckDoesNotWaitAndACancelStopsIt.
func TestPause_ACheckDoesNotWaitAndACancelStopsIt(t *testing.T) {
	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithMode(collection.ModeCheck))
	started := time.Now()
	if result, err := x.Run(context.Background(), yamlRunbook(t, "id: p\ntasks:\n  - name: hold\n    pause:\n      minutes: 30\n")); err != nil || result.HasErrors() {
		t.Fatalf("check: %v, %+v", err, result.Nodes)
	}
	if time.Since(started) > 2*time.Second {
		t.Error("a check waited out its pause")
	}

	real := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started = time.Now()
	result, _ := real.Run(ctx, yamlRunbook(t, "id: p\ntasks:\n  - name: hold\n    pause:\n      minutes: 30\n"))
	if time.Since(started) > 5*time.Second {
		t.Error("a canceled run kept pausing")
	}
	if len(result.Nodes) > 0 && result.Nodes[0].Err == nil {
		t.Error("a canceled pause reported success")
	}
}
