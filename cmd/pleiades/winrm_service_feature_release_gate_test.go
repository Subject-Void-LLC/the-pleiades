package main_test

import (
	"os"
	"strings"
	"testing"
)

// This file is the Release Gate for svc.windows.*/win.feature.*: the real
// built binary, driven through init, add-host, add-credential and run,
// against a real Windows host over real WinRM.
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
)

// winrmServiceGate skips unless the lab target and a service name to
// exercise are both configured.
func winrmServiceGate(t *testing.T) (cfg winrmGateConfig, service string) {
	t.Helper()

	cfg = winrmGateConfig{
		host:     os.Getenv(envWinRMHost),
		user:     os.Getenv(envWinRMUser),
		password: os.Getenv(envWinRMPassword),
	}
	service = os.Getenv(envWinRMTestService)

	var missing []string
	for name, value := range map[string]string{
		envWinRMHost: cfg.host, envWinRMUser: cfg.user, envWinRMPassword: cfg.password,
		envWinRMTestService: service,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("svc.windows.* Release Gate needs a real Windows host and a service to exercise; set %s. "+
			"This gate stops and restarts the named service, ends it running with an Automatic start type, "+
			"and toggles its start type through Disabled and back, so point %s at a service you are fine "+
			"briefly stopping and leaving running/Automatic afterward (Spooler is a reasonable default on a "+
			"stock Windows Server image).",
			strings.Join(missing, ", "), envWinRMTestService)
	}
	return cfg, service
}

// winrmFeatureGate skips unless the lab target and a DISM feature name to
// exercise are both configured.
func winrmFeatureGate(t *testing.T) (cfg winrmGateConfig, feature string) {
	t.Helper()

	cfg = winrmGateConfig{
		host:     os.Getenv(envWinRMHost),
		user:     os.Getenv(envWinRMUser),
		password: os.Getenv(envWinRMPassword),
	}
	feature = os.Getenv(envWinRMTestFeature)

	var missing []string
	for name, value := range map[string]string{
		envWinRMHost: cfg.host, envWinRMUser: cfg.user, envWinRMPassword: cfg.password,
		envWinRMTestFeature: feature,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("win.feature.* Release Gate needs a real Windows host and a feature to exercise; set %s. "+
			"This gate disables the named DISM feature and re-enables it, ending it enabled, so point %s at "+
			"a feature you are fine leaving enabled afterward and that does not need a restart to finish "+
			"applying (TelnetClient is a lightweight, commonly-available example that needs neither).",
			strings.Join(missing, ", "), envWinRMTestFeature)
	}
	return cfg, feature
}

// TestWinRMGate_ServiceStartStopAndBack cycles a real service through
// start -> stop -> start (the reverse), asserting the diff each task
// records actually changed the way it claims.
//
// Starting first rather than reading state separately is deliberate:
// svc.windows.* has no bare "read only" FQCN, since every real method is
// read-then-act by design (see internal/catalog/svc/windows's own package
// doc). Starting a service that is already running is a documented no-op,
// so this is safe regardless of the service's state when the gate begins,
// and it establishes the fixed starting point (running) the rest of the
// cycle assumes.
func TestWinRMGate_ServiceStartStopAndBack(t *testing.T) {
	cfg, service := winrmServiceGate(t)
	dir := winrmGateProject(t, cfg)

	run := func(name, fqcn string) string {
		t.Helper()
		rb := writeRunbook(t, dir, name, "id: "+name+"\nhosts: win-gate\ntasks:\n"+
			"  - name: "+fqcn+"\n    "+fqcn+":\n      name: "+service+"\n")
		out, err := runPleiades(t, dir, "run", rb, "--verbose")
		if err != nil {
			t.Fatalf("%s against %s: %v\n%s", fqcn, service, err, out)
		}
		return out
	}

	// Establish a known starting point: running.
	run("service_start_baseline", "svc.windows.start")

	stopOut := run("service_stop", "svc.windows.stop")
	if before, after := diffSide(stopOut, "before"), diffSide(stopOut, "after"); before["running"] != "true" || after["running"] != "false" {
		t.Errorf("svc.windows.stop's diff does not show a running -> not-running transition:\n%s", stopOut)
	}

	// The reverse. Left running is the fixed end state this file's own
	// doc comment promises.
	startOut := run("service_start_reverse", "svc.windows.start")
	if diffSide(startOut, "after")["running"] != "true" {
		t.Errorf("the reversing svc.windows.start did not leave %s reporting running:\n%s", service, startOut)
	}
}

// TestWinRMGate_ServiceEnableDisableAndBack is
// TestWinRMGate_ServiceStartStopAndBack's own shape for start type rather
// than run state: enable -> disable -> enable (the reverse), ending
// Automatic.
func TestWinRMGate_ServiceEnableDisableAndBack(t *testing.T) {
	cfg, service := winrmServiceGate(t)
	dir := winrmGateProject(t, cfg)

	run := func(name, fqcn string) string {
		t.Helper()
		rb := writeRunbook(t, dir, name, "id: "+name+"\nhosts: win-gate\ntasks:\n"+
			"  - name: "+fqcn+"\n    "+fqcn+":\n      name: "+service+"\n")
		out, err := runPleiades(t, dir, "run", rb, "--verbose")
		if err != nil {
			t.Fatalf("%s against %s: %v\n%s", fqcn, service, err, out)
		}
		return out
	}

	run("service_enable_baseline", "svc.windows.enable")

	disableOut := run("service_disable", "svc.windows.disable")
	if diffSide(disableOut, "before")["start_type"] != "Automatic" || diffSide(disableOut, "after")["start_type"] != "Disabled" {
		t.Errorf("svc.windows.disable's diff does not show an Automatic -> Disabled transition:\n%s", disableOut)
	}

	enableOut := run("service_enable_reverse", "svc.windows.enable")
	if diffSide(enableOut, "after")["start_type"] != "Automatic" {
		t.Errorf("the reversing svc.windows.enable did not leave %s reporting Automatic:\n%s", service, enableOut)
	}
}

// TestWinRMGate_FeatureRemoveInstallAndBack cycles a real DISM feature
// through install -> remove -> install (the reverse), ending enabled.
//
// Installing first establishes the fixed starting point (enabled) the
// same way TestWinRMGate_ServiceStartStopAndBack's opening start does,
// and is a documented no-op if the feature is already enabled.
func TestWinRMGate_FeatureRemoveInstallAndBack(t *testing.T) {
	cfg, feature := winrmFeatureGate(t)
	dir := winrmGateProject(t, cfg)

	run := func(name, fqcn string) string {
		t.Helper()
		rb := writeRunbook(t, dir, name, "id: "+name+"\nhosts: win-gate\ntasks:\n"+
			"  - name: "+fqcn+"\n    "+fqcn+":\n      name: "+feature+"\n")
		out, err := runPleiades(t, dir, "run", rb, "--verbose")
		if err != nil {
			t.Fatalf("%s against %s: %v\n%s", fqcn, feature, err, out)
		}
		return out
	}

	run("feature_install_baseline", "win.feature.install")

	removeOut := run("feature_remove", "win.feature.remove")
	if diffSide(removeOut, "before")["state"] != "Enabled" || diffSide(removeOut, "after")["state"] != "Disabled" {
		t.Errorf("win.feature.remove's diff does not show an Enabled -> Disabled transition:\n%s", removeOut)
	}

	installOut := run("feature_install_reverse", "win.feature.install")
	if diffSide(installOut, "after")["state"] != "Enabled" {
		t.Errorf("the reversing win.feature.install did not leave %s reporting Enabled:\n%s", feature, installOut)
	}
}

// diffSide reads one side ("before" or "after") of the first diff in a
// run --verbose output, as its keys and values. Reading the side rather
// than searching the whole output is what tells a transition from its
// reverse: both values appear in either, once in each side.
func diffSide(out, side string) map[string]string {
	fields := map[string]string{}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "diff:" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		sideAt := strings.Repeat(" ", indent+2) + side + ":"
		fieldAt := strings.Repeat(" ", indent+4)
		for j := i + 1; j < len(lines); j++ {
			if lines[j] != sideAt {
				continue
			}
			for _, field := range lines[j+1:] {
				if !strings.HasPrefix(field, fieldAt) {
					break
				}
				if strings.HasPrefix(field, fieldAt+" ") {
					continue
				}
				if key, value, ok := strings.Cut(strings.TrimSpace(field), ": "); ok {
					fields[key] = value
				}
			}
			break
		}
		break
	}
	return fields
}

// TestDiffSide runs everywhere, unlike the gates that use diffSide, on
// output svc.windows.stop printed against a real Windows Server 2025.
func TestDiffSide(t *testing.T) {
	out := `executing:
  tasks[0] [e0b13cb7-3007-4b20-b9e5-b71337705115]: changed
    diff:
      after:
        exists: true
        name: SysMain
        running: false
        start_type: Automatic
        status: Stopped
      before:
        dependents:
          - a nested line, skipped
        exists: true
        running: true
        status: Running
    inverse:
      description: Start SysMain, which this task stopped.
      fqcn: svc.windows.start
      params:
        name: SysMain
    name: SysMain
run complete`
	before, after := diffSide(out, "before"), diffSide(out, "after")
	if before["running"] != "true" || before["status"] != "Running" || before["exists"] != "true" {
		t.Errorf("before = %v", before)
	}
	if after["running"] != "false" || after["start_type"] != "Automatic" || len(after) != 5 {
		t.Errorf("after = %v", after)
	}
	if _, ok := before["name"]; ok {
		t.Errorf("before read past its own block: %v", before)
	}
	if got := diffSide("no diff here", "after"); len(got) != 0 {
		t.Errorf("an output with no diff gave %v", got)
	}
}
