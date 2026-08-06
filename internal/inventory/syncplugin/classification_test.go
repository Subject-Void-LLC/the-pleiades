package syncplugin_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// TestClassifyPath covers the shared rule-tree walk every plugin routes
// through. The cases that matter are the failures: an unresolvable path
// must quarantine rather than error, because Section 6g requires the sync
// to continue and the device to stay visible for manual review.
func TestClassifyPath(t *testing.T) {
	rs := classification.DefaultRuleSet()

	tests := []struct {
		name            string
		path            []string
		state           inventory.LifecycleState
		wantQuarantined bool
		wantType        string
		wantCapability  capability.Name
		wantReason      string
	}{
		{
			name:           "known cisco ios path",
			path:           []string{"network_device", "cisco", "ios"},
			state:          inventory.StateActive,
			wantType:       "cisco_router",
			wantCapability: capability.NameCiscoIOS,
		},
		{
			name:           "capabilities accumulate down the tree",
			path:           []string{"linux_server", "debian_family"},
			state:          inventory.StateActive,
			wantType:       "linux_server",
			wantCapability: capability.NameApt,
		},
		{
			name:            "empty path quarantines",
			path:            nil,
			state:           inventory.StateActive,
			wantQuarantined: true,
			wantReason:      "no classification path",
		},
		{
			name:            "unknown path quarantines",
			path:            []string{"network_device", "acme", "unobtainium"},
			state:           inventory.StateActive,
			wantQuarantined: true,
			wantReason:      "did not resolve",
		},
		{
			// A path that exists in the tree but assigns no type is a real
			// shape in this rule set: linux_server.debian_family alone
			// inherits a type, but a bare intermediate that never assigns
			// one must not hydrate as an empty string.
			name:            "path matching no rule at all quarantines",
			path:            []string{"network_device"},
			state:           inventory.StateActive,
			wantQuarantined: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncplugin.ClassifyPath(rs, tt.path, tt.state)

			if got.Quarantined() != tt.wantQuarantined {
				t.Fatalf("Quarantined() = %v, want %v (classification: %+v)", got.Quarantined(), tt.wantQuarantined, got)
			}

			if tt.wantQuarantined {
				if got.Type != "" {
					t.Errorf("a quarantined classification must carry no type, got %q", got.Type)
				}
				if len(got.Capabilities) != 0 {
					t.Errorf("a quarantined classification must carry no capabilities, got %v", got.Capabilities)
				}
				if got.Reason == "" {
					t.Error("a quarantined classification must explain why")
				}
				if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
					t.Errorf("Reason = %q, want it to contain %q", got.Reason, tt.wantReason)
				}
				return
			}

			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if got.State != tt.state {
				t.Errorf("State = %v, want %v", got.State, tt.state)
			}
			var found bool
			for _, c := range got.Capabilities {
				if c == tt.wantCapability {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected capability %q in %v", tt.wantCapability, got.Capabilities)
			}
		})
	}
}

// TestQuarantine proves the constructor produces exactly the shape
// reconciliation branches on, so a quarantined device can never be
// mistaken for a placeable one.
func TestQuarantine(t *testing.T) {
	got := syncplugin.Quarantine("upstream returned no software type")

	if !got.Quarantined() {
		t.Error("Quarantine() must produce a quarantined classification")
	}
	if got.State != inventory.StateQuarantined {
		t.Errorf("State = %v, want %v", got.State, inventory.StateQuarantined)
	}
	if got.State.CanExecute() {
		t.Error("a quarantined device must not be executable")
	}
	if got.Reason == "" {
		t.Error("Quarantine() must carry its reason")
	}
}

// TestReconciliation_Counts proves the report's accessors, which every
// caller uses instead of walking Results themselves.
func TestReconciliation_Counts(t *testing.T) {
	var r syncplugin.Reconciliation
	if r.Total() != 0 {
		t.Fatalf("a zero Reconciliation must be empty, got %d", r.Total())
	}
	if r.Count(syncplugin.OutcomeAdded) != 0 {
		t.Error("a zero Reconciliation must count nothing")
	}

	r.Results = []syncplugin.DeviceResult{
		{Name: "sw1", Outcome: syncplugin.OutcomeAdded},
		{Name: "sw2", Outcome: syncplugin.OutcomeAdded},
		{Name: "sw3", Outcome: syncplugin.OutcomeUnchanged},
		{Name: "sw4", Outcome: syncplugin.OutcomeQuarantined, Reason: "no software type"},
	}

	if got := r.Total(); got != 4 {
		t.Errorf("Total() = %d, want 4", got)
	}
	if got := r.Count(syncplugin.OutcomeAdded); got != 2 {
		t.Errorf("Count(added) = %d, want 2", got)
	}
	if got := r.Count(syncplugin.OutcomeConflict); got != 0 {
		t.Errorf("Count(conflict) = %d, want 0", got)
	}
}
