package systemd_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc/systemd"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with a systemctl on PATH that is a shell script. The SSH
// transport, the shell, the quoting, the argument vector and the exit
// status are all genuine; only the daemon at the far end is not. Proving
// these against real systemd is a container Release Gate's job.

// ---------- harness ----------

type svcContext struct {
	secrets map[string]string
	stats   map[string]any
}

func (c *svcContext) InjectSecrets() map[string]string { return c.secrets }
func (c *svcContext) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}
func (c *svcContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

type svcTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *svcTarget) SSHHost() string { return d.host }
func (d *svcTarget) SSHPort() int    { return d.port }

// systemdState is what the fake systemctl reports for `show`.
type systemdState struct {
	load, active, unitFile string
}

var (
	runningEnabled  = systemdState{"loaded", "active", "enabled"}
	stoppedDisabled = systemdState{"loaded", "inactive", "disabled"}
	runningDisabled = systemdState{"loaded", "active", "disabled"}
	stoppedEnabled  = systemdState{"loaded", "inactive", "enabled"}
	absent          = systemdState{"not-found", "inactive", ""}
	masked          = systemdState{"masked", "inactive", "masked"}
	static          = systemdState{"loaded", "active", "static"}
	runtimeEnabled  = systemdState{"loaded", "active", "enabled-runtime"}
)

// harness wires a real SSH server, a fake systemctl and a device, and
// returns everything a method call needs plus a way to read back which
// systemctl commands were actually sent.
type harness struct {
	rc     *svcContext
	device inventory.InventoryItem
	record string

	// stateDir holds the fake unit's three state strings, one file each,
	// which is what lets a test read back what the unit looks like after
	// a call rather than trusting what the method recorded about it.
	stateDir string
}

// The fake systemctl keeps the unit's state in files and a successful
// verb changes it the way systemd would: start and restart leave it
// active, stop leaves it inactive, enable and disable set the boot-time
// state. That is what makes a real run's read-back a reading of what its
// own verb did, and so what lets a check's predicted after half be
// compared against the after half a real run records from the same
// starting state. The model is only as faithful as these five lines;
// proving it against real systemd is the container Release Gate's job,
// as the header above says.
//
// Every path travels by environment rather than being interpolated:
// t.TempDir derives its path from the test name, and a test named after
// a hostile unit could otherwise get a path the shell re-expands.
const fakeSystemctl = `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
if [ "$1" = "show" ]; then
  printf 'LoadState=%s\n' "$(cat "$FAKE_STATE/load")"
  printf 'ActiveState=%s\n' "$(cat "$FAKE_STATE/active")"
  printf 'UnitFileState=%s\n' "$(cat "$FAKE_STATE/unitfile")"
  exit 0
fi
status="${FAKE_EXIT:-0}"
if [ "$status" != 0 ]; then exit "$status"; fi
case "$1" in
  start|restart) printf 'active' > "$FAKE_STATE/active" ;;
  stop) printf 'inactive' > "$FAKE_STATE/active" ;;
  enable) printf 'enabled' > "$FAKE_STATE/unitfile" ;;
  disable) printf 'disabled' > "$FAKE_STATE/unitfile" ;;
esac
exit 0
`

func newHarness(t *testing.T, state systemdState) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatalf("creating the fake unit's state directory: %v", err)
	}
	for name, value := range map[string]string{"load": state.load, "active": state.active, "unitfile": state.unitFile} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(value), 0o600); err != nil {
			t.Fatalf("writing the fake unit's %s state: %v", name, err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(fakeSystemctl), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake systemctl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_STATE", stateDir)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return &harness{
		rc:       &svcContext{secrets: srv.Secrets(), stats: map[string]any{}},
		device:   &svcTarget{Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameSystemd}}, host: srv.Host, port: srv.Port},
		record:   record,
		stateDir: stateDir,
	}
}

// state reads the fake unit's state back from its files, which is what
// the unit looks like on the "device" right now.
func (h *harness) state(t *testing.T) systemdState {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(h.stateDir, name)) // #nosec G304 -- path built by this test
		if err != nil {
			t.Fatalf("reading the fake unit's %s state: %v", name, err)
		}
		return string(data)
	}
	return systemdState{load: read("load"), active: read("active"), unitFile: read("unitfile")}
}

func (h *harness) params(unit string) map[string]any {
	return map[string]any{"name": unit, "insecure_skip_host_key_verify": true}
}

// verbs returns the systemctl verb of every non-show invocation, which is
// what proves a converged run sent nothing.
func (h *harness) verbs(t *testing.T) []string {
	t.Helper()
	var verbs []string
	for _, verb := range h.invocations(t) {
		if verb != "show" {
			verbs = append(verbs, verb)
		}
	}
	return verbs
}

// invocations returns the verb of every systemctl invocation in order,
// reads included, which is what proves a check read the unit once and
// did not read it back.
func (h *harness) invocations(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(h.record) // #nosec G304 -- path built by this test
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading recorded invocations: %v", err)
	}
	var verbs []string
	first := true
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "---" {
			first = true
			continue
		}
		if first {
			first = false
			verbs = append(verbs, line)
		}
	}
	return verbs
}

// recordedInverse reads back what sdk.RecordInverse stored, which is a
// map holding fqcn, params and description.
func recordedInverse(t *testing.T, h *harness) (fqcn string, params map[string]any, description string, present bool) {
	t.Helper()
	raw, ok := h.rc.stats[sdk.StatInverse]
	if !ok {
		return "", nil, "", false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("inverse is %T, want map[string]any", raw)
	}
	fqcn, _ = m["fqcn"].(string)
	params, _ = m["params"].(map[string]any)
	description, _ = m["description"].(string)
	return fqcn, params, description, true
}

// ---------- registration ----------

// TestAllSixAreImplemented is the claim the module catalog makes about
// this namespace, checked against the live registry rather than a list.
func TestAllSixAreImplemented(t *testing.T) {
	for _, fqcn := range []string{
		"svc.systemd.start", "svc.systemd.stop", "svc.systemd.restart",
		"svc.systemd.enable", "svc.systemd.disable", "svc.systemd.daemon_reload",
	} {
		t.Run(fqcn, func(t *testing.T) {
			desc, ok := collection.Lookup(fqcn)
			if !ok {
				t.Fatalf("%s is not registered", fqcn)
			}
			if desc.Manifest.Status != collection.StatusImplemented {
				t.Errorf("Status = %v, want implemented", desc.Manifest.Status)
			}
			if desc.Invoke == nil {
				t.Error("Invoke is nil")
			}
			if len(desc.Manifest.RequiredCapabilities) != 1 || desc.Manifest.RequiredCapabilities[0] != capability.NameSystemd {
				t.Errorf("RequiredCapabilities = %v, want [SystemdCapable]", desc.Manifest.RequiredCapabilities)
			}
			// A method answering "not reversible" must say why, and
			// Register already enforces that; this checks the answer was
			// actually considered rather than left as the zero value with
			// a throwaway note.
			if !desc.Manifest.Reversibility.Reversible && desc.Manifest.Reversibility.Notes == "" {
				t.Error("declares itself not reversible with no reason")
			}
			// Every method in this namespace can say what it would change
			// without changing it, and the declaration has a function
			// behind it. Register already refuses the two disagreeing;
			// this pins that the answer is yes rather than no.
			if !desc.Manifest.SupportsCheck {
				t.Error("SupportsCheck = false, want true: every svc.systemd method has a check")
			}
			if desc.Check == nil {
				t.Error("Check is nil, so a check run would report this method as uncheckable")
			}
		})
	}
}

// ---------- check mode ----------

// methodFor returns the function the engine would call for fqcn in mode,
// looked up through the registry and collection.Descriptor.MethodFor
// exactly as the dispatcher looks it up. Going through the registry
// rather than calling CheckStart directly is what proves the REGISTRATION
// hands a check the check function, not only that the function exists.
func methodFor(t *testing.T, fqcn string, mode collection.Mode) collection.Method {
	t.Helper()
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	method, err := desc.MethodFor(mode)
	if err != nil {
		t.Fatalf("%s in mode %s: %v", fqcn, mode, err)
	}
	return method
}

// diffHalves reads back the diff a call recorded.
func diffHalves(t *testing.T, h *harness) (before, after map[string]any) {
	t.Helper()
	d, ok := h.rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded, got %#v", h.rc.stats[sdk.StatDiff])
	}
	before, _ = d[sdk.DiffBefore].(map[string]any)
	after, _ = d[sdk.DiffAfter].(map[string]any)
	return before, after
}

// TestCheckPredictsWhatARealRunDoes is the check-mode contract for every
// unit-shaped method, in two halves run from the same starting state.
//
// The check half proves the check changed nothing: it read the unit once,
// sent no verb, left the fake unit's state exactly as it found it, and
// recorded no inverse. The control half then runs the REAL method from
// that same starting state, and the check's answer has to match it: the
// same Changed, the same before, and a predicted after equal to the after
// the real run read back from the device once its verb had landed. A
// check that predicted from a different rule than the real run acts on
// would fail the second half even if it passed the first.
func TestCheckPredictsWhatARealRunDoes(t *testing.T) {
	tests := []struct {
		name        string
		fqcn        string
		state       systemdState
		wantChanged bool
	}{
		{name: "start, stopped", fqcn: "svc.systemd.start", state: stoppedDisabled, wantChanged: true},
		{name: "start, already running", fqcn: "svc.systemd.start", state: runningEnabled, wantChanged: false},
		{name: "stop, running", fqcn: "svc.systemd.stop", state: runningEnabled, wantChanged: true},
		{name: "stop, already stopped", fqcn: "svc.systemd.stop", state: stoppedDisabled, wantChanged: false},
		{name: "stop, masked", fqcn: "svc.systemd.stop", state: masked, wantChanged: false},
		{name: "restart, running", fqcn: "svc.systemd.restart", state: runningEnabled, wantChanged: true},
		{name: "restart, stopped", fqcn: "svc.systemd.restart", state: stoppedDisabled, wantChanged: true},
		{name: "enable, disabled", fqcn: "svc.systemd.enable", state: runningDisabled, wantChanged: true},
		{name: "enable, already enabled", fqcn: "svc.systemd.enable", state: stoppedEnabled, wantChanged: false},
		{name: "enable, enabled-runtime", fqcn: "svc.systemd.enable", state: runtimeEnabled, wantChanged: false},
		{name: "disable, enabled", fqcn: "svc.systemd.disable", state: runningEnabled, wantChanged: true},
		{name: "disable, already disabled", fqcn: "svc.systemd.disable", state: runningDisabled, wantChanged: false},
		{name: "disable, masked", fqcn: "svc.systemd.disable", state: masked, wantChanged: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The check.
			h := newHarness(t, tt.state)
			checked, err := methodFor(t, tt.fqcn, collection.ModeCheck)(context.Background(), h.rc, h.device, h.params("nginx"))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if checked.Changed != tt.wantChanged {
				t.Errorf("check Changed = %v, want %v", checked.Changed, tt.wantChanged)
			}
			if got := h.invocations(t); strings.Join(got, ",") != "show" {
				t.Errorf("systemctl invocations = %v, want exactly one show: a check reads once and sends nothing", got)
			}
			if got := h.state(t); got != tt.state {
				t.Errorf("the unit is %+v after the check, want it untouched at %+v", got, tt.state)
			}
			if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
				t.Error("a check recorded an inverse, but it changed nothing there is to undo")
			}
			if got := h.rc.stats["name"]; got != "nginx" {
				t.Errorf("stat name = %v, want nginx", got)
			}
			checkBefore, checkAfter := diffHalves(t, h)

			// The control: the real method, from the same starting state.
			c := newHarness(t, tt.state)
			real, err := methodFor(t, tt.fqcn, collection.ModeExecute)(context.Background(), c.rc, c.device, c.params("nginx"))
			if err != nil {
				t.Fatalf("real run: %v", err)
			}
			if real.Changed != checked.Changed {
				t.Errorf("the check predicted Changed = %v, but the real run reported %v", checked.Changed, real.Changed)
			}
			// The control has to have actually done something when it
			// reported a change, or its after half proves nothing.
			if real.Changed && len(c.verbs(t)) == 0 {
				t.Fatal("the real run reported a change and sent no verb, so it cannot serve as the control")
			}
			realBefore, realAfter := diffHalves(t, c)
			if !reflect.DeepEqual(checkBefore, realBefore) {
				t.Errorf("the check recorded before %v, the real run %v", checkBefore, realBefore)
			}
			if !reflect.DeepEqual(checkAfter, realAfter) {
				t.Errorf("the check predicted after %v, but the real run left %v", checkAfter, realAfter)
			}
		})
	}
}

// TestCheckRefusesWhatARealRunRefuses proves a check fails on exactly the
// units a real run refuses, with the same message, and before sending
// anything. A dry run that predicted "would start" for a typo'd or masked
// unit would be a clean report for a plan that fails.
func TestCheckRefusesWhatARealRunRefuses(t *testing.T) {
	tests := []struct {
		fqcn  string
		state systemdState
	}{
		{fqcn: "svc.systemd.start", state: absent},
		{fqcn: "svc.systemd.stop", state: absent},
		{fqcn: "svc.systemd.restart", state: absent},
		{fqcn: "svc.systemd.enable", state: absent},
		{fqcn: "svc.systemd.disable", state: absent},
		{fqcn: "svc.systemd.start", state: masked},
		{fqcn: "svc.systemd.restart", state: masked},
		{fqcn: "svc.systemd.enable", state: masked},
		{fqcn: "svc.systemd.enable", state: static},
		{fqcn: "svc.systemd.disable", state: static},
	}

	for _, tt := range tests {
		t.Run(tt.fqcn+" on "+tt.state.load+"/"+tt.state.unitFile, func(t *testing.T) {
			h := newHarness(t, tt.state)
			_, checkErr := methodFor(t, tt.fqcn, collection.ModeCheck)(context.Background(), h.rc, h.device, h.params("nginx"))
			if checkErr == nil {
				t.Fatal("the check predicted success for a unit a real run refuses")
			}
			if verbs := h.verbs(t); len(verbs) != 0 {
				t.Errorf("systemctl verbs sent = %v, want none", verbs)
			}

			c := newHarness(t, tt.state)
			_, realErr := methodFor(t, tt.fqcn, collection.ModeExecute)(context.Background(), c.rc, c.device, c.params("nginx"))
			if realErr == nil {
				t.Fatal("the real run did not refuse, so this case is not testing a refusal")
			}
			if checkErr.Error() != realErr.Error() {
				t.Errorf("check refused with %q, the real run with %q: the same refusal must read the same", checkErr, realErr)
			}
		})
	}
}

// TestCheckMissingNameIsRefused proves an authoring mistake fails a check
// the same way it fails a real run.
func TestCheckMissingNameIsRefused(t *testing.T) {
	h := newHarness(t, runningEnabled)

	_, err := methodFor(t, "svc.systemd.start", collection.ModeCheck)(context.Background(), h.rc, h.device,
		map[string]any{"insecure_skip_host_key_verify": true})
	if err == nil {
		t.Fatal("expected a refusal when name is missing")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %v, want it to name the missing parameter", err)
	}
	if got := h.invocations(t); len(got) != 0 {
		t.Errorf("systemctl invocations = %v, want none: the refusal comes before anything is read", got)
	}
}

// TestCheckDaemonReload covers the check of the one method that is not
// unit-shaped. It predicts the change the real run always reports, sends
// nothing, records nothing, and still connects: a dry run against a
// device that cannot be reached fails rather than predicting a reload
// that could never be sent.
func TestCheckDaemonReload(t *testing.T) {
	params := map[string]any{"insecure_skip_host_key_verify": true}
	check := methodFor(t, "svc.systemd.daemon_reload", collection.ModeCheck)

	h := newHarness(t, runningEnabled)
	result, err := check(context.Background(), h.rc, h.device, params)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Changed {
		t.Error("check Changed = false, but a real daemon_reload always reports a change")
	}
	if got := h.invocations(t); len(got) != 0 {
		t.Errorf("systemctl invocations = %v, want none", got)
	}
	if len(h.rc.stats) != 0 {
		t.Errorf("the check recorded %v, but a real daemon_reload records nothing", h.rc.stats)
	}

	// A device with no SSH transport at all: the connect is what fails.
	unreachable := &inventorytest.Stub{StubName: "nowhere", Caps: []capability.Name{capability.NameSystemd}}
	if _, err := check(context.Background(), h.rc, unreachable, params); err == nil {
		t.Error("a check against an unreachable device predicted a reload rather than failing")
	}
}

// ---------- convergence ----------

// TestConvergence is the property this namespace is most prone to
// losing: a method that sends its verb unconditionally works, and reports
// changed forever, which is indistinguishable from one that is genuinely
// fixing something every run.
func TestConvergence(t *testing.T) {
	tests := []struct {
		name        string
		state       systemdState
		invoke      collection.Method
		wantChanged bool
		wantVerbs   []string
	}{
		{
			name:  "start on a running unit does nothing",
			state: runningEnabled, invoke: systemd.Start,
			wantChanged: false, wantVerbs: nil,
		},
		{
			name:  "start on a stopped unit starts it",
			state: stoppedDisabled, invoke: systemd.Start,
			wantChanged: true, wantVerbs: []string{"start"},
		},
		{
			name:  "stop on a stopped unit does nothing",
			state: stoppedDisabled, invoke: systemd.Stop,
			wantChanged: false, wantVerbs: nil,
		},
		{
			name:  "stop on a running unit stops it",
			state: runningEnabled, invoke: systemd.Stop,
			wantChanged: true, wantVerbs: []string{"stop"},
		},
		{
			name:  "enable on an enabled unit does nothing",
			state: stoppedEnabled, invoke: systemd.Enable,
			wantChanged: false, wantVerbs: nil,
		},
		{
			name:  "enable on a disabled unit enables it",
			state: runningDisabled, invoke: systemd.Enable,
			wantChanged: true, wantVerbs: []string{"enable"},
		},
		{
			name:  "enable treats enabled-runtime as already enabled",
			state: runtimeEnabled, invoke: systemd.Enable,
			wantChanged: false, wantVerbs: nil,
		},
		{
			name:  "disable on a disabled unit does nothing",
			state: runningDisabled, invoke: systemd.Disable,
			wantChanged: false, wantVerbs: nil,
		},
		{
			name:  "disable on an enabled unit disables it",
			state: runningEnabled, invoke: systemd.Disable,
			wantChanged: true, wantVerbs: []string{"disable"},
		},
		{
			// The one method that is never converged. Restarting a
			// running unit is the point, not a no-op.
			name:  "restart always acts, even on a running unit",
			state: runningEnabled, invoke: systemd.Restart,
			wantChanged: true, wantVerbs: []string{"restart"},
		},
		{
			name:  "restart starts a stopped unit rather than failing",
			state: stoppedDisabled, invoke: systemd.Restart,
			wantChanged: true, wantVerbs: []string{"restart"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.state)

			result, err := tt.invoke(context.Background(), h.rc, h.device, h.params("nginx"))
			if err != nil {
				t.Fatalf("invoke: %v", err)
			}
			if result.Changed != tt.wantChanged {
				t.Errorf("Changed = %v, want %v", result.Changed, tt.wantChanged)
			}
			got := h.verbs(t)
			if strings.Join(got, ",") != strings.Join(tt.wantVerbs, ",") {
				t.Errorf("systemctl verbs sent = %v, want %v", got, tt.wantVerbs)
			}
		})
	}
}

// ---------- inverses ----------

// TestInverseRecorded proves each method emits the instruction that
// undoes it, built from what the run FOUND rather than from what the
// method is.
func TestInverseRecorded(t *testing.T) {
	tests := []struct {
		name       string
		state      systemdState
		invoke     collection.Method
		wantFQCN   string
		wantRecord bool
	}{
		{name: "start records a stop", state: stoppedDisabled, invoke: systemd.Start, wantFQCN: "svc.systemd.stop", wantRecord: true},
		{name: "stop records a start", state: runningEnabled, invoke: systemd.Stop, wantFQCN: "svc.systemd.start", wantRecord: true},
		{name: "enable records a disable", state: runningDisabled, invoke: systemd.Enable, wantFQCN: "svc.systemd.disable", wantRecord: true},
		{name: "disable records an enable", state: runningEnabled, invoke: systemd.Disable, wantFQCN: "svc.systemd.enable", wantRecord: true},
		{
			// The whole point of recording at run time: a run that
			// changed nothing must emit nothing, because undoing it means
			// doing nothing. A static inverse on the manifest could not
			// express this.
			name: "a converged start records no inverse", state: runningEnabled, invoke: systemd.Start, wantRecord: false,
		},
		{
			name: "a converged stop records no inverse", state: stoppedDisabled, invoke: systemd.Stop, wantRecord: false,
		},
		{
			// A restart has no inverse at all: its effect is the
			// interruption, and restarting again would repeat it rather
			// than reverse it.
			name: "restart records no inverse", state: runningEnabled, invoke: systemd.Restart, wantRecord: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.state)

			if _, err := tt.invoke(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
				t.Fatalf("invoke: %v", err)
			}

			gotFQCN, params, _, recorded := recordedInverse(t, h)
			if recorded != tt.wantRecord {
				t.Fatalf("inverse recorded = %v, want %v", recorded, tt.wantRecord)
			}
			if !tt.wantRecord {
				return
			}
			if gotFQCN != tt.wantFQCN {
				t.Errorf("inverse fqcn = %q, want %q", gotFQCN, tt.wantFQCN)
			}
			// The instruction has to carry the unit, or a rollback would
			// know which method to call and not what to call it on.
			if params["name"] != "nginx" {
				t.Errorf("inverse params = %v, want name=nginx", params)
			}
		})
	}
}

// TestDisableNotesTheRuntimeCase covers the honesty detail: an
// enabled-runtime unit comes back permanently enabled, because
// systemctl enable cannot express "only until reboot", and the inverse
// says so rather than pretending the round trip is exact.
func TestDisableNotesTheRuntimeCase(t *testing.T) {
	h := newHarness(t, runtimeEnabled)

	if _, err := systemd.Disable(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	_, _, description, present := recordedInverse(t, h)
	if !present {
		t.Fatal("no inverse recorded for a disable that changed something")
	}
	if !strings.Contains(description, "reboot") {
		t.Errorf("description = %q, want it to say the enabled-runtime distinction is not preserved", description)
	}
}

// ---------- refusals ----------

// TestRefusals covers the three states these methods refuse before
// sending anything, each because systemd's own message for the case
// describes a symptom rather than the cause.
func TestRefusals(t *testing.T) {
	tests := []struct {
		name     string
		state    systemdState
		invoke   collection.Method
		wantText string
	}{
		{
			// The one that matters most: systemctl answers "inactive" for
			// a unit that has never existed, so a stop trusting it would
			// report success for a typo.
			name: "stopping a unit that does not exist", state: absent, invoke: systemd.Stop,
			wantText: "does not know a unit",
		},
		{name: "starting a unit that does not exist", state: absent, invoke: systemd.Start, wantText: "does not know a unit"},
		{name: "starting a masked unit", state: masked, invoke: systemd.Start, wantText: "masked"},
		{name: "restarting a masked unit", state: masked, invoke: systemd.Restart, wantText: "masked"},
		{name: "enabling a static unit", state: static, invoke: systemd.Enable, wantText: "static"},
		{name: "disabling a static unit", state: static, invoke: systemd.Disable, wantText: "static"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.state)

			_, err := tt.invoke(context.Background(), h.rc, h.device, h.params("nginx"))
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantText)
			}
			if verbs := h.verbs(t); len(verbs) != 0 {
				t.Errorf("systemctl verbs sent = %v, want none: the refusal must come before anything is sent", verbs)
			}
		})
	}
}

// TestStoppingAMaskedUnitIsNotRefused is the deliberate asymmetry. A
// masked unit cannot be running, so a stop has already got what it
// asked for, and refusing would fail a task whose goal is met.
func TestStoppingAMaskedUnitIsNotRefused(t *testing.T) {
	h := newHarness(t, masked)

	result, err := systemd.Stop(context.Background(), h.rc, h.device, h.params("nginx"))
	if err != nil {
		t.Fatalf("stopping a masked unit must succeed as already-stopped: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true for a masked unit, which was never running")
	}
}

// TestMissingNameIsRefusedBeforeConnecting proves an authoring mistake
// costs no round trip and names the runbook rather than the device.
func TestMissingNameIsRefusedBeforeConnecting(t *testing.T) {
	h := newHarness(t, runningEnabled)

	_, err := systemd.Start(context.Background(), h.rc, h.device, map[string]any{"insecure_skip_host_key_verify": true})
	if err == nil {
		t.Fatal("expected a refusal when name is missing")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %v, want it to name the missing parameter", err)
	}
}

// ---------- diff and stats ----------

// TestDiffAndStatRecorded proves both recordings happen, including on a
// converged run, because "it was already like this" is exactly what tells
// a later rollback to do nothing.
func TestDiffAndStatRecorded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state systemdState
	}{
		{name: "a run that changed something", state: stoppedDisabled},
		{name: "a converged run", state: runningEnabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.state)

			if _, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx")); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if got := h.rc.stats["name"]; got != "nginx" {
				t.Errorf("stat name = %v, want nginx", got)
			}
			diff, ok := h.rc.stats[sdk.StatDiff]
			if !ok {
				t.Fatal("no diff recorded")
			}
			d, ok := diff.(map[string]any)
			if !ok {
				t.Fatalf("diff is %T, want a map", diff)
			}
			for _, half := range []string{sdk.DiffBefore, sdk.DiffAfter} {
				side, ok := d[half].(map[string]any)
				if !ok {
					t.Fatalf("diff.%s is %T, want a map", half, d[half])
				}
				for _, key := range []string{"unit", "exists", "active", "enabled"} {
					if _, ok := side[key]; !ok {
						t.Errorf("diff.%s is missing %q", half, key)
					}
				}
			}
		})
	}
}

// ---------- daemon_reload ----------

// TestDaemonReload covers the one method that is not unit-shaped: it
// takes no name, always reports changed, and sends exactly the one verb.
func TestDaemonReload(t *testing.T) {
	h := newHarness(t, runningEnabled)

	result, err := systemd.DaemonReload(context.Background(), h.rc, h.device,
		map[string]any{"insecure_skip_host_key_verify": true})
	if err != nil {
		t.Fatalf("DaemonReload: %v", err)
	}
	if !result.Changed {
		t.Error("Changed = false: systemd cannot say whether a reload mattered, so this always reports changed")
	}
	if got := h.verbs(t); strings.Join(got, ",") != "daemon-reload" {
		t.Errorf("systemctl verbs sent = %v, want [daemon-reload]", got)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("daemon_reload recorded an inverse, but re-reading unit files has no prior state to restore")
	}
}

// TestDaemonReloadFailureIsReported proves a refused reload is an error
// rather than a silent success, which would leave a later start failing
// with a confusing "unit not found".
func TestDaemonReloadFailureIsReported(t *testing.T) {
	h := newHarness(t, runningEnabled)
	t.Setenv("FAKE_EXIT", "1")

	if _, err := systemd.DaemonReload(context.Background(), h.rc, h.device,
		map[string]any{"insecure_skip_host_key_verify": true}); err == nil {
		t.Fatal("expected an error when daemon-reload fails")
	}
}

// TestApplyFailureIsReported proves a systemctl that refuses the change
// fails the task rather than reporting a change that did not happen.
func TestApplyFailureIsReported(t *testing.T) {
	h := newHarness(t, stoppedDisabled)
	t.Setenv("FAKE_EXIT", "5")

	_, err := systemd.Start(context.Background(), h.rc, h.device, h.params("nginx"))
	if err == nil {
		t.Fatal("expected an error when systemctl start fails")
	}
	if !strings.Contains(err.Error(), "svc.systemd.start") {
		t.Errorf("error = %v, want it to name the method", err)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("an inverse was recorded for a change that failed")
	}
}
