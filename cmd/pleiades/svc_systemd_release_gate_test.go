// Release gate for the six svc.systemd.* methods and the five generic svc.*
// dispatchers: each checked, then run, through the real binary against a
// real systemd on a Rocky Linux device, on a throwaway unit.
//
// Until this gate, every one of them read implemented in the module
// catalog with only a fake systemctl behind it (Phase 38's rule is that no
// module claims implemented without a real device). Every state is read
// back with a separate systemctl show, run inside the container beside the
// product, never taken from the command's own report. The unit ends stopped
// and disabled.
package main_test

import (
	"slices"
	"strings"
	"testing"
)

// systemdGateUnit is the throwaway unit the gate acts on: a service that
// sleeps until stopped, with an [Install] section so it can be enabled.
const systemdGateUnit = "pleiades-gate"

// systemdGateUnitFile is that unit's file. description changes between the
// two writes, which is what makes systemd say it needs a reload.
func systemdGateUnitFile(description string) string {
	return "[Unit]\nDescription=" + description + "\n\n[Service]\nExecStart=/bin/sleep infinity\n\n[Install]\nWantedBy=multi-user.target\n"
}

// TestCLI_SystemdServicesCycleOnARealDevice is the gate.
func TestCLI_SystemdServicesCycleOnARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the svc.systemd gate, which builds a Rocky Linux device under systemd, in short mode")
	}
	device := startTestdataDevice(t, "rocky-systemd-sshd", rockyGatePassword, systemdDevice)
	dir, home := device.manage(t, "rocky", "linux_server,rhel_family")

	sh := func(script string) string {
		t.Helper()
		out, code := device.exec(t, "sh", "-c", script)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", script, code, out)
		}
		return out
	}
	// show reads the unit's properties beside the product, one systemctl
	// show of its own, as KEY=value lines in name order joined with "|",
	// since systemctl prints them in its own order rather than the one asked.
	show := func(props ...string) string {
		t.Helper()
		args := "systemctl show"
		for _, p := range props {
			args += " -p " + p
		}
		lines := strings.Split(sh(args+" -- "+systemdGateUnit+".service"), "\n")
		slices.Sort(lines)
		return strings.Join(lines, "|")
	}
	state := func() string { return show("ActiveState", "UnitFileState") }

	// The unit is written and loaded beside the product, then its file
	// changed, so systemd holds a stale view only a reload clears. It is
	// enabled while that happens: systemd unloads an inactive unit nothing
	// references and reads it fresh on the next show, so only a unit
	// multi-user.target wants stays loaded with the old contents.
	unitPath := "/etc/systemd/system/" + systemdGateUnit + ".service"
	sh("printf '%s' '" + systemdGateUnitFile("first") + "' > " + unitPath + " && systemctl daemon-reload && systemctl enable " + systemdGateUnit)
	sh("printf '%s' '" + systemdGateUnitFile("second") + "' > " + unitPath)
	if got := show("NeedDaemonReload", "Description"); got != "Description=first|NeedDaemonReload=yes" {
		t.Fatalf("systemd does not hold a stale view of the changed unit file, so the reload below proves nothing: %s", got)
	}

	t.Run("svc.systemd.daemon_reload", func(t *testing.T) {
		check, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", "svc.systemd.daemon_reload", "--mode", "check")
		if code != 0 || len(check.Tasks) != 1 || check.Tasks[0].Status != "would_change" {
			t.Fatalf("the check did not predict the reload (exit %d):\n%s", code, raw)
		}
		if got := show("NeedDaemonReload"); got != "NeedDaemonReload=yes" {
			t.Fatalf("the check reloaded systemd: %s", got)
		}
		run, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", "svc.systemd.daemon_reload")
		if code != 0 || len(run.Tasks) != 1 || run.Tasks[0].Status != "changed" {
			t.Fatalf("the reload did not run (exit %d):\n%s", code, raw)
		}
		if got := show("NeedDaemonReload", "Description"); got != "Description=second|NeedDaemonReload=no" {
			t.Fatalf("after the reload systemd still holds the old unit file: %s", got)
		}
		if _, ok := run.Tasks[0].Stats["inverse"]; ok {
			t.Errorf("a reload recorded an undo, which its manifest says it has none: %v", run.Tasks[0].Stats)
		}
	})

	// The cycles start from a stopped, disabled unit, set beside the product.
	sh("systemctl disable " + systemdGateUnit)
	if got := state(); got != "ActiveState=inactive|UnitFileState=disabled" {
		t.Fatalf("the throwaway unit is not stopped and disabled before the cycles: %s", got)
	}

	// restart always restarts, so it cannot converge: its proof is that the
	// check moves nothing and the run gives the unit a new main process.
	restart := func(t *testing.T, method string) {
		t.Helper()
		before := show("ActiveState", "MainPID")
		check, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", method, "name="+systemdGateUnit, "--mode", "check")
		if code != 0 || len(check.Tasks) != 1 || check.Tasks[0].Status != "would_change" {
			t.Fatalf("the check did not predict the restart (exit %d):\n%s", code, raw)
		}
		if got := show("ActiveState", "MainPID"); got != before {
			t.Fatalf("the check restarted the unit: %s became %s", before, got)
		}
		run, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", method, "name="+systemdGateUnit)
		if code != 0 || len(run.Tasks) != 1 || run.Tasks[0].Status != "changed" {
			t.Fatalf("the restart did not run (exit %d):\n%s", code, raw)
		}
		after := show("ActiveState", "MainPID")
		if !strings.HasPrefix(after, "ActiveState=active|") || after == before {
			t.Fatalf("the restart left %s, from %s: not running under a new process", after, before)
		}
		if _, ok := run.Tasks[0].Stats["inverse"]; ok {
			t.Errorf("a restart recorded an undo, which its manifest says it has none: %v", run.Tasks[0].Stats)
		}
	}

	for _, step := range []struct {
		method  string
		inverse string // the undo the run records; "" is restart's own proof
		want    string // the state the run leaves
	}{
		{"svc.systemd.start", "svc.systemd.stop", "ActiveState=active|UnitFileState=disabled"},
		{"svc.systemd.restart", "", ""},
		{"svc.systemd.stop", "svc.systemd.start", "ActiveState=inactive|UnitFileState=disabled"},
		{"svc.systemd.enable", "svc.systemd.disable", "ActiveState=inactive|UnitFileState=enabled"},
		{"svc.systemd.disable", "svc.systemd.enable", "ActiveState=inactive|UnitFileState=disabled"},
		// The generic dispatchers resolve the device's service manager and
		// record the concrete method's undo.
		{"svc.start", "svc.systemd.stop", "ActiveState=active|UnitFileState=disabled"},
		{"svc.restart", "", ""},
		{"svc.stop", "svc.systemd.start", "ActiveState=inactive|UnitFileState=disabled"},
		{"svc.enable", "svc.systemd.disable", "ActiveState=inactive|UnitFileState=enabled"},
		{"svc.disable", "svc.systemd.enable", "ActiveState=inactive|UnitFileState=disabled"},
	} {
		t.Run(step.method, func(t *testing.T) {
			if step.inverse == "" {
				restart(t, step.method)
				return
			}
			stats := checkThenRun(t, dir, home, "rocky", state, step.method, "name="+systemdGateUnit)
			if got := state(); got != step.want {
				t.Errorf("the run left %s, want %s", got, step.want)
			}
			inverse, _ := stats["inverse"].(map[string]any)
			params, _ := inverse["params"].(map[string]any)
			if inverse["fqcn"] != step.inverse || params["name"] != systemdGateUnit {
				t.Errorf("the run recorded the undo %v, want %s of %s", stats["inverse"], step.inverse, systemdGateUnit)
			}
		})
	}

	// The known state the gate ends in, read once more beside the product.
	if got := state(); got != "ActiveState=inactive|UnitFileState=disabled" {
		t.Errorf("the gate ended with the unit %s, want it stopped and disabled", got)
	}
}
