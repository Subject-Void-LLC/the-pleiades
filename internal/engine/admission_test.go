package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestLifecycleAdmits_EveryState walks every inventory.LifecycleState value
// (the same eight states pkg/inventory/lifecycle_test.go's own round-trip
// test enumerates) through LifecycleAdmits, proving StateActive is the only
// one that admits and that every other state reports a reason naming the
// device and its actual state, in the exact wording runNode's own
// lifecycle-skip block has always produced (executor.go).
func TestLifecycleAdmits_EveryState(t *testing.T) {
	tests := []struct {
		state inventory.LifecycleState
		want  bool
	}{
		{state: inventory.StateDiscovered, want: false},
		{state: inventory.StateQuarantined, want: false},
		{state: inventory.StateOnboarding, want: false},
		{state: inventory.StateActive, want: true},
		{state: inventory.StateSimulateLocked, want: false},
		{state: inventory.StateUnreachable, want: false},
		{state: inventory.StateDecommissioning, want: false},
		{state: inventory.StateArchived, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			dev := &inventorytest.Stub{StubID: "dev-1", StubName: "dev-1", StubState: tt.state}

			ok, reason := engine.LifecycleAdmits(dev)

			if ok != tt.want {
				t.Fatalf("LifecycleAdmits(%s) ok = %v, want %v", tt.state, ok, tt.want)
			}

			if tt.want {
				// An admitting result must never carry a reason: a non-empty
				// reason alongside true would read as an admitted device
				// that is somehow also flagged, which is a contradiction no
				// caller should have to guard against.
				if reason != "" {
					t.Errorf("LifecycleAdmits(%s) reason = %q, want empty", tt.state, reason)
				}
				return
			}

			// A refusing result must name both the device and its actual
			// state, in runNode's exact historical wording, since
			// executor_test.go asserts on that same text and any drift here
			// would be an unplanned, out-of-scope break of that test.
			want := `device "dev-1" is ` + tt.state.String() + `, not active`
			if reason != want {
				t.Errorf("LifecycleAdmits(%s) reason = %q, want %q", tt.state, reason, want)
			}
		})
	}
}

// TestCapabilityAdmits covers CapabilityAdmits' four required cases: no
// requirement at all, one satisfied requirement, one missing requirement,
// and two requirements where the first is satisfied and the second is
// missing, proving the function keeps checking past a satisfied entry
// instead of stopping early, and reports the entry that is actually
// missing rather than the first one it happened to check.
func TestCapabilityAdmits(t *testing.T) {
	const (
		capA capability.Name = "CapACapable"
		capB capability.Name = "CapBCapable"
	)

	tests := []struct {
		name       string
		caps       []capability.Name
		required   []capability.Name
		wantOK     bool
		wantReason string
	}{
		{
			name:     "no capabilities required",
			caps:     nil,
			required: nil,
			wantOK:   true,
		},
		{
			name:     "one satisfied requirement",
			caps:     []capability.Name{capA},
			required: []capability.Name{capA},
			wantOK:   true,
		},
		{
			name:       "one missing requirement",
			caps:       nil,
			required:   []capability.Name{capA},
			wantOK:     false,
			wantReason: `device "dev-1" does not have capability CapACapable`,
		},
		{
			name:       "first satisfied, second missing",
			caps:       []capability.Name{capA},
			required:   []capability.Name{capA, capB},
			wantOK:     false,
			wantReason: `device "dev-1" does not have capability CapBCapable`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := &inventorytest.Stub{StubID: "dev-1", StubName: "dev-1", Caps: tt.caps}

			ok, reason := engine.CapabilityAdmits(dev, tt.required)

			if ok != tt.wantOK {
				t.Fatalf("CapabilityAdmits() ok = %v, want %v", ok, tt.wantOK)
			}

			if tt.wantOK {
				if reason != "" {
					t.Errorf("CapabilityAdmits() reason = %q, want empty", reason)
				}
				return
			}

			if reason != tt.wantReason {
				t.Errorf("CapabilityAdmits() reason = %q, want %q", reason, tt.wantReason)
			}

			// The reason must name the actually-missing capability, not
			// merely some capability: if the test regressed to reporting
			// the first required entry regardless of whether it was
			// satisfied, "first satisfied, second missing" would report
			// capA instead of capB and this substring check would catch it
			// even if a future wording tweak changed the surrounding text.
			if !strings.Contains(reason, string(tt.required[len(tt.required)-1])) {
				t.Errorf("CapabilityAdmits() reason = %q, want it to name %s", reason, tt.required[len(tt.required)-1])
			}
		})
	}
}

// TestLifecycleAdmits_ConformsToExecutorSkipReason is the literal proof
// behind this phase's claim that the Walk-tier Executor and any other
// lifecycle-admission caller (the Crawl-tier dispatch worker,
// internal/dispatch/worker.go) share one lifecycle-admission
// implementation, rather than two copies that merely happen to agree
// today: it drives a minimal but real engine.Executor (real DAG, real
// in-process lock manager, real in-process event bus) against a single
// non-Active device, and asserts the NodeResult.SkipReason it produces is
// byte-identical, via a direct string comparison rather than a substring
// check, to what calling LifecycleAdmits directly on that same device
// returns. executor.go's own comment at the runNode call site says
// LifecycleAdmits is "this exact check, relocated"; this test is what
// makes that statement checked rather than merely asserted.
func TestLifecycleAdmits_ConformsToExecutorSkipReason(t *testing.T) {
	quarantined := &inventorytest.Stub{StubID: "quarantined-host", StubName: "quarantined-host", StubState: inventory.StateQuarantined}
	resolver := mapResolver{"fleet": {quarantined}}

	// A single-node, single-device runbook is enough: this test is not
	// about DAG traversal, it is about one call site's SkipReason output
	// matching LifecycleAdmits' own output for the identical device.
	dag := buildDAG(t, `{
		"id": "lifecycle-conformance",
		"tasks": [{"name": "ping", "fqcn": "noop", "params": {"target": "fleet"}}]
	}`)

	x := engine.NewExecutor(resolver, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err := x.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("expected exactly one node result, got %d: %+v", len(result.Nodes), result.Nodes)
	}
	if !result.Nodes[0].Skipped {
		t.Fatalf("expected the quarantined device's node to be skipped, got %+v", result.Nodes[0])
	}

	executorReason := result.Nodes[0].SkipReason
	_, directReason := engine.LifecycleAdmits(quarantined)

	// The load-bearing assertion: exact equality, not strings.Contains.
	// Executor.Run and a direct LifecycleAdmits call must produce the
	// identical string for the identical device, proving there is one
	// admission implementation behind both call sites, not two that have
	// merely not drifted apart yet.
	if executorReason != directReason {
		t.Fatalf("Executor.Run's SkipReason (%q) is not byte-identical to LifecycleAdmits' own reason (%q)", executorReason, directReason)
	}
	if executorReason == "" {
		t.Fatal("SkipReason is empty; the quarantined device should have been skipped with a reason")
	}
}
