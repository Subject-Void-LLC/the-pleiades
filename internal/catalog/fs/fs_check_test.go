// Package fs_test: tests of the fs.mount and fs.unmount checks.
package fs_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// checkDiff returns a recorded diff's before and after halves.
func checkDiff(t *testing.T, rc *ctxStub) (before, after map[string]any) {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	before, _ = diff[sdk.DiffBefore].(map[string]any)
	after, _ = diff[sdk.DiffAfter].(map[string]any)
	return before, after
}

// TestChecks_ReadAndPredict runs each method's registered check on the
// fake mount harness from each state it meets, then the real run from the
// same state as the control. The check sends no mount or umount (the
// fakes record every call), leaves the real fstab file byte for byte as
// it was, decides the change the real run decides, records no undo
// instruction, predicts the fstab half (persisted) the real run leaves,
// and predicts the mount half the method's own contract promises. The
// fake findmnt does not change when the fake mount runs, so what a real
// mount leaves is not compared here; mounting needs root, which this
// harness does not have.
func TestChecks_ReadAndPredict(t *testing.T) {
	const mp = "/mnt/data"
	entry := "/dev/sdb1\t" + mp + "\text4\trw,relatime\t0\t0\n"
	for _, tc := range []struct {
		name      string
		fqcn      string
		state     mountState
		fstab     string
		extra     map[string]any
		changes   bool
		predicted map[string]any // the after half's keys the check must state
		refused   string
	}{
		{"mount a new path and persist it", "fs.mount", notMounted, "", nil, true,
			map[string]any{"mounted": true, "source": "/dev/sdb1", "fstype": "ext4", "persisted": true}, ""},
		{"mount without persisting", "fs.mount", notMounted, "", map[string]any{"persist": false}, true,
			map[string]any{"mounted": true, "persisted": false}, ""},
		{"persist a path already mounted", "fs.mount", mounted, "", nil, true,
			map[string]any{"mounted": true, "options": "rw,relatime", "persisted": true}, ""},
		{"already mounted and persisted", "fs.mount", mounted, entry, nil, false,
			map[string]any{"mounted": true, "persisted": true}, ""},
		{"mounted from somewhere else", "fs.mount", mounted, "", map[string]any{"src": "/dev/sdc1"}, false, nil,
			"already mounted from /dev/sdb1"},
		{"unmount and unpersist", "fs.unmount", mounted, entry, nil, true,
			map[string]any{"mounted": false, "source": "", "persisted": false}, ""},
		{"unpersist a path not mounted", "fs.unmount", notMounted, entry, nil, true,
			map[string]any{"mounted": false, "persisted": false}, ""},
		{"unmount but keep the entry", "fs.unmount", mounted, entry, map[string]any{"persist": false}, true,
			map[string]any{"mounted": false, "persisted": true}, ""},
		{"nothing to undo", "fs.unmount", notMounted, "", nil, false,
			map[string]any{"mounted": false, "persisted": false}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			params := map[string]any{}
			if tc.fqcn == "fs.mount" {
				params["src"], params["fstype"] = "/dev/sdb1", "ext4"
			}
			for k, v := range tc.extra {
				params[k] = v
			}

			h := newHarness(t, tc.state, tc.fstab)
			result, err := d.Check(context.Background(), h.rc, h.device, h.params(mp, params))
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check ran %v", calls)
			}
			if got, readErr := os.ReadFile(h.fstab); readErr != nil || string(got) != tc.fstab {
				t.Errorf("the check left fstab as %q (%v), want it untouched as %q", got, readErr, tc.fstab)
			}
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("check = %v, want the real run's refusal %q", err, tc.refused)
				}
				real := newHarness(t, tc.state, tc.fstab)
				if _, err := d.Invoke(context.Background(), real.rc, real.device, real.params(mp, params)); err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Errorf("the real run = %v, want the same refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if result.Changed != tc.changes {
				t.Errorf("the check predicted Changed = %v, want %v", result.Changed, tc.changes)
			}
			if _, ok := h.rc.stats[sdk.StatInverse]; ok {
				t.Error("the check recorded an undo instruction for a change it never made")
			}
			_, predicted := checkDiff(t, h.rc)
			for key, want := range tc.predicted {
				if got, stated := predicted[key]; !stated || got != want {
					t.Errorf("predicted %s = %v (stated %v), want %v", key, got, stated, want)
				}
			}
			if _, stated := predicted["options"]; stated && tc.changes && !tc.state.mounted && tc.fqcn == "fs.mount" {
				t.Error("the check predicted a new mount's options, which the kernel decides")
			}

			real := newHarness(t, tc.state, tc.fstab)
			ran, err := d.Invoke(context.Background(), real.rc, real.device, real.params(mp, params))
			if err != nil {
				t.Fatalf("the real run: %v", err)
			}
			if ran.Changed != result.Changed {
				t.Errorf("the check predicted Changed = %v, the real run reported %v", result.Changed, ran.Changed)
			}
			if _, actual := checkDiff(t, real.rc); actual["persisted"] != predicted["persisted"] {
				t.Errorf("predicted persisted = %v, the real run left %v", predicted["persisted"], actual["persisted"])
			}
		})
	}
}
