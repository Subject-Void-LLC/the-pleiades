// Tests for the Windows machine's scripts and answers. The scripts run
// only on Windows, so what is checked here is what they say; a live run
// against the lab's win-check runs them (docs/design/os-checks.md).
package main

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"go.yaml.in/yaml/v3"
)

// TestReadyAnswer accepts only Windows with go, git and tar.exe, naming
// what is missing otherwise.
func TestReadyAnswer(t *testing.T) {
	for out, want := range map[string]string{
		"Win32NT\r\ngo\r\ngit\r\ntar\r\n": "",
		"Unix\ngo\ngit\ntar":              "is not a Windows machine: its platform is Unix",
		"Win32NT\nno-go\ngit\ntar":        "has no go on its PATH",
		"Win32NT\ngo\nno-git\ntar":        "has no git on its PATH",
		"Win32NT\ngo\ngit\nno-tar":        "has no tar.exe",
		"Win32NT\ngo":                     "answered the readiness check",
	} {
		err := readyAnswer("win-1", out)
		if (want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("readyAnswer(%q) = %v, want %q", out, err, want)
		}
	}
}

// TestScripts_TakeTheirValuesFromTheEnvironment: no script holds a value
// of the run's in its text; each reads the commit, the packages and the
// parallelism from exec.winrm.shell's env, so each is a constant.
func TestScripts_TakeTheirValuesFromTheEnvironment(t *testing.T) {
	for name, script := range map[string]string{"prepare": prepareScript, "unpack": unpackScript, "test": testScript, "stop": stopScript} {
		if !strings.Contains(script, "$env:PLEIADES_SHA") {
			t.Errorf("%s script does not read the commit from the environment:\n%s", name, script)
		}
	}
	if !strings.Contains(prepareScript, "Select-Object -Skip 2") {
		t.Errorf("the prepare script does not keep the newest %d trees:\n%s", oscheck.Kept, prepareScript)
	}
}

// TestTestScript: both passes run under cmd.exe, which joins their
// streams, the full one without the race detector, from a package list
// held to import paths; the run's own process is written down for the stop
// script and removed at the end; and the script exits with the tests'
// status.
func TestTestScript(t *testing.T) {
	for _, w := range []string{
		`if ($env:PLEIADES_PACKAGES -cnotmatch '^[A-Za-z0-9._/ -]+$')`,
		`Set-Content -LiteralPath $pidFile -Value $PID`,
		`& cmd.exe /d /c "go run ./tools/testgate -full -race=false -p $parallel $env:PLEIADES_PACKAGES 2>&1"`,
		`& cmd.exe /d /c "go run ./tools/testgate -repeat 3 -strict -p $parallel $env:PLEIADES_PACKAGES 2>&1"`,
		`Remove-Item -LiteralPath $pidFile`,
		`C:\mingit\cmd`,
	} {
		if !strings.Contains(testScript, w) {
			t.Errorf("test script does not hold %q:\n%s", w, testScript)
		}
	}
	if !strings.HasSuffix(testScript, "exit $code") {
		t.Errorf("test script does not end with the tests' status:\n%s", testScript)
	}
	if !strings.Contains(stopScript, "taskkill.exe /T /F /PID $id") || !strings.Contains(stopScript, `'^\d+$'`) {
		t.Errorf("stop script does not end the run's tree by a number it checked:\n%s", stopScript)
	}
}

// TestShellParams: the script, PowerShell, the timeout and the environment
// reach exec.winrm.shell as parameters adhoc reads, each value whole.
func TestShellParams(t *testing.T) {
	params := shellParams("Write-Output 'a: b'", map[string]string{"SHA": "abc", "PACKAGES": "a/b c/d"}, 90)
	want := []string{"shell=powershell", "command=Write-Output 'a: b'", "timeout:=90"}
	for i, w := range want {
		if params[i] != w {
			t.Errorf("param %d = %q, want %q", i, params[i], w)
		}
	}
	env, ok := strings.CutPrefix(params[len(params)-1], "env:=")
	var decoded map[string]string
	if !ok || yaml.Unmarshal([]byte(env), &decoded) != nil || decoded["PACKAGES"] != "a/b c/d" || decoded["SHA"] != "abc" {
		t.Errorf("env = %q", params[len(params)-1])
	}
	if got := shellParams("x", nil, 5); len(got) != 3 {
		t.Errorf("no env still sent one: %q", got)
	}
}

// TestCheck names wincheck's settings as the WinRM release gates name
// theirs, so one project serves both.
func TestCheck(t *testing.T) {
	if check.ProjectKey != "PLEIADES_WINRM_PROJECT" || check.DeviceKey != "PLEIADES_WINRM_DEVICE" || check.EnvFile != "tests/win/.env" {
		t.Errorf("check = %+v", check)
	}
}
