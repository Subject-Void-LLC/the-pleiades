package inventory_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestParseLifecycleState_RoundTripsEveryState asserts Parse is the exact
// inverse of String for all eight states. A repository stores the string
// form, so any state whose two halves disagree becomes a device that cannot
// be loaded at all.
func TestParseLifecycleState_RoundTripsEveryState(t *testing.T) {
	states := []inventory.LifecycleState{
		inventory.StateDiscovered,
		inventory.StateQuarantined,
		inventory.StateOnboarding,
		inventory.StateActive,
		inventory.StateSimulateLocked,
		inventory.StateUnreachable,
		inventory.StateDecommissioning,
		inventory.StateArchived,
	}

	for _, want := range states {
		t.Run(want.String(), func(t *testing.T) {
			got, err := inventory.ParseLifecycleState(want.String())
			if err != nil {
				t.Fatalf("ParseLifecycleState(%q) returned error: %v", want.String(), err)
			}
			if got != want {
				t.Errorf("ParseLifecycleState(%q) = %v, want %v", want.String(), got, want)
			}
		})
	}
}

// TestParseLifecycleState_RejectsUnknown covers the case that matters for
// safety. StateActive is the only state permitting execution, so an
// unrecognized value must never resolve to it: a device from a newer build,
// or a corrupted column, would otherwise silently become executable.
func TestParseLifecycleState_RejectsUnknown(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "unknown word", input: "banana"},
		{name: "String's own fallback", input: "unknown"},
		{name: "wrong case", input: "Active"},
		{name: "underscore instead of hyphen", input: "simulate_locked"},
		{name: "surrounding space", input: " active "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := inventory.ParseLifecycleState(tt.input)
			if err == nil {
				t.Fatalf("ParseLifecycleState(%q) succeeded, want an error", tt.input)
			}
			if got == inventory.StateActive {
				t.Errorf("ParseLifecycleState(%q) returned StateActive on failure, which would make an unrecognized device executable", tt.input)
			}
			if got.CanExecute() {
				t.Errorf("ParseLifecycleState(%q) returned an executable state on failure", tt.input)
			}
		})
	}
}

// TestLifecycleState_OnlyActiveCanExecute pins the guard every dispatch
// path is meant to consult.
func TestLifecycleState_OnlyActiveCanExecute(t *testing.T) {
	tests := []struct {
		state inventory.LifecycleState
		want  bool
	}{
		{inventory.StateDiscovered, false},
		{inventory.StateQuarantined, false},
		{inventory.StateOnboarding, false},
		{inventory.StateActive, true},
		{inventory.StateSimulateLocked, false},
		{inventory.StateUnreachable, false},
		{inventory.StateDecommissioning, false},
		{inventory.StateArchived, false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := tt.state.CanExecute(); got != tt.want {
				t.Errorf("%v.CanExecute() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}
