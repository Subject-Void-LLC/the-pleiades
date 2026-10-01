// doctor's checks against a machine the test describes, so each answer
// is proved both ways wherever the tests run. checks_live_test.go asks the
// real machine; this asks one that has everything, and one that has
// nothing.
package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// fakeMachine answers doctor's questions from its fields: a command it
// lists prints that output, a tool it lists is on PATH, and a variable it
// lists is set. Anything else fails, as a missing program does.
type fakeMachine struct {
	commands map[string]string
	tools    map[string]bool
	env      map[string]string
}

// install makes m the machine the checks ask until the test ends.
func (m fakeMachine) install(t *testing.T) {
	t.Helper()
	savedRun, savedLookPath, savedGetenv := run, lookPath, getenv
	t.Cleanup(func() { run, lookPath, getenv = savedRun, savedLookPath, savedGetenv })
	run = func(name string, args ...string) (string, error) {
		out, ok := m.commands[strings.Join(append([]string{name}, args...), " ")]
		if !ok {
			return "", errors.New("not on this machine")
		}
		return out, nil
	}
	lookPath = func(tool string) (string, error) {
		if !m.tools[tool] {
			return "", errors.New("not on PATH")
		}
		return "/fake/bin/" + tool, nil
	}
	getenv = func(key string) string { return m.env[key] }
}

// modinfo is what `go version -m` prints for a tool built at version.
func modinfo(tool, version string) string {
	return "/fake/bin/" + tool + ": go1.26.8\n\tpath\texample.com/" + tool + "\n\tmod\texample.com/" + tool + "\t" + version + "\th1:x=\n"
}

// allChecks runs every check, as main does.
func allChecks() []line {
	var lines []line
	lines = append(lines, checkGo()...)
	lines = append(lines, checkDocker())
	lines = append(lines, checkTools()...)
	lines = append(lines, checkHooks())
	return append(lines, checkOptional()...)
}

// statuses maps each line's subject to its status.
func statuses(lines []line) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		out[l.what] = l.status
	}
	return out
}

// pins reads the versions the real Makefile pins.
func pins(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	return pinnedTools(string(raw))
}

// TestChecks_AMachineWithEverythingIsOK proves every check says ok when
// the machine has what it asks about, at the pinned versions.
func TestChecks_AMachineWithEverythingIsOK(t *testing.T) {
	atRepoRoot(t)
	p := pins(t)
	fakeMachine{
		commands: map[string]string{
			"docker info --format {{.ServerVersion}}": "27.3.1",
			"go version -m /fake/bin/gosec":           modinfo("gosec", p["gosec"]),
			"go version -m /fake/bin/govulncheck":     modinfo("govulncheck", p["govulncheck"]),
			"go version -m /fake/bin/actionlint":      modinfo("actionlint", p["actionlint"]),
			"git config core.hooksPath":               ".githooks",
			"gopls version":                           "v0.20.0",
			"python3 -c import winrm":                 "",
		},
		tools: map[string]bool{"gosec": true, "govulncheck": true, "actionlint": true},
		env:   map[string]string{"PLEIADES_WINRM_HOST": "x", "PLEIADES_WINRM_PROJECT": "x", "PLEIADES_SNOW_INSTANCE": "x"},
	}.install(t)

	for _, l := range allChecks() {
		if l.status != "ok" {
			t.Errorf("%s = %s (%s), want ok on a machine that has it", l.what, l.status, l.detail)
		}
	}
}

// TestChecks_AMachineWithNothingSaysWhatToDo proves each required check
// says fix and each optional one says info when the machine lacks it, and
// that every fix line names what to run.
func TestChecks_AMachineWithNothingSaysWhatToDo(t *testing.T) {
	atRepoRoot(t)
	fakeMachine{}.install(t)

	lines := allChecks()
	got := statuses(lines)
	want := map[string]string{
		"Docker daemon": "fix", "gosec": "fix", "govulncheck": "fix", "actionlint": "fix", "git hooks": "fix",
		"gopls": "info", "pywinrm": "info", "WinRM gates": "info", "lab project gates": "info", "ServiceNow gate": "info",
	}
	for what, status := range want {
		if got[what] != status {
			t.Errorf("%s = %q, want %q on a machine without it", what, got[what], status)
		}
	}
	for _, l := range lines {
		if l.status == "fix" && !strings.Contains(l.detail, "make ") && !strings.Contains(l.detail, "start Docker") {
			t.Errorf("fix line %q does not say what to run", l.detail)
		}
	}
}

// TestCheckTools_AWrongOrUnknownVersionIsAFix proves a scanner on PATH is
// not enough: one at another version than the pin, or one whose version
// cannot be read, is a fix naming the pin.
func TestCheckTools_AWrongOrUnknownVersionIsAFix(t *testing.T) {
	atRepoRoot(t)
	p := pins(t)
	fakeMachine{
		commands: map[string]string{
			"go version -m /fake/bin/gosec":       modinfo("gosec", "v0.0.1"),
			"go version -m /fake/bin/govulncheck": "not a Go binary",
			"go version -m /fake/bin/actionlint":  modinfo("actionlint", p["actionlint"]),
		},
		tools: map[string]bool{"gosec": true, "govulncheck": true, "actionlint": true},
	}.install(t)

	byTool := map[string]line{}
	for _, l := range checkTools() {
		byTool[l.what] = l
	}
	if l := byTool["gosec"]; l.status != "fix" || !strings.Contains(l.detail, "v0.0.1, pinned "+p["gosec"]) {
		t.Errorf("gosec at another version = %+v, want a fix naming both versions", l)
	}
	if l := byTool["govulncheck"]; l.status != "fix" || !strings.Contains(l.detail, "unknown version") {
		t.Errorf("govulncheck of unreadable version = %+v, want a fix saying the version is unknown", l)
	}
	if l := byTool["actionlint"]; l.status != "ok" {
		t.Errorf("actionlint at its pin = %+v, want ok", l)
	}
}

// TestCheckGo_AnOlderToolchainIsAFix proves a toolchain older than go.mod
// asks for is a fix naming both, rather than a build that fails later
// with an error about something else.
func TestCheckGo_AnOlderToolchainIsAFix(t *testing.T) {
	atRepoRoot(t)
	saved := goVersion
	t.Cleanup(func() { goVersion = saved })
	goVersion = func() string { return "go1.20" }
	got := checkGo()
	if len(got) != 1 || got[0].status != "fix" || !strings.Contains(got[0].detail, "go1.20, and go.mod asks for") {
		t.Fatalf("checkGo on go1.20 = %+v, want a fix naming both toolchains", got)
	}
}
