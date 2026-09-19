// Package user_test: tests of the identity.user.* checks.
package user_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/user"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestUserChecks covers each user method's check on the fake harness,
// with the real run from the same state as its control: no useradd,
// usermod or userdel is sent, the change decision is the real run's, the
// predicted account applies exactly what the task names (a new account's
// unnamed uid, gid, home and shell are the system's to assign, so they
// are absent), no undo is recorded, and modify refuses a missing account
// as its real run does.
func TestUserChecks(t *testing.T) {
	existing := map[string]any{"exists": true, "uid": 1000, "gid": 1000, "comment": "Deploy", "home": "/home/deploy", "shell": "/bin/bash"}
	with := func(changes map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range existing {
			out[k] = v
		}
		for k, v := range changes {
			out[k] = v
		}
		return out
	}
	for _, tc := range []struct {
		name        string
		fqcn        string
		run         collection.Method
		state       userState
		extra       map[string]any
		wantChanged bool
		wantAfter   map[string]any
		refused     bool
	}{
		{"create, absent", "identity.user.create", user.Create, absentUser, nil, true, map[string]any{"exists": true}, false},
		{"create, absent, attributes named", "identity.user.create", user.Create, absentUser, map[string]any{"uid": 1500, "shell": "/bin/sh"}, true, map[string]any{"exists": true, "uid": 1500, "shell": "/bin/sh"}, false},
		{"create, present, converged", "identity.user.create", user.Create, presentUser, map[string]any{"shell": "/bin/bash"}, false, existing, false},
		{"create, present, other shell", "identity.user.create", user.Create, presentUser, map[string]any{"shell": "/bin/sh"}, true, with(map[string]any{"shell": "/bin/sh"}), false},
		{"modify, other comment", "identity.user.modify", user.Modify, presentUser, map[string]any{"comment": "Deployer"}, true, with(map[string]any{"comment": "Deployer"}), false},
		{"modify, converged", "identity.user.modify", user.Modify, presentUser, map[string]any{"comment": "Deploy"}, false, existing, false},
		{"modify, absent", "identity.user.modify", user.Modify, absentUser, map[string]any{"comment": "x"}, false, nil, true},
		{"remove, present", "identity.user.remove", user.Remove, presentUser, nil, true, map[string]any{"exists": false, "uid": 0, "gid": 0, "comment": "", "home": "", "shell": ""}, false},
		{"remove, absent", "identity.user.remove", user.Remove, absentUser, nil, false, map[string]any{"exists": false, "uid": 0, "gid": 0, "comment": "", "home": "", "shell": ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			h := newHarness(t, tc.state)
			checked, checkErr := d.Check(context.Background(), h.rc, h.device, h.params("deploy", tc.extra))
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check changed the account database: %v", calls)
			}
			run := newHarness(t, tc.state)
			ran, runErr := tc.run(context.Background(), run.rc, run.device, run.params("deploy", tc.extra))
			if tc.refused {
				if checkErr == nil || runErr == nil || checkErr.Error() != runErr.Error() {
					t.Errorf("check refused with %v, run with %v; want the same refusal", checkErr, runErr)
				}
				return
			}
			if checkErr != nil || runErr != nil {
				t.Fatalf("check: %v; run: %v", checkErr, runErr)
			}
			if checked.Changed != tc.wantChanged || ran.Changed != checked.Changed {
				t.Errorf("check changed %v, run %v; want %v", checked.Changed, ran.Changed, tc.wantChanged)
			}
			if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}
			diff, _ := h.rc.stats[sdk.StatDiff].(map[string]any)
			if after, _ := diff["after"].(map[string]any); !reflect.DeepEqual(after, tc.wantAfter) {
				t.Errorf("predicted %v, want %v", after, tc.wantAfter)
			}
		})
	}
}
