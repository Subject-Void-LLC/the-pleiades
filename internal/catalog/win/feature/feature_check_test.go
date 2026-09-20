// Package feature: tests of the win.feature checks.
package feature

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmdism"
)

// featureDiff returns a recorded diff's before and after halves.
func featureDiff(t *testing.T, rc *fakeRC) (before, after map[string]any) {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	before, _ = diff[sdk.DiffBefore].(map[string]any)
	after, _ = diff[sdk.DiffAfter].(map[string]any)
	return before, after
}

// TestFeatureChecks runs each method's registered check against a feature
// found in each state DISM reports, with both verbs' seams replaced by
// ones that fail the test, so a check that sends anything is caught. The
// real run from the same start is the control: the check predicts the
// change the real run makes, and every key its after half states is one
// the real run leaves; the state it leaves out is the one a restart
// decides.
func TestFeatureChecks(t *testing.T) {
	for _, tc := range []struct {
		fqcn    string
		found   string
		changes bool
		leaves  string // the state a real run reads back
	}{
		{"win.feature.install", "Disabled", true, "Enabled"},
		{"win.feature.install", "Enable Pending", true, "Enable Pending"},
		{"win.feature.install", "Enabled", false, "Enabled"},
		{"win.feature.remove", "Enabled", true, "Disabled"},
		{"win.feature.remove", "Partially Enabled", true, "Disable Pending"},
		{"win.feature.remove", "Disabled", false, "Disabled"},
	} {
		t.Run(tc.fqcn+"/"+tc.found, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			params := map[string]any{paramName: "IIS-WebServerRole"}

			// The check: one read, nothing sent.
			reads := 0
			withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
				reads++
				return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: tc.found}, nil
			})
			sent := func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
				t.Errorf("%s's check sent a change", tc.fqcn)
				return winrmdism.ChangeResult{}, nil
			}
			withApply(t, &enableFunc, sent)
			withApply(t, &disableFunc, sent)
			rc := newFakeRC()
			result, err := d.Check(context.Background(), rc, stubDevice(), params)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if result.Changed != tc.changes || reads != 1 {
				t.Errorf("check: Changed = %v after %d read(s), want %v after 1", result.Changed, reads, tc.changes)
			}
			if _, ok := rc.stats[sdk.StatInverse]; ok {
				t.Error("the check recorded an undo instruction for a change it never made")
			}
			if _, ok := rc.stats[statRebootRequired]; ok {
				t.Error("the check claimed to know whether a restart is needed, which only running the change says")
			}
			before, predicted := featureDiff(t, rc)
			if before["state"] != tc.found {
				t.Errorf("the check's before state = %v, want %q as read", before["state"], tc.found)
			}
			if _, stated := predicted["state"]; stated == tc.changes {
				t.Errorf("the check's after half states the state = %v, want %v", stated, !tc.changes)
			}

			// The control: the real run from the same start.
			reads = 0
			withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
				reads++
				state := tc.found
				if reads > 1 {
					state = tc.leaves
				}
				return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: state}, nil
			})
			apply := func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
				return winrmdism.ChangeResult{}, nil
			}
			withApply(t, &enableFunc, apply)
			withApply(t, &disableFunc, apply)
			real := newFakeRC()
			ran, err := d.Invoke(context.Background(), real, stubDevice(), params)
			if err != nil {
				t.Fatalf("real run: %v", err)
			}
			if ran.Changed != result.Changed {
				t.Errorf("the check predicted Changed = %v, the real run reported %v", result.Changed, ran.Changed)
			}
			_, actual := featureDiff(t, real)
			for key, want := range predicted {
				if actual[key] != want {
					t.Errorf("predicted %s = %v, the real run left %v", key, want, actual[key])
				}
			}
		})
	}
}

// TestFeatureChecks_AnUnknownFeatureIsRefused covers the refusal a check
// shares with the real run: a name DISM does not recognize is an error,
// not a prediction.
func TestFeatureChecks_AnUnknownFeatureIsRefused(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRol", Exists: false}, nil
	})
	for _, check := range []collection.Method{CheckInstall, CheckRemove} {
		_, err := check(context.Background(), newFakeRC(), stubDevice(), map[string]any{paramName: "IIS-WebServerRol"})
		if err == nil || !strings.Contains(err.Error(), "does not recognize a feature") {
			t.Errorf("a check of an unknown feature = %v, want the real run's refusal", err)
		}
	}
}
