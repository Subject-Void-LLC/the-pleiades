// Package apt_test: tests of the pkg.apt.* checks.
package apt_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestAptChecks covers each method's check on the fake-package-manager
// harness, with the real run from the same state as its control: a check
// sends no apt-get at all (only dpkg-query and apt-cache, which read);
// its change decision is the real run's; the version it predicts is the
// one named, or APT's candidate; it records no undo; and a package APT has
// no candidate for is refused rather than predicted as installable.
//
// The harness's apt-get changes nothing dpkg-query then reports, so the
// real run's after-state here is not what a real apt-get would leave, and
// the prediction is checked against what APT says it would install
// instead. Comparing it with a real apt-get's result is a container
// gate's job, as it is for these methods' real runs.
func TestAptChecks(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fqcn        string
		run         collection.Method
		state       pkgState
		extra       map[string]any
		wantChanged bool
		wantAfter   map[string]any
		wantRefused string
	}{
		{"install, absent", "pkg.apt.install", apt.Install, pkgState{candidate: "2.0"}, nil, true, map[string]any{"installed": true, "version": "2.0"}, ""},
		{"install, absent, a version named", "pkg.apt.install", apt.Install, pkgState{candidate: "2.0"}, map[string]any{"version": "1.5"}, true, map[string]any{"installed": true, "version": "1.5"}, ""},
		{"install, present", "pkg.apt.install", apt.Install, installedCurrent, nil, false, map[string]any{"installed": true, "version": "1.0"}, ""},
		{"install, present at another version", "pkg.apt.install", apt.Install, installedCurrent, map[string]any{"version": "0.9"}, true, map[string]any{"installed": true, "version": "0.9"}, ""},
		{"install, nothing to install", "pkg.apt.install", apt.Install, absent, nil, false, nil, "no candidate"},
		{"remove, present", "pkg.apt.remove", apt.Remove, installedCurrent, nil, true, map[string]any{"installed": false, "version": ""}, ""},
		{"remove, absent", "pkg.apt.remove", apt.Remove, absent, nil, false, map[string]any{"installed": false, "version": ""}, ""},
		{"upgrade, behind", "pkg.apt.upgrade", apt.Upgrade, installedOld, nil, true, map[string]any{"installed": true, "version": "1.1"}, ""},
		{"upgrade, current", "pkg.apt.upgrade", apt.Upgrade, installedCurrent, nil, false, map[string]any{"installed": true, "version": "1.0"}, ""},
		{"upgrade, absent", "pkg.apt.upgrade", apt.Upgrade, pkgState{candidate: "3.0"}, nil, true, map[string]any{"installed": true, "version": "3.0"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			h := newHarness(t, tc.state)
			checked, err := d.Check(context.Background(), h.rc, h.device, h.params("curl", tc.extra))
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check ran apt-get: %v", calls)
			}
			if tc.wantRefused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantRefused) {
					t.Fatalf("check = %v, want it refused with %q", err, tc.wantRefused)
				}
				return
			}
			if err != nil {
				t.Fatalf("check: %v", err)
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

// TestAptChecks_FailWhenTheyCannotReadOrRecord covers
// a check that cannot finish its reads or record its answer: it fails
// naming why, as the real run does, rather than predicting from a value it
// never read or reporting a decision with no diff behind it, and it sends
// nothing that changes the device either way.
func TestAptChecks_FailWhenTheyCannotReadOrRecord(t *testing.T) {
	candidateFails := absent
	candidateFails.candidateExit = 1
	currentCandidateFails := installedCurrent
	currentCandidateFails.candidateExit = 1
	for _, tc := range []struct {
		name    string
		fqcn    string
		state   pkgState
		failKey string
		extra   map[string]any
		want    string
	}{
		{"install, the candidate cannot be read", "pkg.apt.install", candidateFails, "", nil, "apt-cache policy curl exited 1"},
		{"upgrade, absent, the candidate cannot be read", "pkg.apt.upgrade", candidateFails, "", nil, "apt-cache policy curl exited 1"},
		{"upgrade, present, the candidate cannot be read", "pkg.apt.upgrade", currentCandidateFails, "", nil, "apt-cache policy curl exited 1"},
		{"install, the name cannot be recorded", "pkg.apt.install", pkgState{candidate: "2.0"}, "name", nil, `injected failure recording "name"`},
		{"remove, the name cannot be recorded", "pkg.apt.remove", installedCurrent, "name", nil, `injected failure recording "name"`},
		{"upgrade, the name cannot be recorded", "pkg.apt.upgrade", installedOld, "name", nil, `injected failure recording "name"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := collection.Lookup(tc.fqcn)
			h := newHarness(t, tc.state)
			h.rc.failOnKey = tc.failKey
			_, err := d.Check(context.Background(), h.rc, h.device, h.params("curl", tc.extra))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("check error = %v, want one containing %q", err, tc.want)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check sent %v", calls)
			}
		})
	}
}
