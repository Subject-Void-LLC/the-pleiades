// The Windows machine as a lab device, over WinRM: checked and run through
// exec.winrm.shell, sent its tree with win.file.copy and unpacked by
// tar.exe. Every value reaches PowerShell through exec.winrm.shell's env
// ($env:PLEIADES_<NAME>), never inside a script's text, so each script is a
// constant.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// winDir is where trees are unpacked, under the login's profile, one
// directory per commit; the newest oscheck.Kept stay.
const winDir = ".pleiades-wincheck"

// winPath is put before the PATH a WinRM session starts with, which a
// machine-wide install of go, git or gcc may not be on yet: where the lab's
// win-check unpacked go, MinGit and WinLibs' GCC, then where go.dev's and
// Git for Windows' installers put them.
const winPath = `C:\go\bin;C:\mingit\cmd;C:\mingw64\bin;C:\Program Files\Go\bin;C:\Program Files\Git\cmd`

// files is the directory, relative to the project, the archive is written
// in to be sent; each archive is deleted once sent.
const files = ".wincheck"

// windows is the real machine.
type windows struct {
	oscheck.Lab
	// dedicated says the machine is a lab VM kept for checks, whose login's
	// Go caches the readiness step clears before every run (Dedicated).
	dedicated bool
	// data is the directory every run's variable data goes under (its
	// trees, its Go build and module caches and its TEMP), or "" for the
	// login's profile and its own caches: the lab's win-check mounts its
	// second disk as D: and names D:\pleiades.
	data string
}

// Dedicated is the machine as a lab VM a check starts and puts back
// (oscheck.Dedicating): win-check on VirtualBox, win11-arm on Fusion.
func (w windows) Dedicated() oscheck.Leg {
	w.dedicated = true
	return w
}

// newLeg is device in project, driven by the pleiades tool builds under its
// own name: wincheck's and winarmcheck's are separate builds, so one check
// never runs the other's binary (an older one could not read a project whose
// records name capabilities it predates).
func newLeg(tool string) func(project, device, data string) (oscheck.Leg, error) {
	return func(project, device, data string) (oscheck.Leg, error) {
		return newLegFor(tool, project, device, data)
	}
}

// newLegFor is newLeg's machine, its variable data under data when one is
// named.
func newLegFor(tool, project, device, data string) (oscheck.Leg, error) {
	if err := oscheck.CheckWindowsDataDir("the data directory", data); err != nil {
		return nil, err
	}
	lab, err := oscheck.NewLab(tool, project, device)
	if err != nil {
		return nil, err
	}
	return windows{Lab: lab, data: data}, nil
}

// shell runs script through exec.winrm.shell, under PowerShell, with env's
// values as $env:PLEIADES_<NAME> (and the data directory as
// $env:PLEIADES_DATA when the machine has one), returning its standard
// output.
func (w windows) shell(ctx context.Context, script string, env map[string]string, seconds int) (string, error) {
	return w.Pleiades.Adhoc(ctx, "exec.winrm.shell", w.params(script, env, seconds)...)
}

// params are exec.winrm.shell's parameters for script with env, the data
// directory added as DATA when the machine has one: every script the
// machine runs gets them this way, the test script included, since a
// script given the data directory for its tree and one not given it look
// in different places (the test script looked in the profile for a tree the
// prepare script had put on D:, 2026-10-29).
func (w windows) params(script string, env map[string]string, seconds int) []string {
	if w.data != "" {
		with := make(map[string]string, len(env)+1)
		for k, v := range env {
			with[k] = v
		}
		with["DATA"] = w.data
		env = with
	}
	return shellParams(script, env, seconds)
}

// shellParams are exec.winrm.shell's parameters for script, under
// PowerShell, with env's values as $env:PLEIADES_<NAME> and seconds as its
// own timeout.
func shellParams(script string, env map[string]string, seconds int) []string {
	params := []string{"shell=powershell", "command=" + script, "timeout:=" + strconv.Itoa(seconds)}
	if len(env) > 0 {
		params = append(params, "env:="+oscheck.MapYAML(env))
	}
	return params
}

// Ready builds pleiades from this tree, asks the machine whether it runs
// Windows with go and tar.exe and the space free on its profile's drive,
// making room first (readyScript), and sets its clock (clockScript). Room it
// made is said on standard output.
func (w windows) Ready() error {
	if err := w.Pleiades.Build(); err != nil {
		return err
	}
	script := readyScript
	if w.dedicated {
		script = dedicatedReadyScript
	}
	out, err := w.shell(context.Background(), script, nil, 300)
	if err != nil {
		return fmt.Errorf("checking %s: %v", w.Target, err)
	}
	note, err := readyAnswer(w.Target, out, w.dedicated, w.data)
	if note != "" {
		fmt.Printf("wincheck: %s\n", note)
	}
	if err != nil {
		return err
	}
	utc := time.Now().UTC().Format(time.RFC3339)
	if _, err := w.shell(context.Background(), clockScript, map[string]string{"UTC": utc}, 60); err != nil {
		return fmt.Errorf("setting %s's clock: %v", w.Target, err)
	}
	return nil
}

// rootLine is the line every script starts with: the directory the trees
// are under, the data directory when the machine has one ($env:PLEIADES_DATA)
// and the login's profile otherwise.
const rootLine = `$root = if ($env:PLEIADES_DATA) { $env:PLEIADES_DATA } else { $env:USERPROFILE }
`

// readyScript prints the platform, then go, git and tar, or no-go, no-git
// and no-tar when one is not on the PATH, then the whole gigabytes free on
// the drive holding the data directory (the login's profile without one),
// where the trees and the check's build cache are, before and after making
// room, one line each. Making room removes only what is this check's own,
// under winDir: every tree but the newest Kept-1, and, while the space free
// is still under MinFreeGB, oscheck.CacheDir, which the prune passes over by
// name (a leading dot hides nothing on Windows).
var readyScript = readyScriptWith("")

// dedicatedReadyScript is readyScript for a lab VM kept for checks: before
// anything else it clears the login's own Go build cache (GOCACHE removed
// names its default, %LocalAppData%\go-build), the go-build work folders and
// Test* temporary folders a stopped go test left in its TEMP (and in the
// data directory's tmp, where a run's TEMP goes when the machine has one),
// and, while still under MinFreeGB, its module cache (and the data
// directory's gomod), which the next build downloads again (owner,
// 2026-10-27).
var dedicatedReadyScript = readyScriptWith(`Remove-Item Env:GOCACHE -ErrorAction SilentlyContinue
if (Get-Command go.exe -ErrorAction SilentlyContinue) { & go.exe clean -cache 2>&1 | Out-Null }
foreach ($d in @($env:TEMP) + @(if ($env:PLEIADES_DATA) { Join-Path $root 'tmp' })) { foreach ($f in 'go-build*', 'Test*') { Get-ChildItem -LiteralPath $d -Directory -Filter $f -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue } }
if ([int](Get-FreeGB) -lt ` + strconv.Itoa(oscheck.MinFreeGB) + ` -and (Get-Command go.exe -ErrorAction SilentlyContinue)) { & go.exe clean -modcache 2>&1 | Out-Null }
if ([int](Get-FreeGB) -lt ` + strconv.Itoa(oscheck.MinFreeGB) + ` -and $env:PLEIADES_DATA) { Remove-Item -LiteralPath (Join-Path $root 'gomod') -Recurse -Force -ErrorAction SilentlyContinue }
`)

// readyScriptWith is the readiness script with first run after the space
// free is first measured, before the check's own room is made.
func readyScriptWith(first string) string {
	return `$env:Path = '` + winPath + `;' + $env:Path
[string][Environment]::OSVersion.Platform
foreach ($c in 'go', 'git', 'tar') { if (Get-Command "$c.exe" -ErrorAction SilentlyContinue) { $c } else { "no-$c" } }
` + rootLine + `function Get-FreeGB { [string][math]::Floor((Get-Item $root).PSDrive.Free / 1GB) }
$base = Join-Path $root '` + winDir + `'
$before = Get-FreeGB
` + first + `if (Test-Path -LiteralPath $base) {
	` + pruneLine + `
	if ([int](Get-FreeGB) -lt ` + strconv.Itoa(oscheck.MinFreeGB) + `) { Remove-Item -LiteralPath (Join-Path $base '` + oscheck.CacheDir + `') -Recurse -Force -ErrorAction SilentlyContinue }
}
$before
Get-FreeGB`
}

// pruneLine removes every tree under $base but the newest Kept-1, passing
// over oscheck.CacheDir, which is not a tree.
var pruneLine = `Get-ChildItem -LiteralPath $base -Directory | Where-Object { $_.Name -ne '` + oscheck.CacheDir + `' } | Sort-Object LastWriteTime -Descending | Select-Object -Skip ` + strconv.Itoa(oscheck.Kept-1) + ` | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue`

// clockScript sets the machine's clock: from its time source (w32tm), or,
// when that cannot reach one, to $env:PLEIADES_UTC, this machine's time when
// the check began. A lab box resumed from its saved state keeps the time it
// was saved at, minutes or hours slow, until something sets it, and nothing
// in a resume does (win-check was 567 s slow after three resumes, 2026-10-20).
const clockScript = `w32tm /resync /force | Out-Null
if ($LASTEXITCODE -ne 0) {
  Set-Date -Date ([DateTime]::Parse($env:PLEIADES_UTC, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AdjustToUniversal).ToLocalTime()) | Out-Null
}`

// readyAnswer checks readyScript's output, returning what room it made;
// dedicated says the script was dedicatedReadyScript, and data is the data
// directory the trees are under, or "" for the profile.
func readyAnswer(target, out string, dedicated bool, data string) (string, error) {
	root := `%USERPROFILE%`
	if data != "" {
		root = data
	}
	lines := strings.Fields(out)
	switch {
	case len(lines) != 6:
		return "", fmt.Errorf("%s answered the readiness check with %q", target, out)
	case lines[0] != "Win32NT":
		return "", fmt.Errorf("%s is not a Windows machine: its platform is %s", target, lines[0])
	case lines[1] != "go":
		return "", fmt.Errorf("%s has no go on its PATH (looked in its PATH and %s): install it from https://go.dev/dl, or as the lab's win-check has it (examples/virtualbox_lab)", target, winPath)
	case lines[2] != "git":
		return "", fmt.Errorf("%s has no git on its PATH (looked in its PATH and %s), which the tests that make a throwaway repository need: install Git for Windows, or MinGit as the lab's win-check has it (examples/virtualbox_lab)", target, winPath)
	case lines[3] != "tar":
		return "", fmt.Errorf("%s has no tar.exe, which unpacks the tree; Windows has had it since Windows 10 1803 and Server 2019", target)
	}
	if dedicated {
		return oscheck.Room(target, lines[4], lines[5], root+`\`+winDir+`\`+oscheck.CacheDir+`; and, on this machine kept for checks, the login's Go build and module caches and its TEMP\go-build folders`,
			"what is left is outside Go's caches")
	}
	return oscheck.Room(target, lines[4], lines[5], root+`\`+winDir+`\`+oscheck.CacheDir,
		`the login's own Go cache is %LocalAppData%\go-build`)
}

// Ship sends commit sha's tree: exec.winrm.shell removes all but the
// newest trees and answers the login's profile, git archive writes the
// tree into the project, win.file.copy sends it, and tar.exe unpacks it
// into a directory of its own and the archive is deleted.
func (w windows) Ship(sha string) error {
	ctx := context.Background()
	env := map[string]string{"SHA": sha}
	out, err := w.shell(ctx, prepareScript, env, 300)
	if err != nil {
		return fmt.Errorf("preparing %s: %v", w.Target, err)
	}
	profile := strings.TrimSpace(out)
	if len(profile) < 3 || profile[1:3] != `:\` {
		return fmt.Errorf("%s named %q as its profile directory", w.Target, profile)
	}
	src := path.Join(files, sha+".tar.gz")
	local := filepath.Join(w.Pleiades.Project, filepath.FromSlash(src))
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	defer func() { _ = os.Remove(local) }()
	if out, err := exec.Command("git", "archive", "--format=tar.gz", "-o", local, sha).CombinedOutput(); err != nil { // #nosec G204 -- a commit rev-parse resolved
		return fmt.Errorf("git archive: %v: %s", err, strings.TrimSpace(string(out)))
	}
	archive := profile + `\` + winDir + `\` + sha + ".tar.gz"
	if _, err := w.Pleiades.Adhoc(ctx, "win.file.copy", "src="+src, "path="+archive); err != nil {
		return err
	}
	if _, err := w.shell(ctx, unpackScript, env, 600); err != nil {
		return fmt.Errorf("unpacking the tree on %s: %v", w.Target, err)
	}
	return nil
}

// treeLines are the lines every script after readyScript starts with:
// stop at the first error, and name the directory the trees are under, the
// trees' directory and sha's tree.
const treeLines = `$ErrorActionPreference = 'Stop'
` + rootLine + `$base = Join-Path $root '` + winDir + `'
$tree = Join-Path $base $env:PLEIADES_SHA
`

// prepareScript makes the trees' directory, removes all but the newest
// Kept-1 trees (pruneLine, which keeps the build cache) and any earlier copy
// of sha's, and prints the directory the trees are under.
var prepareScript = treeLines + `New-Item -ItemType Directory -Force -Path $base | Out-Null
` + pruneLine + `
foreach ($old in @($tree, ($tree + '.tar.gz'))) { if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Recurse -Force } }
$root`

// unpackScript unpacks sha's archive into its tree, which tar.exe will not
// make itself, and deletes the archive.
const unpackScript = treeLines + `$archive = $tree + '.tar.gz'
New-Item -ItemType Directory -Force -Path $tree | Out-Null
& tar.exe -xzf $archive -C $tree
if ($LASTEXITCODE -ne 0) { throw "tar.exe exited $LASTEXITCODE" }
Remove-Item -LiteralPath $archive -Force`

// Test runs the packages on sha's tree through exec.winrm.shell (see
// testScript), stopping them with stopScript when ctx ends first.
func (w windows) Test(ctx context.Context, sha string, parallel int, packages []string, stdout io.Writer) (bool, error) {
	if !oscheck.FileExists(w.Pleiades.Binary) {
		return false, fmt.Errorf("this worktree has no pleiades for %s yet; run -phase prepare first", w.Target)
	}
	fmt.Fprintln(stdout, "wincheck: running on the Windows machine; Pleiades returns the output when the run ends, since it does not stream it yet")
	env := map[string]string{"SHA": sha, "PARALLEL": strconv.Itoa(parallel), "PACKAGES": strings.Join(packages, " ")}
	run := func(ctx context.Context) (oscheck.Outcome, error) {
		return w.Pleiades.AdhocOutcome(ctx, "exec.winrm.shell", w.params(testScript, env, oscheck.TestTimeout)...)
	}
	stop := func(ctx context.Context) error {
		_, err := w.shell(ctx, stopScript, map[string]string{"SHA": sha}, 60)
		return err
	}
	return oscheck.RunTests(ctx, w.Target, run, stop, stdout)
}

// pidFile is where, in the unpacked tree, testScript writes the process
// that runs the tests.
const pidFile = ".wincheck.pid"

// testScript is what the machine runs in the unpacked tree: pr-check's two
// passes over the container-free packages, the full pass and three strict
// repeats, building into the check's own oscheck.CacheDir, and it exits with the tests' status. A C compiler (gcc.exe on
// the PATH: WinLibs' GCC on x64, or llvm-mingw's, whose newest C:\llvm-mingw-*
// folder the script puts on the PATH, on ARM) turns cgo on, so go-sqlite3's
// tests run; the full pass also uses the race detector then, where Go has one
// for Windows, which is amd64 alone. Without a compiler Go builds with cgo
// off and testgate reports go-sqlite3's stub as a skip that needs cgo; a suite
// whose stubbed fixture is shared (internal/ui/resources) fails there, so the
// ARM VM has llvm-mingw. The script says which before the passes begin, and a failed task
// reports the tests' output. Each pass runs under
// cmd.exe, which joins its standard error to its standard output so they
// read in order: PowerShell would make each stderr line an error record,
// and print an empty one as the record's type name. The command line
// cmd.exe parses holds only the package list, refused here unless it is
// import paths and spaces (oscheck.Options.Check refused anything else
// before it was sent), and a number. The script's own process ID is written
// to pidFile first, so stopScript can end it and everything under it, and
// the file is removed at the end, so a later stop cannot reach a process
// that reused the number.
var testScript = `$ErrorActionPreference = 'Continue'
$env:Path = '` + winPath + `;' + $env:Path
` + rootLine + `$env:GOCACHE = Join-Path (Join-Path $root '` + winDir + `') '` + oscheck.CacheDir + `'
if ($env:PLEIADES_DATA) {
	$env:GOMODCACHE = Join-Path $root 'gomod'
	$env:TEMP = Join-Path $root 'tmp'
	$env:TMP = $env:TEMP
	New-Item -ItemType Directory -Force -Path $env:GOMODCACHE, $env:TEMP | Out-Null
}
$tree = Join-Path (Join-Path $root '` + winDir + `') $env:PLEIADES_SHA
if (-not (Test-Path -LiteralPath $tree)) { "no tree at $tree; run -phase prepare first"; exit 2 }
if ($env:PLEIADES_PACKAGES -cnotmatch '^[A-Za-z0-9._/ -]+$') { 'the package list is not import paths and spaces'; exit 2 }
Set-Location -LiteralPath $tree
$pidFile = Join-Path $tree '` + pidFile + `'
Set-Content -LiteralPath $pidFile -Value $PID
foreach ($f in '` + recordFull + `', '` + recordRepeat + `') { Remove-Item -LiteralPath (Join-Path $tree $f) -Force -ErrorAction SilentlyContinue }
$parallel = [int]$env:PLEIADES_PARALLEL
# The list goes in a file: cmd.exe refuses a command line past 8191
# characters, which 143 packages passed (2026-10-25), failing the run in a
# second with nothing to show for it.
$packages = Join-Path $tree '` + packagesFile + `'
Set-Content -LiteralPath $packages -Value $env:PLEIADES_PACKAGES -Encoding ascii
$llvm = Get-ChildItem -Directory -Path 'C:\llvm-mingw-*' -ErrorAction SilentlyContinue | Sort-Object Name | Select-Object -Last 1
if ($llvm) { $env:Path = (Join-Path $llvm.FullName 'bin') + ';' + $env:Path }
$race = 'false'
if (-not (Get-Command gcc.exe -ErrorAction SilentlyContinue)) { 'wincheck: no gcc, so cgo and the race detector are off' }
elseif ((& go env GOARCH) -eq 'amd64') { $race = 'true'; 'wincheck: gcc found, so cgo and the race detector are on' }
else { 'wincheck: gcc found, so cgo is on; Go has no race detector for this architecture' }
& cmd.exe /d /c "go run ./tools/testgate -full -race=$race -report-out ` + recordFull + ` -p $parallel -packages-file ` + packagesFile + ` 2>&1"
$code = $LASTEXITCODE
if ($code -eq 0) {
	& cmd.exe /d /c "go run ./tools/testgate -repeat 3 -strict -report-out ` + recordRepeat + ` -p $parallel -packages-file ` + packagesFile + ` 2>&1"
	$code = $LASTEXITCODE
}
Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
exit $code`

// recordFull and recordRepeat are oscheck.RecordFiles, the files testScript's
// two passes write their records to. They are constants so the scripts
// that name them are.
const (
	recordFull   = ".pleiades-record-full.json"
	recordRepeat = ".pleiades-record-repeat.json"
)

// packagesFile is where testScript writes the packages for testgate to read
// (-packages-file), in the tree it runs in.
const packagesFile = ".pleiades-packages.txt"

// Records reads back the testgate records testScript's passes wrote in
// sha's tree, those that exist.
func (w windows) Records(ctx context.Context, sha string) ([]flakegate.Run, error) {
	out, err := w.shell(ctx, recordsScript, map[string]string{"SHA": sha}, 120)
	if err != nil {
		return nil, err
	}
	return oscheck.ParseRecords(out)
}

// recordsScript prints each record file in sha's tree that exists, read as
// UTF-8, which is how testgate wrote it: Windows PowerShell 5.1 would
// otherwise read a file with no byte order mark in the system's code page.
const recordsScript = rootLine + `$tree = Join-Path (Join-Path $root '` + winDir + `') $env:PLEIADES_SHA
foreach ($f in '` + recordFull + `', '` + recordRepeat + `') {
	$p = Join-Path $tree $f
	if (Test-Path -LiteralPath $p) { Get-Content -LiteralPath $p -Raw -Encoding UTF8 }
}`

// stopScript ends the test run testScript started in sha's tree, and every
// process under it, by the process ID in pidFile; it does nothing when
// there is no such file or it holds anything but a number.
var stopScript = rootLine + `$tree = Join-Path (Join-Path $root '` + winDir + `') $env:PLEIADES_SHA
$id = Get-Content -LiteralPath (Join-Path $tree '` + pidFile + `') -ErrorAction SilentlyContinue | Select-Object -First 1
if ($id -match '^\d+$') { & taskkill.exe /T /F /PID $id 2>&1 | Out-Null }
exit 0`
