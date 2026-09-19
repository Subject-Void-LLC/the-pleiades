// Package dnf_test: tests of the pkg.dnf.* checks.
package dnf_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestDnfChecks covers each method's check on the fake-package-manager
// harness, with the real run from the same state as its control: the only
// dnf a check runs is check-update, the read an upgrade's real run makes
// too; its change decision is the real run's; its prediction names the
// version only when the task does (or when the package would be removed),
// since which build dnf installs is dnf's choice; and it records no undo.
//
// The harness's dnf changes nothing rpm then reports, so as for pkg.apt.*,
// comparing a prediction with a real dnf's result is a container gate's
// job; here it is checked against what the task asks for.
func TestDnfChecks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fqcn        string
		run         collection.Method
		state       pkgState
		extra       map[string]any
		wantChanged bool
		wantAfter   map[string]any
	}{
		{"install, absent", "pkg.dnf.install", dnf.Install, absent, nil, true, map[string]any{"installed": true}},
		{"install, absent, a version named", "pkg.dnf.install", dnf.Install, absent, map[string]any{"version": "2.0-1"}, true, map[string]any{"installed": true, "version": "2.0-1"}},
		{"install, present", "pkg.dnf.install", dnf.Install, installedCurrent, nil, false, map[string]any{"installed": true, "version": "1.0-1"}},
		{"remove, present", "pkg.dnf.remove", dnf.Remove, installedCurrent, nil, true, map[string]any{"installed": false, "version": ""}},
		{"remove, absent", "pkg.dnf.remove", dnf.Remove, absent, nil, false, map[string]any{"installed": false, "version": ""}},
		{"upgrade, behind", "pkg.dnf.upgrade", dnf.Upgrade, installedOld, nil, true, map[string]any{"installed": true}},
		{"upgrade, current", "pkg.dnf.upgrade", dnf.Upgrade, installedCurrent, nil, false, map[string]any{"installed": true, "version": "1.0-1"}},
		{"upgrade, absent", "pkg.dnf.upgrade", dnf.Upgrade, absent, nil, true, map[string]any{"installed": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			h := newHarness(t, tc.state)
			checked, err := d.Check(context.Background(), h.rc, h.device, h.params("curl", tc.extra))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			for _, call := range h.invocations(t) {
				if !strings.HasPrefix(call, "check-update") {
					t.Errorf("the check ran dnf %s", call)
				}
			}
			if checked.Changed != tc.wantChanged {
				t.Errorf("the check predicts changed %v, want %v", checked.Changed, tc.wantChanged)
			}
			if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}
			diff, _ := h.rc.stats[sdk.StatDiff].(map[string]any)
			if after, _ := diff["after"].(map[string]any); !reflect.DeepEqual(after, tc.wantAfter) {
				t.Errorf("predicted %v, want %v", after, tc.wantAfter)
			}

			run := newHarness(t, tc.state)
			ran, err := tc.run(context.Background(), run.rc, run.device, run.params("curl", tc.extra))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if ran.Changed != checked.Changed {
				t.Errorf("the run changed %v, the check predicted %v", ran.Changed, checked.Changed)
			}
		})
	}
}
