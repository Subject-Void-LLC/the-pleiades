// Tests of the identity.user.* checks against a primary group the device
// does not have yet, as when a task before them creates it.
package user_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/user"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestUserChecks_AGroupNotYetMadeCannotBeChecked covers a check whose
// group is absent: the call is reported unchecked with the group named
// (LESSONS 255's rule for state an earlier task may make), never failed
// and never predicted to succeed. The real run from the same state is the
// control: it still refuses the missing group.
func TestUserChecks_AGroupNotYetMadeCannotBeChecked(t *testing.T) {
	absentGroup := func(s userState) userState {
		s.groupExitOverride = 2
		return s
	}
	for _, tc := range []struct {
		name  string
		fqcn  string
		run   collection.Method
		state userState
		extra map[string]any
	}{
		{"create, absent account", "identity.user.create", user.Create, absentGroup(absentUser),
			map[string]any{"group": "app", "home": "/srv/app"}},
		{"create, present account", "identity.user.create", user.Create, absentGroup(presentUser),
			map[string]any{"group": "app"}},
		{"modify, present account", "identity.user.modify", user.Modify, absentGroup(presentUser),
			map[string]any{"group": "app", "shell": "/bin/sh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := collection.Lookup(tc.fqcn)
			h := newHarness(t, tc.state)
			_, err := d.Check(context.Background(), h.rc, h.device, h.params("deploy", tc.extra))
			var cannot *collection.CannotCheckError
			if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, `group "app" does not exist on the device yet`) {
				t.Fatalf("check err = %v, want a cannot-check answer naming the missing group", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check changed the account database: %v", calls)
			}

			// A present account's real run looks the group up and refuses;
			// a new one's hands it to useradd, which refuses on a real
			// device (this harness's useradd does not, so the control is
			// that the group reaches it).
			run := newHarness(t, tc.state)
			_, err = tc.run(context.Background(), run.rc, run.device, run.params("deploy", tc.extra))
			if tc.state.exists {
				if err == nil || !strings.Contains(err.Error(), `group "app" does not exist`) {
					t.Errorf("real run err = %v, want it to refuse the missing group", err)
				}
				return
			}
			if calls := run.invocations(t); err != nil || len(calls) != 1 || !strings.Contains(calls[0], "useradd -g app") {
				t.Errorf("real run: err %v, calls %v; want one useradd naming -g app", err, calls)
			}
		})
	}
}
