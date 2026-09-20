package feature

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmdism"
)

// See internal/catalog/svc/windows's own windows_internal_test.go doc
// comment: this file is the identical whitebox seam-swapping strategy,
// adapted to winrmdism.

type fakeRC struct {
	stats map[string]any
}

func newFakeRC() *fakeRC { return &fakeRC{stats: map[string]any{}} }
func (c *fakeRC) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "secret"}
}
func (c *fakeRC) SetStat(k string, v any) error  { c.stats[k] = v; return nil }
func (c *fakeRC) EmitFact(k string, v any) error { return c.SetStat(k, v) }

type failingRC struct {
	*fakeRC
	failOnKey string
}

func (c *failingRC) SetStat(k string, v any) error {
	if k == c.failOnKey {
		return fmt.Errorf("simulated failure writing stat %q", k)
	}
	return c.fakeRC.SetStat(k, v)
}

// winrmDevice declares and structurally implements WinRMCapable and
// WindowsFeatureCapable, mirroring internal/catalog/svc/windows's own
// test double.
type winrmDevice struct {
	*inventorytest.Stub
}

func (d *winrmDevice) WinRMHost() string   { return "192.0.2.1" }
func (d *winrmDevice) WinRMPort() int      { return 5985 }
func (d *winrmDevice) DISMLogPath() string { return `C:\Windows\Logs\DISM\dism.log` }

func stubDevice() *winrmDevice {
	return &winrmDevice{Stub: &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsFeature}}}
}

func withStatus(t *testing.T, fn func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error)) {
	t.Helper()
	original := statusFunc
	statusFunc = fn
	t.Cleanup(func() { statusFunc = original })
}

func withApply(t *testing.T, slot *func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error), fn func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error)) {
	t.Helper()
	original := *slot
	*slot = fn
	t.Cleanup(func() { *slot = original })
}

func TestRunFeatureOp_ConvergedSkipsApply(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})

	applyCalled := false
	rc := newFakeRC()
	result, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			applyCalled = true
			return winrmdism.ChangeResult{}, nil
		},
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runFeatureOp: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true, want false: the feature was already enabled")
	}
	if applyCalled {
		t.Error("apply was called on an already-converged feature")
	}
	if rc.stats["name"] != "IIS-WebServerRole" {
		t.Errorf(`stats["name"] = %v, want "IIS-WebServerRole"`, rc.stats["name"])
	}
	if rc.stats[statRebootRequired] != false {
		t.Errorf("stats[%q] = %v, want false on a converged run", statRebootRequired, rc.stats[statRebootRequired])
	}
}

func TestRunFeatureOp_NotConvergedCallsApplyAndRereads(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		calls++
		if calls == 1 {
			return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
		}
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})

	applyCalled := false
	rc := newFakeRC()
	result, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			applyCalled = true
			return winrmdism.ChangeResult{RebootRequired: true}, nil
		},
		inverse: func(name string) (sdk.Inverse, bool) {
			return sdk.Inverse{FQCN: "win.feature.remove", Params: map[string]any{paramName: name}}, true
		},
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runFeatureOp: %v", err)
	}
	if !result.Changed || !applyCalled {
		t.Error("expected apply to run and the result to report changed for a non-converged feature")
	}
	if calls != 2 {
		t.Errorf("statusFunc was called %d times, want 2", calls)
	}
	if rc.stats[statRebootRequired] != true {
		t.Errorf("stats[%q] = %v, want true: apply reported RebootRequired", statRebootRequired, rc.stats[statRebootRequired])
	}
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] missing a recorded diff", sdk.StatDiff)
	}
	before, _ := diff[sdk.DiffBefore].(map[string]any)
	after, _ := diff[sdk.DiffAfter].(map[string]any)
	if before["state"] != "Disabled" || after["state"] != "Enabled" {
		t.Errorf("diff before/after = %v / %v, want Disabled -> Enabled", before, after)
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok || inv["fqcn"] != "win.feature.remove" {
		t.Errorf("stats[%q] = %v, want an inverse naming win.feature.remove", sdk.StatInverse, rc.stats[sdk.StatInverse])
	}
}

func TestRunFeatureOp_RefusesAFeatureThatDoesNotExist(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "Not-A-Real-Feature", Exists: false}, nil
	})

	_, err := runFeatureOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "Not-A-Real-Feature"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
	}, collection.ModeExecute)
	if err == nil {
		t.Fatal("expected a refusal for a feature DISM does not recognize")
	}
	if got := err.Error(); !strings.Contains(got, "Not-A-Real-Feature") || !strings.Contains(got, "does not recognize") {
		t.Errorf("error = %q, want it to name the feature and say DISM does not recognize it", got)
	}
}

func TestRunFeatureOp_InverseOnlyEmittedWhenChanged(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})

	inverseCalled := false
	rc := newFakeRC()
	_, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
		inverse: func(name string) (sdk.Inverse, bool) {
			inverseCalled = true
			return sdk.Inverse{}, true
		},
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runFeatureOp: %v", err)
	}
	if inverseCalled {
		t.Error("inverse must not be evaluated at all when the run converged without acting")
	}
	if rc.stats[sdk.StatInverse] != nil {
		t.Error("no inverse should have been recorded for a converged (no-op) run")
	}
}

func TestRunFeatureOp_ApplyFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
	})
	_, err := runFeatureOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, fmt.Errorf("boom")
		},
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "win.feature.install") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to wrap the apply failure with the fqcn", err)
	}
}

func TestRunFeatureOp_AfterReadFailureIsWrapped(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		calls++
		if calls == 1 {
			return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
		}
		return winrmdism.FeatureState{}, fmt.Errorf("boom")
	})
	_, err := runFeatureOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to wrap the after-change read failure", err)
	}
}

func TestRunFeatureOp_NameStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: statName}
	_, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
	}, collection.ModeExecute)
	if err == nil {
		t.Errorf("expected the %q stat failure to be wrapped", statName)
	}
}

func TestRunFeatureOp_RebootRequiredStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: statRebootRequired}
	_, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
	}, collection.ModeExecute)
	if err == nil {
		t.Errorf("expected the %q stat failure to be wrapped", statRebootRequired)
	}
}

func TestRunFeatureOp_DiffStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: sdk.StatDiff}
	_, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
	}, collection.ModeExecute)
	if err == nil {
		t.Errorf("expected the %q stat failure to be wrapped", sdk.StatDiff)
	}
}

func TestRunFeatureOp_InverseStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: sdk.StatInverse}
	_, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
		fqcn:      "win.feature.install",
		converged: winrmdism.FeatureState.Enabled,
		apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
			return winrmdism.ChangeResult{}, nil
		},
		inverse: func(name string) (sdk.Inverse, bool) {
			return sdk.Inverse{FQCN: "win.feature.remove", Params: map[string]any{paramName: name}}, true
		},
	}, collection.ModeExecute)
	if err == nil {
		t.Errorf("expected the %q stat failure to be wrapped", sdk.StatInverse)
	}
}

func TestWinrmSession_RefusesANilDevice(t *testing.T) {
	_, _, err := winrmSession(newFakeRC(), nil, "win.feature.install")
	if err == nil || !strings.Contains(err.Error(), "needs a target device") {
		t.Errorf("error = %v, want a refusal naming a needed device", err)
	}
}

func TestWinrmSession_RefusesADeviceWithNoWinRMAccessors(t *testing.T) {
	stub := &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWindowsFeature}}
	_, _, err := winrmSession(newFakeRC(), stub, "win.feature.install")
	if err == nil || !strings.Contains(err.Error(), "does not implement its accessors") {
		t.Errorf("error = %v, want a refusal naming the missing WinRM accessors", err)
	}
}

// TestWinrmSession_RefusesADeviceWithNoDISMAccessor covers a device that
// implements WinRMCapable but not WindowsFeatureCapable's DISMLogPath --
// a case internal/catalog/svc/windows has no equivalent of, since that
// package only ever needs the one capability.
type winrmOnlyDevice struct {
	*inventorytest.Stub
}

func (d *winrmOnlyDevice) WinRMHost() string { return "192.0.2.1" }
func (d *winrmOnlyDevice) WinRMPort() int    { return 5985 }

func TestWinrmSession_RefusesADeviceWithNoDISMAccessor(t *testing.T) {
	dev := &winrmOnlyDevice{Stub: &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsFeature}}}
	_, _, err := winrmSession(newFakeRC(), dev, "win.feature.install")
	if err == nil || !strings.Contains(err.Error(), "does not implement its accessors") {
		t.Errorf("error = %v, want a refusal naming the missing DISM accessor", err)
	}
}

// TestInstall_DisabledEmitsRemoveInverse and
// TestRemove_EnabledEmitsInstallInverse drive the real, registered
// Install/Remove functions with statusFunc and the relevant applyFunc
// swapped, covering their own inverse closures the same way
// windows_internal_test.go's enable/disable tests cover theirs.
func TestInstall_DisabledEmitsRemoveInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		calls++
		if calls == 1 {
			return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
		}
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
	})
	withApply(t, &enableFunc, func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
		return winrmdism.ChangeResult{RebootRequired: true}, nil
	})

	rc := newFakeRC()
	result, err := Install(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected the disabled -> enabled transition to report changed")
	}
	if rc.stats[statRebootRequired] != true {
		t.Errorf("stats[%q] = %v, want true", statRebootRequired, rc.stats[statRebootRequired])
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok || inv["fqcn"] != "win.feature.remove" {
		t.Errorf("stats[%q] = %v, want an inverse naming win.feature.remove", sdk.StatInverse, rc.stats[sdk.StatInverse])
	}
}

func TestRemove_EnabledEmitsInstallInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		calls++
		if calls == 1 {
			return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Enabled"}, nil
		}
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
	})
	withApply(t, &disableFunc, func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
		return winrmdism.ChangeResult{}, nil
	})

	rc := newFakeRC()
	result, err := Remove(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected the enabled -> disabled transition to report changed")
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok || inv["fqcn"] != "win.feature.install" {
		t.Errorf("stats[%q] = %v, want an inverse naming win.feature.install", sdk.StatInverse, rc.stats[sdk.StatInverse])
	}
}

// TestRunFeatureOp_CheckStatFailuresAreWrapped is the check-mode half of
// the stat-failure tests above: a check that cannot record the feature's
// name or its diff fails naming the method, and never applies the change,
// since a call to apply fails the test.
func TestRunFeatureOp_CheckStatFailuresAreWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmdism.Session, string, string) (winrmdism.FeatureState, error) {
		return winrmdism.FeatureState{Name: "IIS-WebServerRole", Exists: true, State: "Disabled"}, nil
	})
	for _, key := range []string{statName, sdk.StatDiff} {
		t.Run(key, func(t *testing.T) {
			rc := &failingRC{fakeRC: newFakeRC(), failOnKey: key}
			_, err := runFeatureOp(context.Background(), rc, stubDevice(), map[string]any{"name": "IIS-WebServerRole"}, featureOp{
				fqcn:      "win.feature.install",
				converged: winrmdism.FeatureState.Enabled,
				apply: func(context.Context, winrmdism.Session, string, string) (winrmdism.ChangeResult, error) {
					t.Error("a check applied the change")
					return winrmdism.ChangeResult{}, nil
				},
			}, collection.ModeCheck)
			if err == nil || !strings.HasPrefix(err.Error(), "win.feature.install: ") {
				t.Errorf("error = %v, want the %q stat failure wrapped with the fqcn", err, key)
			}
		})
	}
}
