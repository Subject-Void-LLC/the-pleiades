package systemd_test

import (
	"context"
	"os"
	"path/filepath"
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
}

func newHarness(t *testing.T, state systemdState) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	// The record path travels by environment rather than being
	// interpolated: t.TempDir derives its path from the test name, and a
	// test named after a hostile unit could otherwise get a path the
	// shell re-expands.
	script := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
if [ "$1" = "show" ]; then
  printf 'LoadState=%s\n' "$FAKE_LOAD"
  printf 'ActiveState=%s\n' "$FAKE_ACTIVE"
  printf 'UnitFileState=%s\n' "$FAKE_UNITFILE"
  exit 0
fi
exit "${FAKE_EXIT:-0}"
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake systemctl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_LOAD", state.load)
	t.Setenv("FAKE_ACTIVE", state.active)
	t.Setenv("FAKE_UNITFILE", state.unitFile)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return &harness{
		rc:     &svcContext{secrets: srv.Secrets(), stats: map[string]any{}},
		device: &svcTarget{Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameSystemd}}, host: srv.Host, port: srv.Port},
		record: record,
	}
}

func (h *harness) params(unit string) map[string]any {
	return map[string]any{"name": unit, "insecure_skip_host_key_verify": true}
}

// verbs returns the systemctl verb of every non-show invocation, which is
// what proves a converged run sent nothing.
func (h *harness) verbs(t *testing.T) []string {
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
			if line != "show" {
				verbs = append(verbs, line)
			}
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
		})
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
