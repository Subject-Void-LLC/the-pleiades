// Tests for the Windows machine's scripts and answers. The scripts run
// only on Windows, so what is checked here is what they say; a live run
// against the lab's win-check runs them (docs/design/os-checks.md).
package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"go.yaml.in/yaml/v3"
)

// TestReadyAnswer accepts only Windows with go, git and tar.exe and
// oscheck.MinFreeGB free after making room, naming what is wrong otherwise,
// and says what room it made when the machine started short.
func TestReadyAnswer(t *testing.T) {
	for out, want := range map[string]struct{ err, note string }{
		"Win32NT\r\ngo\r\ngit\r\ntar\r\n21\r\n21\r\n": {},
		"Win32NT\ngo\ngit\ntar\n2\n11":                {note: `had 2 GB free, under the 4 GB a run needs, so this check removed its own older trees and build cache (%USERPROFILE%\.pleiades-wincheck\.gocache): 11 GB free now`},
		"Unix\ngo\ngit\ntar\n21\n21":                  {err: "is not a Windows machine: its platform is Unix"},
		"Win32NT\nno-go\ngit\ntar\n21\n21":            {err: "has no go on its PATH"},
		"Win32NT\ngo\nno-git\ntar\n21\n21":            {err: "has no git on its PATH"},
		"Win32NT\ngo\ngit\nno-tar\n21\n21":            {err: "has no tar.exe"},
		"Win32NT\ngo\ngit\ntar\n3\n3":                 {err: "has 3 GB free where its tests unpack and build, under the 4 GB", note: "had 3 GB free"},
		"Win32NT\ngo\ngit\ntar\n21":                   {err: "answered the readiness check"},
		"Win32NT\ngo":                                 {err: "answered the readiness check"},
	} {
		note, err := readyAnswer("win-1", out, false, "")
		if (want.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), want.err) {
			t.Errorf("readyAnswer(%q) = %v, want %q", out, err, want.err)
		}
		if (want.note == "") != (note == "") || !strings.Contains(note, want.note) {
			t.Errorf("readyAnswer(%q) said %q, want %q", out, note, want.note)
		}
	}
}

// TestDedicatedReadyScript: a lab VM kept for checks also has its login's Go
// build cache cleared, go test's leftover folders in its TEMP removed, and
// its module cache cleared while short, all before the check's own room is
// made; a machine not kept for checks never does any of it.
func TestDedicatedReadyScript(t *testing.T) {
	for _, w := range []string{
		"Remove-Item Env:GOCACHE -ErrorAction SilentlyContinue",
		"& go.exe clean -cache 2>&1 | Out-Null",
		"foreach ($d in @($env:TEMP) + @(if ($env:PLEIADES_DATA) { Join-Path $root 'tmp' })) { foreach ($f in 'go-build*', 'Test*') { Get-ChildItem -LiteralPath $d -Directory -Filter $f",
		"if ([int](Get-FreeGB) -lt 4 -and (Get-Command go.exe -ErrorAction SilentlyContinue)) { & go.exe clean -modcache",
		"if ([int](Get-FreeGB) -lt 4 -and $env:PLEIADES_DATA) { Remove-Item -LiteralPath (Join-Path $root 'gomod')",
	} {
		if !strings.Contains(dedicatedReadyScript, w) {
			t.Errorf("the dedicated ready script does not hold %q:\n%s", w, dedicatedReadyScript)
		}
		if strings.Contains(readyScript, w) {
			t.Errorf("the ordinary ready script holds %q", w)
		}
	}
	if strings.Index(dedicatedReadyScript, "clean -cache") > strings.Index(dedicatedReadyScript, pruneLine) {
		t.Error("the login's caches are cleared after the check's own room is made, so its own cache can go first")
	}
	var leg oscheck.Leg = windows{}
	d, ok := leg.(oscheck.Dedicating)
	if !ok || !d.Dedicated().(windows).dedicated {
		t.Error("windows does not become a dedicated machine when the check runs it as a box")
	}
	note, err := readyAnswer("win-1", "Win32NT\ngo\ngit\ntar\n3\n3", true, "")
	if err == nil || !strings.Contains(err.Error(), "the login's Go build and module caches") || !strings.Contains(err.Error(), "outside Go's caches") || note == "" {
		t.Errorf("a dedicated machine still short says %v (note %q)", err, note)
	}
	if _, err := readyAnswer("win-1", "Win32NT\ngo\ngit\ntar\n3\n3", true, `D:\pleiades`); err == nil || !strings.Contains(err.Error(), `D:\pleiades\.pleiades-wincheck\.gocache`) {
		t.Errorf("a machine with a data directory names the profile's cache: %v", err)
	}
}

// TestNewLegFor_DataDirectory: a data directory named in the settings
// reaches every script as $env:PLEIADES_DATA, and one that is not an
// absolute drive path of plain characters is refused by name.
func TestNewLegFor_DataDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	leg, err := newLegFor("wincheck", "/p", "win-1", `D:\pleiades`)
	if err != nil {
		t.Fatal(err)
	}
	w := leg.(windows)
	if w.data != `D:\pleiades` {
		t.Errorf("data = %q", w.data)
	}
	for _, bad := range []string{`D:`, `D:\`, `D:\a b`, `D:\a"b`, "/data", `D:\pleiades\`} {
		if _, err := newLegFor("wincheck", "/p", "win-1", bad); err == nil || !strings.Contains(err.Error(), "not an absolute drive path") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	for name, script := range map[string]string{"ready": readyScript, "dedicated ready": dedicatedReadyScript, "prepare": prepareScript, "unpack": unpackScript, "test": testScript, "stop": stopScript, "records": recordsScript} {
		if !strings.Contains(script, rootLine) {
			t.Errorf("the %s script does not read the data directory from the environment:\n%s", name, script)
		}
		if strings.Contains(strings.ReplaceAll(script, rootLine, ""), "$env:USERPROFILE") {
			t.Errorf("the %s script still reads the profile beside the root:\n%s", name, script)
		}
	}
	for _, w := range []string{
		"$env:GOMODCACHE = Join-Path $root 'gomod'",
		"$env:TEMP = Join-Path $root 'tmp'",
		"$env:TMP = $env:TEMP",
	} {
		if !strings.Contains(testScript, w) {
			t.Errorf("the test script does not hold %q:\n%s", w, testScript)
		}
	}
}

// TestReadyScript_MakesRoomOnlyFromItsOwn: the readiness script prunes the
// trees it keeps (passing over the build cache by name, since a leading dot
// hides nothing on Windows) and removes the build cache only while space is
// short, only under its own directory; and every run builds into that cache.
func TestReadyScript_MakesRoomOnlyFromItsOwn(t *testing.T) {
	for _, w := range []string{
		`$base = Join-Path $root '.pleiades-wincheck'`,
		`Where-Object { $_.Name -ne '.gocache' } | Sort-Object LastWriteTime -Descending | Select-Object -Skip 2 | Remove-Item`,
		`if ([int](Get-FreeGB) -lt 4) { Remove-Item -LiteralPath (Join-Path $base '.gocache') -Recurse`,
	} {
		if !strings.Contains(readyScript, w) {
			t.Errorf("the ready script does not hold %q:\n%s", w, readyScript)
		}
	}
	if strings.Count(readyScript, "Remove-Item") != 2 || strings.Contains(readyScript, "go-build") {
		t.Errorf("the ready script removes something that is not its own:\n%s", readyScript)
	}
	if !strings.Contains(prepareScript, pruneLine) {
		t.Errorf("the prepare script's prune would remove the build cache:\n%s", prepareScript)
	}
	if !strings.Contains(testScript, `$env:GOCACHE = Join-Path (Join-Path $root '.pleiades-wincheck') '.gocache'`) {
		t.Errorf("the test script does not build into the check's own cache:\n%s", testScript)
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
	// The clock is set from its time source, and from this machine's time,
	// read from the environment, only when that fails.
	if !strings.Contains(clockScript, "w32tm /resync /force") || !strings.Contains(clockScript, "$env:PLEIADES_UTC") ||
		strings.Index(clockScript, "w32tm") > strings.Index(clockScript, "Set-Date") {
		t.Errorf("the clock script:\n%s", clockScript)
	}
}

// TestTestScript: both passes run under cmd.exe, which joins their
// streams, the full one with the race detector only when the machine has
// gcc (which turns cgo on) and is amd64, and saying which, from a package list held to
// import paths; the run's own process is written down for the stop script
// and removed at the end; and the script exits with the tests' status.
func TestTestScript(t *testing.T) {
	for _, w := range []string{
		`if ($env:PLEIADES_PACKAGES -cnotmatch '^[A-Za-z0-9._/ -]+$')`,
		`Set-Content -LiteralPath $pidFile -Value $PID`,
		`Remove-Item -LiteralPath (Join-Path $tree $f) -Force`,
		`$race = 'false'`,
		`if (-not (Get-Command gcc.exe -ErrorAction SilentlyContinue)) { 'wincheck: no gcc, so cgo and the race detector are off' }`,
		`elseif ((& go env GOARCH) -eq 'amd64') { $race = 'true'; 'wincheck: gcc found, so cgo and the race detector are on' }`,
		`else { 'wincheck: gcc found, so cgo is on; Go has no race detector for this architecture' }`,
		`Get-ChildItem -Directory -Path 'C:\llvm-mingw-*'`,
		`& cmd.exe /d /c "go run ./tools/testgate -full -race=$race -report-out .pleiades-record-full.json -p $parallel -packages-file .pleiades-packages.txt 2>&1"`,
		`C:\mingw64\bin`,
		`& cmd.exe /d /c "go run ./tools/testgate -repeat 3 -strict -report-out .pleiades-record-repeat.json -p $parallel -packages-file .pleiades-packages.txt 2>&1"`,
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

// TestRecordFiles: the records the test script's passes write are the
// files oscheck names, and the records script reads exactly those, as
// UTF-8, from sha's tree.
func TestRecordFiles(t *testing.T) {
	if !slices.Equal([]string{recordFull, recordRepeat}, oscheck.RecordFiles) {
		t.Errorf("wincheck writes %s and %s, oscheck names %v", recordFull, recordRepeat, oscheck.RecordFiles)
	}
	for _, w := range []string{"$env:PLEIADES_SHA", recordFull, recordRepeat, "-Raw -Encoding UTF8"} {
		if !strings.Contains(recordsScript, w) {
			t.Errorf("records script does not hold %q:\n%s", w, recordsScript)
		}
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

// TestParams_EveryScriptGetsTheDataDirectory: a machine with a data
// directory sends it as DATA with every script's env, beside the values
// given, and leaves a given env map as it was; a machine without one sends
// nothing more. The test script runs through the same params as the rest.
func TestParams_EveryScriptGetsTheDataDirectory(t *testing.T) {
	w := windows{data: `D:\pleiades`}
	given := map[string]string{"SHA": "abc"}
	params := w.params(testScript, given, 9)
	env, ok := strings.CutPrefix(params[len(params)-1], "env:=")
	var decoded map[string]string
	if !ok || yaml.Unmarshal([]byte(env), &decoded) != nil || decoded["DATA"] != `D:\pleiades` || decoded["SHA"] != "abc" {
		t.Errorf("env = %q", params[len(params)-1])
	}
	if _, leaked := given["DATA"]; leaked {
		t.Error("the given env map was changed")
	}
	if got := w.params("x", nil, 5); len(got) != 4 {
		t.Errorf("no env given still gets DATA: %q", got)
	}
	if got := (windows{}).params("x", nil, 5); len(got) != 3 {
		t.Errorf("a machine with no data directory sent one: %q", got)
	}
}

// TestCheck names wincheck's settings as the WinRM release gates name
// theirs, so one project serves both.
func TestCheck(t *testing.T) {
	if check.ProjectKey != "PLEIADES_WINRM_PROJECT" || check.DeviceKey != "PLEIADES_WINRM_DEVICE" || check.DataKey != "PLEIADES_WINRM_DATA" || check.EnvFile != "tests/win/.env" {
		t.Errorf("check = %+v", check)
	}
	if checkARM.DataKey != "PLEIADES_WINARM_DATA" {
		t.Errorf("checkARM = %+v", checkARM)
	}
}
