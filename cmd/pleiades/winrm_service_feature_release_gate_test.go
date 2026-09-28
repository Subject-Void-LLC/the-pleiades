package main_test

import (
	"os"
	"strings"
	"testing"
)

// This file is the Release Gate for svc.windows.*/win.feature.*: the real
// built binary, driven the way a user calls one method once
// (`pleiades adhoc ... --json`), against a real Windows host over real
// WinRM. Since Phase 46 it also holds every check to its real run: each
// method's check must predict the change, move nothing, agree with what
// the run then does, and predict nothing once the host has converged
// (checkThenRun, shared with the container check gates).
//
// # Why this one is env-gated, and reuses winrm_static_ip_release_gate_test.go's constants
//
// The identical reasoning that file's own doc comment gives: there is no
// Windows container a Linux CI box can run, and no way to fake WinRM
// without also faking the thing under test, so this needs a real host and
// skips clearly when it does not have one. It reuses
// envWinRMHost/envWinRMUser/envWinRMPassword rather than declaring its own
// copies, since those three name the same lab target either gate would
// point at; it does NOT reuse envWinRMAdapter/envWinRMIP/envWinRMMask/
// envWinRMGateway/envWinRMDNS, which are scoped to that file's own
// network-reconfiguration scenario and have nothing to do with a service
// or a feature.
//
// # What this gate does NOT try to prove
//
// pkg/winrmsvc and pkg/winrmdism's own unit tests already cover script
// construction, quoting, and state parsing against canned input, and
// internal/catalog/svc/windows and internal/catalog/win/feature's own
// tests already cover every refusal and decision branch by swapping their
// statusFunc/applyFunc seams. This gate exists for the one thing none of
// that can reach: a real Service Control Manager and a real DISM actually
// changing a real Windows host's state, and this platform reading that
// change back correctly.
//
// # The reverse is not optional here either
//
// The static-IP gate's own doc comment states the rule this file follows
// too: a gate that leaves a lab machine altered can only be run once, and
// the reverse is the reversibility contract's own claim made executable.
// Each test below performs a small, deliberately reversible cycle and
// ends the target service running (and Automatic) and the target feature
// enabled -- a fixed, known end state rather than whatever the machine
// happened to hold before, so a re-run starts from a state this file
// already knows how to handle. Point PLEIADES_WINRM_TEST_SERVICE and
// PLEIADES_WINRM_TEST_FEATURE at a service/feature you are fine leaving
// running/enabled on the target host.

const (
	envWinRMTestService = "PLEIADES_WINRM_TEST_SERVICE"
	envWinRMTestFeature = "PLEIADES_WINRM_TEST_FEATURE"

	// envWinRMProject and envWinRMDevice point these gates at a project
	// that already manages the Windows host, run as a user runs it: the
	// binary decrypts the device's credential from that project's own
	// vault, so no password is put in the environment. The VirtualBox lab
	// (examples/virtualbox_lab) is such a project. They take precedence over
	// PLEIADES_WINRM_HOST, _USER and _PASSWORD, which build a throwaway
	// project instead.
	envWinRMProject = "PLEIADES_WINRM_PROJECT"
	envWinRMDevice  = "PLEIADES_WINRM_DEVICE"
)

// winrmCheckTarget returns the project directory and inventory name the
// gate runs against, and the service or feature thingEnv names, skipping
// with hint when no target or no thing is configured.
func winrmCheckTarget(t *testing.T, thingEnv, hint string) (dir, host, thing string) {
	t.Helper()
	thing = os.Getenv(thingEnv)
	project, device := os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)
	cfg := winrmGateConfig{host: os.Getenv(envWinRMHost), user: os.Getenv(envWinRMUser), password: os.Getenv(envWinRMPassword)}
	switch {
	case thing == "":
	case project != "" && device != "":
		return project, device, thing
	case cfg.host != "" && cfg.user != "" && cfg.password != "":
		return winrmGateProject(t, cfg), "win-gate", thing
	}
	t.Skipf("this Release Gate needs a real Windows host: set %s and %s (a project that manages it), or %s, %s and %s; "+
		"and %s. %s", envWinRMProject, envWinRMDevice, envWinRMHost, envWinRMUser, envWinRMPassword, thingEnv, hint)
	return "", "", ""
}

// serviceHint and featureHint say what each gate does to the thing it is
// pointed at, so a person chooses one they are fine changing.
const (
	serviceHint = "The gate stops, starts, restarts and disables the named service and ends it running and Automatic, " +
		"so name one you are fine briefly stopping: SysMain is on Windows Server 2025, including Server Core, " +
		"which has no Spooler."
	featureHint = "The gate disables the named DISM feature and enables it again, ending it enabled, so name one you " +
		"are fine leaving enabled that needs no restart to apply (TelnetClient)."
)

// winrmRead runs a PowerShell expression on the host through
// exec.winrm.shell, a different method from the one under test, and
// returns its trimmed output: the state read beside the method rather
// than from its own report. Anything on stderr fails the test, so a read
// that went wrong cannot pass as a state.
func winrmRead(t *testing.T, dir, host, expr string) string {
	t.Helper()
	rep, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", host, "exec.winrm.shell", "shell=powershell", "command="+expr)
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("reading %q on %s (exit %d):\n%s", expr, host, code, raw)
	}
	if stderr := strings.TrimSpace(stringStat(rep.Tasks[0].Stats, "stderr")); stderr != "" {
		t.Fatalf("reading %q on %s wrote to stderr: %s", expr, host, stderr)
	}
	return strings.TrimSpace(stringStat(rep.Tasks[0].Stats, "stdout"))
}

// winrmAdhoc runs one call for its effect and fails on any error. The
// gates use it to reach a known starting state.
func winrmAdhoc(t *testing.T, dir, host, method string, params ...string) {
	t.Helper()
	rep, raw, code := runPleiadesJSONWithHome(t, dir, "", append([]string{"adhoc", host, method}, params...)...)
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("%s on %s (exit %d):\n%s", method, host, code, raw)
	}
}

// TestWinRMGate_ServiceStartStopAndBack cycles a real service through
// start, stop, start (the reverse) and restart, each through checkThenRun
// except the restart, which never converges.
//
// Starting first establishes the fixed starting point (running) the rest
// assumes, and is a documented no-op when the service is already running.
func TestWinRMGate_ServiceStartStopAndBack(t *testing.T) {
	dir, host, service := winrmCheckTarget(t, envWinRMTestService, serviceHint)
	status := func() string { return winrmRead(t, dir, host, "(Get-Service -Name '"+service+"').Status") }

	winrmAdhoc(t, dir, host, "svc.windows.start", "name="+service)
	checkThenRun(t, dir, "", host, status, "svc.windows.stop", "name="+service)
	// The reverse. Left running is the fixed end state this file's doc
	// promises.
	checkThenRun(t, dir, "", host, status, "svc.windows.start", "name="+service)

	// A restart always makes a change, so its proof is that the check
	// predicts it and moves nothing, and the run leaves the service running.
	check, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", host, "svc.windows.restart", "name="+service, "--mode", "check")
	if code != 0 || check.Tasks[0].Status != "would_change" {
		t.Fatalf("the restart check did not predict the restart (exit %d):\n%s", code, raw)
	}
	if got := status(); got != "Running" {
		t.Fatalf("after the restart check %s is %s, want it still Running", service, got)
	}
	run, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", host, "svc.windows.restart", "name="+service)
	if code != 0 || run.Tasks[0].Status != "changed" {
		t.Fatalf("the restart did not run (exit %d):\n%s", code, raw)
	}
	predictionMatches(t, "svc.windows.restart", check.Tasks[0].Stats, run.Tasks[0].Stats)
	if got := status(); got != "Running" {
		t.Fatalf("after the restart %s is %s, want Running", service, got)
	}
}

// TestWinRMGate_ServiceEnableDisableAndBack is the same shape for start
// type: enable, disable, enable (the reverse), ending Automatic.
func TestWinRMGate_ServiceEnableDisableAndBack(t *testing.T) {
	dir, host, service := winrmCheckTarget(t, envWinRMTestService, serviceHint)
	startType := func() string { return winrmRead(t, dir, host, "(Get-Service -Name '"+service+"').StartType") }

	winrmAdhoc(t, dir, host, "svc.windows.enable", "name="+service)
	checkThenRun(t, dir, "", host, startType, "svc.windows.disable", "name="+service)
	checkThenRun(t, dir, "", host, startType, "svc.windows.enable", "name="+service)
}

// TestWinRMGate_FeatureRemoveInstallAndBack cycles a real DISM feature
// through install, remove, install (the reverse), ending enabled.
func TestWinRMGate_FeatureRemoveInstallAndBack(t *testing.T) {
	dir, host, feature := winrmCheckTarget(t, envWinRMTestFeature, featureHint)
	state := func() string {
		return winrmRead(t, dir, host, "(Get-WindowsOptionalFeature -Online -FeatureName '"+feature+"').State")
	}

	winrmAdhoc(t, dir, host, "win.feature.install", "name="+feature)
	checkThenRun(t, dir, "", host, state, "win.feature.remove", "name="+feature)
	checkThenRun(t, dir, "", host, state, "win.feature.install", "name="+feature)
}
