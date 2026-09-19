package windows

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmsvc"
)

// This file is a whitebox test of runServiceOp's own decision logic
// (converged, refusal, the after-read, diff recording, inverse
// emission), reached by swapping statusFunc for a canned answer -- the
// role remoteexectest's fake systemctl plays for pkg/remotesvc's tests,
// adapted to a transport with no in-process fake worth building (see
// statusFunc's own doc comment in windows.go). The exported
// Start/Stop/Restart/Enable/Disable functions get their own
// registration/param-validation/plumbing tests in the sibling
// package windows_test files, exactly as pkg/winrmexec's package doc
// argues this split should be drawn: everything short of a live device
// is a real, unskipped test here; the live device belongs to the gated
// Release Gate.

type fakeRC struct {
	stats map[string]any
}

func newFakeRC() *fakeRC { return &fakeRC{stats: map[string]any{}} }
func (c *fakeRC) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "secret"}
}
func (c *fakeRC) SetStat(k string, v any) error  { c.stats[k] = v; return nil }
func (c *fakeRC) EmitFact(k string, v any) error { return c.SetStat(k, v) }

// failingRC fails SetStat for one specific key, so a test can force
// runServiceOp's own error-wrapping around rc.SetStat(statName, ...),
// sdk.RecordDiff and sdk.RecordInverse individually -- each of which
// calls SetStat under a different key -- without a fake that fails
// everything.
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

// winrmDevice declares and structurally implements WinRMCapable, mirroring
// internal/catalog/exec/winrm's own test double: inventorytest.Stub alone
// declares the capability but has no WinRMHost/WinRMPort accessors, and
// winrmSession needs the real structural assertion to succeed.
type winrmDevice struct {
	*inventorytest.Stub
}

func (d *winrmDevice) WinRMHost() string { return "192.0.2.1" }
func (d *winrmDevice) WinRMPort() int    { return 5985 }

func stubDevice() *winrmDevice {
	return &winrmDevice{Stub: &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM}}}
}

// withStatus swaps statusFunc for the duration of one test, restoring the
// real winrmsvc.Status afterward so no other test in this package
// observes the swap.
func withStatus(t *testing.T, fn func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error)) {
	t.Helper()
	original := statusFunc
	statusFunc = fn
	t.Cleanup(func() { statusFunc = original })
}

func TestRunServiceOp_ConvergedSkipsApply(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})

	applyCalled := false
	rc := newFakeRC()
	result, err := runServiceOp(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { applyCalled = true; return nil },
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runServiceOp: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true, want false: the service was already running")
	}
	if applyCalled {
		t.Error("apply was called on an already-converged service")
	}
	if rc.stats["name"] != "spooler" {
		t.Errorf(`stats["name"] = %v, want "spooler"`, rc.stats["name"])
	}
}

func TestRunServiceOp_NotConvergedCallsApplyAndRereads(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})

	applyCalled := false
	rc := newFakeRC()
	result, err := runServiceOp(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { applyCalled = true; return nil },
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runServiceOp: %v", err)
	}
	if !result.Changed {
		t.Error("Changed = false, want true: the service was stopped and apply should have run")
	}
	if !applyCalled {
		t.Error("apply was never called for a non-converged service")
	}
	if calls != 2 {
		t.Errorf("statusFunc was called %d times, want 2 (before, then after the change)", calls)
	}
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] = %v (%T), want a recorded diff", sdk.StatDiff, rc.stats[sdk.StatDiff], rc.stats[sdk.StatDiff])
	}
	before, _ := diff[sdk.DiffBefore].(map[string]any)
	after, _ := diff[sdk.DiffAfter].(map[string]any)
	if before["status"] != "Stopped" || after["status"] != "Running" {
		t.Errorf("diff before/after = %v / %v, want Stopped -> Running", before, after)
	}
}

func TestRunServiceOp_RefusesAServiceThatDoesNotExist(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "typo-svc", Exists: false}, nil
	})

	_, err := runServiceOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "typo-svc"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
	}, collection.ModeExecute)
	if err == nil {
		t.Fatal("expected a refusal for a service the SCM does not know")
	}
	if got := err.Error(); !strings.Contains(got, "typo-svc") || !strings.Contains(got, "does not know") {
		t.Errorf("error = %q, want it to name the service and say the SCM does not know it", got)
	}
}

func TestRunServiceOp_RefusesDisabledWhenTheOpSaysSo(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"}, nil
	})

	applyCalled := false
	_, err := runServiceOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:            "svc.windows.start",
		converged:       winrmsvc.State.Running,
		apply:           func(context.Context, winrmsvc.Session, string) error { applyCalled = true; return nil },
		refusesDisabled: true,
	}, collection.ModeExecute)
	if err == nil {
		t.Fatal("expected a refusal for a Disabled service")
	}
	if applyCalled {
		t.Error("apply was called even though checkServiceUsable should have refused first")
	}
	if got := err.Error(); !strings.Contains(got, "Disabled") {
		t.Errorf("error = %q, want it to name Disabled as the cause", got)
	}
}

func TestRunServiceOp_DoesNotRefuseDisabledWhenTheOpDoesNotAsk(t *testing.T) {
	// Stop must be able to act on (or converge against) a Disabled
	// service: it is already not running, and refusing would fail a task
	// whose goal is already met.
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"}, nil
	})

	result, err := runServiceOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.stop",
		converged: func(s winrmsvc.State) bool { return !s.Running() },
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runServiceOp: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true, want false: a Disabled, already-stopped service is already what stop wants")
	}
}

func TestRunServiceOp_RestartAlwaysActsEvenWhenRunning(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})

	applyCalled := false
	result, err := runServiceOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.restart",
		converged: nil,
		apply:     func(context.Context, winrmsvc.Session, string) error { applyCalled = true; return nil },
	}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("runServiceOp: %v", err)
	}
	if !result.Changed || !applyCalled {
		t.Error("a nil converged func must always act and report changed, even on a running service")
	}
}

func TestRunServiceOp_InverseOnlyEmittedWhenChanged(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})

	inverseCalled := false
	rc := newFakeRC()
	_, err := runServiceOp(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
		inverse: func(string, winrmsvc.State) (sdk.Inverse, bool) {
			inverseCalled = true
			return sdk.Inverse{}, true
		},
	}, collection.ModeExecute)
	_ = err
	if inverseCalled {
		t.Error("inverse must not be evaluated at all when the run converged without acting")
	}
	if rc.stats[sdk.StatInverse] != nil {
		t.Error("no inverse should have been recorded for a converged (no-op) run")
	}
}

// withApply swaps one of the startFunc/stopFunc/restartFunc/enableFunc/
// disableFunc seams for the duration of one test, restoring the real
// pkg/winrmsvc function afterward.
func withApply(t *testing.T, slot *func(context.Context, winrmsvc.Session, string) error, fn func(context.Context, winrmsvc.Session, string) error) {
	t.Helper()
	original := *slot
	*slot = fn
	t.Cleanup(func() { *slot = original })
}

// TestEnable_ManualToAutomaticEmitsNoInverse and
// TestDisable_ManualToDisabledEmitsNoInverse pin the asymmetry documented
// on each method's own Reversibility.Notes: this namespace's enable and
// disable only ever set Automatic or Disabled, so a service found Manual
// has no exact reverse through either sibling method, and emitting one
// anyway would over-correct a rollback into a state the run never found.
//
// Both drive the real, registered Enable/Disable functions (not a
// hand-copied stand-in for their inverse logic), with statusFunc and the
// relevant applyFunc swapped so nothing dials out -- proving what the
// registered method actually does.
func TestEnable_ManualToAutomaticEmitsNoInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Manual"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
	})
	withApply(t, &enableFunc, func(context.Context, winrmsvc.Session, string) error { return nil })

	rc := newFakeRC()
	result, err := Enable(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected the Manual -> Automatic transition to report changed")
	}
	if rc.stats[sdk.StatInverse] != nil {
		t.Error("a Manual -> Automatic transition must emit no inverse: svc.windows.disable would set Disabled, not restore Manual")
	}
}

func TestEnable_DisabledToAutomaticEmitsDisableInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
	})
	withApply(t, &enableFunc, func(context.Context, winrmsvc.Session, string) error { return nil })

	rc := newFakeRC()
	if _, err := Enable(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] = %v (%T), want a recorded inverse for the Disabled -> Automatic transition", sdk.StatInverse, rc.stats[sdk.StatInverse], rc.stats[sdk.StatInverse])
	}
	if inv["fqcn"] != "svc.windows.disable" {
		t.Errorf("inverse fqcn = %v, want svc.windows.disable", inv["fqcn"])
	}
}

func TestDisable_ManualToDisabledEmitsNoInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Manual"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"}, nil
	})
	withApply(t, &disableFunc, func(context.Context, winrmsvc.Session, string) error { return nil })

	rc := newFakeRC()
	result, err := Disable(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"})
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected the Manual -> Disabled transition to report changed")
	}
	if rc.stats[sdk.StatInverse] != nil {
		t.Error("a Manual -> Disabled transition must emit no inverse: svc.windows.enable would set Automatic, not restore Manual")
	}
}

func TestDisable_AutomaticToDisabledEmitsEnableInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"}, nil
	})
	withApply(t, &disableFunc, func(context.Context, winrmsvc.Session, string) error { return nil })

	rc := newFakeRC()
	if _, err := Disable(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] = %v (%T), want a recorded inverse for the Automatic -> Disabled transition", sdk.StatInverse, rc.stats[sdk.StatInverse], rc.stats[sdk.StatInverse])
	}
	if inv["fqcn"] != "svc.windows.enable" {
		t.Errorf("inverse fqcn = %v, want svc.windows.enable", inv["fqcn"])
	}
}

// TestStart_StoppedEmitsStopInverse and TestStop_RunningEmitsStartInverse
// drive the real, registered Start/Stop functions with statusFunc and
// the relevant applyFunc swapped, covering their own inverse closures
// the same way the enable/disable tests above cover theirs.
func TestStart_StoppedEmitsStopInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})
	withApply(t, &startFunc, func(context.Context, winrmsvc.Session, string) error { return nil })

	rc := newFakeRC()
	result, err := Start(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected the stopped -> running transition to report changed")
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] = %v (%T), want a recorded inverse", sdk.StatInverse, rc.stats[sdk.StatInverse], rc.stats[sdk.StatInverse])
	}
	if inv["fqcn"] != "svc.windows.stop" {
		t.Errorf("inverse fqcn = %v, want svc.windows.stop", inv["fqcn"])
	}
}

func TestStop_RunningEmitsStartInverse(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
		}
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
	})
	withApply(t, &stopFunc, func(context.Context, winrmsvc.Session, string) error { return nil })

	rc := newFakeRC()
	result, err := Stop(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected the running -> stopped transition to report changed")
	}
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] = %v (%T), want a recorded inverse", sdk.StatInverse, rc.stats[sdk.StatInverse], rc.stats[sdk.StatInverse])
	}
	if inv["fqcn"] != "svc.windows.start" {
		t.Errorf("inverse fqcn = %v, want svc.windows.start", inv["fqcn"])
	}
}

// TestWinrmSession_RefusesANilDevice covers the guard a generic dispatch
// path (svc.start resolving through the registry) could plausibly hit
// with no device at all.
func TestWinrmSession_RefusesANilDevice(t *testing.T) {
	_, err := winrmSession(newFakeRC(), nil, "svc.windows.start")
	if err == nil {
		t.Fatal("expected a refusal for a nil device")
	}
	if !strings.Contains(err.Error(), "needs a target device") {
		t.Errorf("error = %v, want it to say a device is needed", err)
	}
}

// The four tests below cover runServiceOp's own error-wrapping around
// each downstream call that can fail: apply itself, the after-change
// re-read, and the two stats it writes (the plain "name" stat, and each
// of sdk.RecordDiff/sdk.RecordInverse's own SetStat call). Each is
// forced independently so a failure in one path cannot masquerade as
// coverage of another.

func TestRunServiceOp_ApplyFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
	})
	_, err := runServiceOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return fmt.Errorf("boom") },
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "svc.windows.start") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to wrap the apply failure with the fqcn", err)
	}
}

func TestRunServiceOp_AfterReadFailureIsWrapped(t *testing.T) {
	calls := 0
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		calls++
		if calls == 1 {
			return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
		}
		return winrmsvc.State{}, fmt.Errorf("boom")
	})
	_, err := runServiceOp(context.Background(), newFakeRC(), stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "svc.windows.start") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to wrap the after-change read failure with the fqcn", err)
	}
}

func TestRunServiceOp_NameStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: statName}
	_, err := runServiceOp(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "svc.windows.start") {
		t.Errorf("error = %v, want it to wrap the %q stat failure with the fqcn", err, statName)
	}
}

func TestRunServiceOp_DiffStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: sdk.StatDiff}
	_, err := runServiceOp(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "svc.windows.start") {
		t.Errorf("error = %v, want it to wrap the %q stat failure with the fqcn", err, sdk.StatDiff)
	}
}

func TestRunServiceOp_InverseStatFailureIsWrapped(t *testing.T) {
	withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) {
		return winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Automatic"}, nil
	})
	rc := &failingRC{fakeRC: newFakeRC(), failOnKey: sdk.StatInverse}
	_, err := runServiceOp(context.Background(), rc, stubDevice(), map[string]any{"name": "spooler"}, serviceOp{
		fqcn:      "svc.windows.start",
		converged: winrmsvc.State.Running,
		apply:     func(context.Context, winrmsvc.Session, string) error { return nil },
		inverse: func(name string, _ winrmsvc.State) (sdk.Inverse, bool) {
			return sdk.Inverse{FQCN: "svc.windows.stop", Params: map[string]any{paramName: name}}, true
		},
	}, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "svc.windows.start") {
		t.Errorf("error = %v, want it to wrap the %q stat failure with the fqcn", err, sdk.StatInverse)
	}
}

// TestServiceChecks covers each svc.windows method's check through the
// same seams its real run is tested through: the check never calls the
// method's apply (a call fails the test), predicts a change exactly when
// the state it reads is not the one its real run converges to (restart
// always), predicts the one field the change sets, records no undo, and
// refuses what the real run refuses (a service the SCM does not know, a
// start of a Disabled one).
func TestServiceChecks(t *testing.T) {
	stopped := winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Manual"}
	running := winrmsvc.State{Name: "spooler", Exists: true, Status: "Running", StartType: "Automatic"}
	disabled := winrmsvc.State{Name: "spooler", Exists: true, Status: "Stopped", StartType: "Disabled"}
	for _, tc := range []struct {
		name        string
		check       func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error)
		apply       *func(context.Context, winrmsvc.Session, string) error
		state       winrmsvc.State
		wantChanged bool
		wantField   string
		wantValue   any
		refused     string
	}{
		{"start, stopped", CheckStart, &startFunc, stopped, true, "status", "Running", ""},
		{"start, running", CheckStart, &startFunc, running, false, "status", "Running", ""},
		{"start, disabled", CheckStart, &startFunc, disabled, false, "", nil, "Disabled"},
		{"stop, running", CheckStop, &stopFunc, running, true, "status", "Stopped", ""},
		{"stop, stopped", CheckStop, &stopFunc, stopped, false, "status", "Stopped", ""},
		{"restart, running", CheckRestart, &restartFunc, running, true, "status", "Running", ""},
		{"enable, manual", CheckEnable, &enableFunc, stopped, true, "start_type", "Automatic", ""},
		{"enable, automatic", CheckEnable, &enableFunc, running, false, "start_type", "Automatic", ""},
		{"disable, automatic", CheckDisable, &disableFunc, running, true, "start_type", "Disabled", ""},
		{"start, unknown service", CheckStart, &startFunc, winrmsvc.State{Name: "typo"}, false, "", nil, "does not know a service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withStatus(t, func(context.Context, winrmsvc.Session, string) (winrmsvc.State, error) { return tc.state, nil })
			withApply(t, tc.apply, func(context.Context, winrmsvc.Session, string) error {
				t.Error("the check applied the change")
				return nil
			})
			rc := newFakeRC()
			result, err := tc.check(context.Background(), rc, stubDevice(), map[string]any{"name": tc.state.Name})
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("check = %v, want it refused with %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if result.Changed != tc.wantChanged {
				t.Errorf("predicted changed %v, want %v", result.Changed, tc.wantChanged)
			}
			if rc.stats[sdk.StatInverse] != nil {
				t.Error("the check recorded an undo instruction")
			}
			diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
			after, _ := diff["after"].(map[string]any)
			if after[tc.wantField] != tc.wantValue {
				t.Errorf("predicted %s = %v, want %v", tc.wantField, after[tc.wantField], tc.wantValue)
			}
		})
	}
}
