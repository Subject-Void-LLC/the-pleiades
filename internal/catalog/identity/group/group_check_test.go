// Package group_test: tests of the identity.group.* checks.
package group_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/group"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestGroupChecks covers each group method's check on the fake harness,
// with the real run from the same state as its control: no groupadd,
// groupmod or groupdel is sent, the change decision is the real run's, the
// predicted group is what the task names (a gid it leaves out is the
// system's to choose, so it is absent), no undo is recorded, and modify
// refuses a missing group as its real run does.
func TestGroupChecks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fqcn        string
		run         collection.Method
		state       groupState
		extra       map[string]any
		wantChanged bool
		wantAfter   map[string]any
		refused     bool
	}{
		{"create, absent", "identity.group.create", group.Create, absentGroup, nil, true, map[string]any{"exists": true}, false},
		{"create, absent, gid named", "identity.group.create", group.Create, absentGroup, map[string]any{"gid": 3000}, true, map[string]any{"exists": true, "gid": 3000}, false},
		{"create, present", "identity.group.create", group.Create, presentGroup, nil, false, map[string]any{"exists": true, "gid": 2000}, false},
		{"create, present, other gid", "identity.group.create", group.Create, presentGroup, map[string]any{"gid": 3000}, true, map[string]any{"exists": true, "gid": 3000}, false},
		{"modify, other gid", "identity.group.modify", group.Modify, presentGroup, map[string]any{"gid": 3000}, true, map[string]any{"exists": true, "gid": 3000}, false},
		{"modify, same gid", "identity.group.modify", group.Modify, presentGroup, map[string]any{"gid": 2000}, false, map[string]any{"exists": true, "gid": 2000}, false},
		{"modify, absent", "identity.group.modify", group.Modify, absentGroup, map[string]any{"gid": 3000}, false, nil, true},
		{"remove, present", "identity.group.remove", group.Remove, presentGroup, nil, true, map[string]any{"exists": false, "gid": 0}, false},
		{"remove, absent", "identity.group.remove", group.Remove, absentGroup, nil, false, map[string]any{"exists": false, "gid": 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			h := newHarness(t, tc.state)
			checked, checkErr := d.Check(context.Background(), h.rc, h.device, h.params("deploy", tc.extra))
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check changed the group database: %v", calls)
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
