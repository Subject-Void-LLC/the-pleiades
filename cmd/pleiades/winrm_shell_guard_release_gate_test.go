// The Release Gate for exec.winrm.shell's creates and removes guards against a
// real Windows host, run as a user runs it: a guarded check reads the guard
// and predicts the run without running anything, a run the guard does not
// stop changes the host, a run it does stop changes nothing, and an unguarded
// check is named unchecked. Every state is read beside the method, through a
// separate exec.winrm.shell call, never from the method's own report.
package main_test

import (
	"os"
	"testing"
)

// winrmGuardTarget returns the project and device the gate runs against,
// skipping when none is configured.
func winrmGuardTarget(t *testing.T) (dir, host string) {
	t.Helper()
	project, device := os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)
	if project != "" && device != "" {
		return project, device
	}
	cfg := winrmGateConfig{host: os.Getenv(envWinRMHost), user: os.Getenv(envWinRMUser), password: os.Getenv(envWinRMPassword)}
	if cfg.host != "" && cfg.user != "" && cfg.password != "" {
		return winrmGateProject(t, cfg), "win-gate"
	}
	t.Skipf("this Release Gate needs a real Windows host: set %s and %s (a project that manages it), or %s, %s and %s",
		envWinRMProject, envWinRMDevice, envWinRMHost, envWinRMUser, envWinRMPassword)
	return "", ""
}

func TestWinRMGate_ShellGuards(t *testing.T) {
	dir, host := winrmGuardTarget(t)
	const folder, stamp = `C:\pleiades-guard-gate`, `C:\pleiades-guard-gate\stamp`
	exists := func() string { return winrmRead(t, dir, host, "Test-Path -LiteralPath '"+stamp+"'") }
	winrmAdhoc(t, dir, host, "exec.winrm.shell", "shell=powershell",
		"command=New-Item -ItemType Directory -Force -Path '"+folder+"' | Out-Null; if (Test-Path -LiteralPath '"+stamp+"') { Remove-Item -Force -LiteralPath '"+stamp+"' }")
	t.Cleanup(func() {
		winrmAdhoc(t, dir, host, "exec.winrm.shell", "shell=powershell", "command=if (Test-Path -LiteralPath '"+folder+"') { Remove-Item -Recurse -Force -LiteralPath '"+folder+"' }")
	})
	makeStamp := []string{"exec.winrm.shell", "shell=powershell", "command=New-Item -ItemType File -Path '" + stamp + "' | Out-Null", "creates=" + stamp}
	unmake := []string{"exec.winrm.shell", "shell=powershell", "command=Remove-Item -Force -LiteralPath '" + stamp + "'", "removes=" + stamp}
	step := func(name string, call []string, check bool, wantStatus, wantExists string) {
		t.Helper()
		args := append([]string{"adhoc", host}, call...)
		if check {
			args = append(args, "--mode", "check")
		}
		rep, raw, code := runPleiadesJSONWithHome(t, dir, "", args...)
		if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != wantStatus {
			t.Fatalf("%s: exit %d, want status %s:\n%s", name, code, wantStatus, raw)
		}
		if got := exists(); got != wantExists {
			t.Fatalf("%s: the stamp's Test-Path is %s, want %s", name, got, wantExists)
		}
	}

	step("a check of a creates the host lacks", makeStamp, true, "would_change", "False")
	step("the run", makeStamp, false, "changed", "True")
	step("a check of a creates the host has", makeStamp, true, "ok", "True")
	step("a run the creates stops", makeStamp, false, "ok", "True")
	step("a check of a removes the host has", unmake, true, "would_change", "True")
	step("the removing run", unmake, false, "changed", "False")
	step("a run the removes stops", unmake, false, "ok", "False")

	rep, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", host, "exec.winrm.shell", "shell=powershell", "command=Write-Output hi", "--mode", "check")
	if code != 3 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "unchecked" {
		t.Fatalf("an unguarded check: exit %d, want 3 and unchecked:\n%s", code, raw)
	}
}
