package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

func TestLifecycleStateCanExecute(t *testing.T) {
	cases := []struct {
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

	for _, tc := range cases {
		t.Run(tc.state.String(), func(t *testing.T) {
			if got := tc.state.CanExecute(); got != tc.want {
				t.Errorf("%s.CanExecute() = %v, want %v", tc.state, got, tc.want)
			}
		})
	}
}

func TestLifecycleStateStringUnknown(t *testing.T) {
	var s inventory.LifecycleState = 255
	if s.String() != "unknown" {
		t.Errorf("expected 'unknown' for an out-of-range state, got %q", s.String())
	}
}
