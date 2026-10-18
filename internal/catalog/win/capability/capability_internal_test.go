// Tests for win.capability.install and remove with pkg/winrmdism's calls
// replaced by recording stand-ins (statusFunc, addFunc, removeFunc). The
// calls themselves, and the SYSTEM task they run, are pkg/winrmdism's
// tests; the real host is the lab's win-check.
package capability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmdism"
)

const appCompat = "ServerCore.AppCompatibility~~~~0.0.1.0"

type fakeRC struct {
	stats   map[string]any
	failKey string
}

func newRC() *fakeRC { return &fakeRC{stats: map[string]any{}} }

func (c *fakeRC) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "secret"}
}
func (c *fakeRC) SetStat(k string, v any) error {
	if k == c.failKey {
		return fmt.Errorf("simulated failure writing %s", k)
	}
	c.stats[k] = v
	return nil
}
func (c *fakeRC) EmitFact(k string, v any) error { return c.SetStat(k, v) }

// winHost implements WinRMCapable and WindowsFeatureCapable.
type winHost struct{ *inventorytest.Stub }

func (winHost) WinRMHost() string   { return "192.0.2.1" }
func (winHost) WinRMPort() int      { return 5985 }
func (winHost) DISMLogPath() string { return `C:\Windows\Logs\DISM\dism.log` }

func host() inventory.InventoryItem {
	return winHost{&inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsFeature}}}
}

// world is the host's capability as the stand-ins see it, and what was
// sent to change it.
type world struct {
	states    []string // the State each status read answers, in turn; "" is unrecognized
	reads     int
	statusErr error
	adds      []string // "name|source|timeout" per add
	removes   []string // "name|timeout" per remove
	result    winrmdism.ChangeResult
	applyErr  error
}

func (w *world) install(t *testing.T) {
	t.Helper()
	oldS, oldA, oldR := statusFunc, addFunc, removeFunc
	t.Cleanup(func() { statusFunc, addFunc, removeFunc = oldS, oldA, oldR })
	statusFunc = func(_ context.Context, _ winrmdism.Session, logPath, name string) (winrmdism.CapabilityState, error) {
		if logPath != `C:\Windows\Logs\DISM\dism.log` {
			t.Errorf("log path %q", logPath)
		}
		if w.statusErr != nil && w.reads > 0 {
			return winrmdism.CapabilityState{}, w.statusErr
		}
		state := w.states[min(w.reads, len(w.states)-1)]
		w.reads++
		return winrmdism.CapabilityState{Name: name, Exists: state != "", State: state}, nil
	}
	addFunc = func(_ context.Context, _ winrmdism.Session, _ string, name, source string, timeout time.Duration) (winrmdism.ChangeResult, error) {
		w.adds = append(w.adds, fmt.Sprintf("%s|%s|%s", name, source, timeout))
		return w.result, w.applyErr
	}
	removeFunc = func(_ context.Context, _ winrmdism.Session, _ string, name string, timeout time.Duration) (winrmdism.ChangeResult, error) {
		w.removes = append(w.removes, fmt.Sprintf("%s|%s", name, timeout))
		return w.result, w.applyErr
	}
}

// diff is the recorded diff's before and after states.
func diff(t *testing.T, rc *fakeRC) (before, after any) {
	t.Helper()
	d, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	b, _ := d["before"].(map[string]any)
	a, _ := d["after"].(map[string]any)
	return b["state"], a["state"]
}

// inverse is the recorded undo's method and name, or "" when none.
func inverse(rc *fakeRC) string {
	raw, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		return ""
	}
	params, _ := raw["params"].(map[string]any)
	return fmt.Sprintf("%v %v", raw["fqcn"], params[paramName])
}

// TestInstall_InstallsAsAskedAndRecordsItsUndo: a capability not present
// is added with the source and timeout given, read back, and undone by
// win.capability.remove; DISM's 3010 is reboot_required.
func TestInstall_InstallsAsAskedAndRecordsItsUndo(t *testing.T) {
	w := &world{states: []string{"Not Present", "Install Pending"}, result: winrmdism.ChangeResult{RebootRequired: true}}
	w.install(t)
	rc := newRC()
	res, err := Install(context.Background(), rc, host(), map[string]any{"name": appCompat, "source": `D:\fod`, "timeout": 900})
	if err != nil || !res.Changed {
		t.Fatalf("%+v, %v", res, err)
	}
	if len(w.adds) != 1 || w.adds[0] != appCompat+`|D:\fod|15m0s` {
		t.Errorf("adds %v", w.adds)
	}
	if b, a := diff(t, rc); b != "Not Present" || a != "Install Pending" {
		t.Errorf("diff %v to %v", b, a)
	}
	if rc.stats[statRebootRequired] != true || rc.stats[statName] != appCompat {
		t.Errorf("stats %v", rc.stats)
	}
	if got := inverse(rc); got != "win.capability.remove "+appCompat {
		t.Errorf("undo %q", got)
	}
}

// TestInstall_DoneOrPendingSendsNothing: installed is no change with no
// restart; installed pending a restart is no change that still says the
// restart finishes it. Neither sends or undoes anything.
func TestInstall_DoneOrPendingSendsNothing(t *testing.T) {
	for state, reboot := range map[string]bool{"Installed": false, "Install Pending": true} {
		w := &world{states: []string{state}}
		w.install(t)
		rc := newRC()
		res, err := Install(context.Background(), rc, host(), map[string]any{"name": appCompat})
		if err != nil || res.Changed || len(w.adds) != 0 || rc.stats[statRebootRequired] != reboot || inverse(rc) != "" {
			t.Errorf("%s: %+v %v, adds %v, stats %v", state, res, err, w.adds, rc.stats)
		}
	}
}

// TestRemove_RemovesAndRecordsItsUndo, and finds absent, staged or pending
// removal already done.
func TestRemove_RemovesAndRecordsItsUndo(t *testing.T) {
	w := &world{states: []string{"Installed", "Not Present"}}
	w.install(t)
	rc := newRC()
	res, err := Remove(context.Background(), rc, host(), map[string]any{"name": appCompat})
	if err != nil || !res.Changed || len(w.removes) != 1 || w.removes[0] != appCompat+"|1h0m0s" {
		t.Fatalf("%+v, %v, removes %v", res, err, w.removes)
	}
	if got := inverse(rc); got != "win.capability.install "+appCompat || rc.stats[statRebootRequired] != false {
		t.Errorf("undo %q, stats %v", got, rc.stats)
	}
	for state, reboot := range map[string]bool{"Not Present": false, "Staged": false, "Uninstall Pending": true} {
		w := &world{states: []string{state}}
		w.install(t)
		rc := newRC()
		res, err := Remove(context.Background(), rc, host(), map[string]any{"name": appCompat})
		if err != nil || res.Changed || len(w.removes) != 0 || rc.stats[statRebootRequired] != reboot {
			t.Errorf("%s: %+v %v, removes %v, stats %v", state, res, err, w.removes, rc.stats)
		}
	}
}

// TestRemove_PendingAfterwardIsARestart: a removal DISM answered 0 for and
// left pending still says a restart finishes it.
func TestRemove_PendingAfterwardIsARestart(t *testing.T) {
	w := &world{states: []string{"Installed", "Uninstall Pending"}}
	w.install(t)
	rc := newRC()
	if _, err := Remove(context.Background(), rc, host(), map[string]any{"name": appCompat}); err != nil || rc.stats[statRebootRequired] != true {
		t.Errorf("%v, stats %v", err, rc.stats)
	}
}

// TestChecks_ReadAndSendNothing: a check predicts the change, leaves the
// state it cannot know out of the after half, and records no undo.
func TestChecks_ReadAndSendNothing(t *testing.T) {
	w := &world{states: []string{"Not Present"}}
	w.install(t)
	rc := newRC()
	res, err := CheckInstall(context.Background(), rc, host(), map[string]any{"name": appCompat})
	if err != nil || !res.Changed || len(w.adds) != 0 || inverse(rc) != "" {
		t.Fatalf("%+v %v, adds %v, stats %v", res, err, w.adds, rc.stats)
	}
	if b, a := diff(t, rc); b != "Not Present" || a != nil {
		t.Errorf("diff %v to %v", b, a)
	}
	w = &world{states: []string{"Not Present"}}
	w.install(t)
	if res, err := CheckRemove(context.Background(), newRC(), host(), map[string]any{"name": appCompat}); err != nil || res.Changed || len(w.removes) != 0 {
		t.Errorf("a check of removing what is absent: %+v %v", res, err)
	}
}

// TestRefusals: each mistake is refused naming the method, and those in
// the task are refused before the device is read.
func TestRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		device inventory.InventoryItem
		w      world
		reads  int
		want   string
	}{
		"no name":         {map[string]any{}, host(), world{states: []string{"Installed"}}, 0, "name is required"},
		"a partial name":  {map[string]any{"name": "ServerCore.AppCompatibility"}, host(), world{states: []string{"Installed"}}, 0, "not a capability identity"},
		"zero timeout":    {map[string]any{"name": appCompat, "timeout": 0}, host(), world{states: []string{"Installed"}}, 0, "more than 0"},
		"text timeout":    {map[string]any{"name": appCompat, "timeout": "60"}, host(), world{states: []string{"Installed"}}, 0, "not a whole number"},
		"numeric source":  {map[string]any{"name": appCompat, "source": 5}, host(), world{states: []string{"Installed"}}, 0, "not text"},
		"no device":       {map[string]any{"name": appCompat}, nil, world{states: []string{"Installed"}}, 0, "needs a target device"},
		"no accessors":    {map[string]any{"name": appCompat}, &inventorytest.Stub{StubName: "bare"}, world{states: []string{"Installed"}}, 0, "does not implement its accessors"},
		"unknown":         {map[string]any{"name": appCompat}, host(), world{states: []string{""}}, 1, "does not recognize a capability"},
		"a failed change": {map[string]any{"name": appCompat}, host(), world{states: []string{"Not Present"}, applyErr: errors.New("dism exited 0x800F0954")}, 1, "0x800F0954"},
		"a failed reread": {map[string]any{"name": appCompat}, host(), world{states: []string{"Not Present"}, statusErr: errors.New("host went away")}, 1, "host went away"},
	} {
		w := tc.w
		w.install(t)
		_, err := Install(context.Background(), newRC(), tc.device, tc.params)
		if err == nil || !strings.HasPrefix(err.Error(), "win.capability.install: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if w.reads != tc.reads {
			t.Errorf("%s: the device was read %d times, want %d", name, w.reads, tc.reads)
		}
	}
}

// TestStatFailuresAreErrors: a run whose answer cannot be recorded fails,
// on each stat it writes, in a run and in a check.
func TestStatFailuresAreErrors(t *testing.T) {
	for _, key := range []string{statName, statRebootRequired, sdk.StatDiff, sdk.StatInverse} {
		w := &world{states: []string{"Not Present", "Installed"}}
		w.install(t)
		rc := newRC()
		rc.failKey = key
		if _, err := Install(context.Background(), rc, host(), map[string]any{"name": appCompat}); err == nil {
			t.Errorf("a run that could not write %s succeeded", key)
		}
	}
	w := &world{states: []string{"Not Present"}}
	w.install(t)
	rc := newRC()
	rc.failKey = statName
	if _, err := CheckInstall(context.Background(), rc, host(), map[string]any{"name": appCompat}); err == nil {
		t.Error("a check that could not write its name succeeded")
	}
}

// TestRegistration: both are implemented, checkable, elevated and each
// other's undo, with only the name kept in the journal.
func TestRegistration(t *testing.T) {
	for fqcn, undo := range map[string]string{fqcnInstall: fqcnRemove, fqcnRemove: fqcnInstall} {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		m := d.Manifest
		if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || !m.ExecutionContext.RequiresElevation {
			t.Errorf("%s: %+v", fqcn, m)
		}
		if !m.Reversibility.Reversible || len(m.Reversibility.Inverses) != 1 || m.Reversibility.Inverses[0].FQCN != undo ||
			strings.Join(m.Reversibility.Inverses[0].Record, ",") != paramName {
			t.Errorf("%s: reversibility %+v", fqcn, m.Reversibility)
		}
	}
}
