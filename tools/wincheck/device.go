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

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// winDir is where trees are unpacked, under the login's profile, one
// directory per commit; the newest oscheck.Kept stay.
const winDir = ".pleiades-wincheck"

// winPath is put before the PATH a WinRM session starts with, which a
// machine-wide install of go may not be on yet: where the lab's win-check
// unpacked it, then where go.dev's installer puts it.
const winPath = `C:\go\bin;C:\Program Files\Go\bin`

// files is the directory, relative to the project, the archive is written
// in to be sent; each archive is deleted once sent.
const files = ".wincheck"

// testSeconds bounds the test run on the machine, as exec.winrm.shell's
// timeout: go test's own per-package timeout is 30 minutes, and a run is
// two passes over every package the branch changes.
const testSeconds = 4 * 60 * 60

// windows is the real machine.
type windows struct {
	oscheck.Lab
}

// newLeg is device in project.
func newLeg(project, device string) (oscheck.Leg, error) {
	lab, err := oscheck.NewLab("wincheck", project, device)
	if err != nil {
		return nil, err
	}
	return windows{Lab: lab}, nil
}

// shell runs script through exec.winrm.shell, under PowerShell, with env's
// values as $env:PLEIADES_<NAME>, returning its standard output.
func (w windows) shell(ctx context.Context, script string, env map[string]string, seconds int) (string, error) {
	params := []string{"shell=powershell", "command=" + script, "timeout:=" + strconv.Itoa(seconds)}
	if len(env) > 0 {
		params = append(params, "env:="+oscheck.MapYAML(env))
	}
	return w.Pleiades.Adhoc(ctx, "exec.winrm.shell", params...)
}

// Ready builds pleiades from this tree and asks the machine whether it
// runs Windows with go and tar.exe.
func (w windows) Ready() error {
	if err := w.Pleiades.Build(); err != nil {
		return err
	}
	out, err := w.shell(context.Background(), readyScript, nil, 60)
	if err != nil {
		return fmt.Errorf("checking %s: %v", w.Target, err)
	}
	return readyAnswer(w.Target, out)
}

// readyScript prints the platform, then go and tar, or no-go and no-tar
// when either is not on the PATH, one line each.
const readyScript = `$env:Path = '` + winPath + `;' + $env:Path
[string][Environment]::OSVersion.Platform
if (Get-Command go.exe -ErrorAction SilentlyContinue) { 'go' } else { 'no-go' }
if (Get-Command tar.exe -ErrorAction SilentlyContinue) { 'tar' } else { 'no-tar' }`

// readyAnswer checks readyScript's output.
func readyAnswer(target, out string) error {
	lines := strings.Fields(out)
	switch {
	case len(lines) != 3:
		return fmt.Errorf("%s answered the readiness check with %q", target, out)
	case lines[0] != "Win32NT":
		return fmt.Errorf("%s is not a Windows machine: its platform is %s", target, lines[0])
	case lines[1] != "go":
		return fmt.Errorf("%s has no go on its PATH (looked in its PATH and %s): install it from https://go.dev/dl, or as the lab's win-check has it (examples/virtualbox_lab)", target, winPath)
	case lines[2] != "tar":
		return fmt.Errorf("%s has no tar.exe, which unpacks the tree; Windows has had it since Windows 10 1803 and Server 2019", target)
	}
	return nil
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
// stop at the first error, and name the trees' directory and sha's tree.
const treeLines = `$ErrorActionPreference = 'Stop'
$base = Join-Path $env:USERPROFILE '` + winDir + `'
$tree = Join-Path $base $env:PLEIADES_SHA
`

// prepareScript makes the trees' directory, removes all but the newest
// Kept-1 trees and any earlier copy of sha's, and prints the profile
// directory.
var prepareScript = treeLines + `New-Item -ItemType Directory -Force -Path $base | Out-Null
Get-ChildItem -LiteralPath $base -Directory | Sort-Object LastWriteTime -Descending | Select-Object -Skip ` + strconv.Itoa(oscheck.Kept-1) + ` | Remove-Item -Recurse -Force
foreach ($old in @($tree, ($tree + '.tar.gz'))) { if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Recurse -Force } }
$env:USERPROFILE`

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
	run := func(ctx context.Context) (string, error) { return w.shell(ctx, testScript, env, testSeconds) }
	stop := func(ctx context.Context) error {
		_, err := w.shell(ctx, stopScript, map[string]string{"SHA": sha}, 60)
		return err
	}
	return oscheck.RunTests(ctx, w.Target, oscheck.Marker("wincheck"), run, stop, stdout)
}

// pidFile is where, in the unpacked tree, testScript writes the process
// that runs the tests.
const pidFile = ".wincheck.pid"

// testScript is what the machine runs in the unpacked tree: pr-check's two
// passes over the container-free packages, the full pass without the race
// detector and three strict repeats, and it always exits 0, ending with the
// marker and the tests' exit status (see oscheck.Marker). Each pass runs
// under cmd.exe, which joins its standard error to its standard output so
// they read in order: PowerShell would make each stderr line an error
// record, and print an empty one as the record's type name. The command
// line cmd.exe parses holds only the package list, refused here unless it
// is import paths and spaces (oscheck.Options.Check refused anything else
// before it was sent), and a number. The script's own process ID is written
// to pidFile first, so stopScript can end it and everything under it, and
// the file is removed at the end, so a later stop cannot reach a process
// that reused the number.
var testScript = `$ErrorActionPreference = 'Continue'
$env:Path = '` + winPath + `;' + $env:Path
$tree = Join-Path (Join-Path $env:USERPROFILE '` + winDir + `') $env:PLEIADES_SHA
if (-not (Test-Path -LiteralPath $tree)) { '` + oscheck.Marker("wincheck") + `2'; exit 0 }
if ($env:PLEIADES_PACKAGES -cnotmatch '^[A-Za-z0-9._/ -]+$') { '` + oscheck.Marker("wincheck") + `2'; exit 0 }
Set-Location -LiteralPath $tree
$pidFile = Join-Path $tree '` + pidFile + `'
Set-Content -LiteralPath $pidFile -Value $PID
$parallel = [int]$env:PLEIADES_PARALLEL
& cmd.exe /d /c "go run ./tools/testgate -full -race=false -p $parallel $env:PLEIADES_PACKAGES 2>&1"
$code = $LASTEXITCODE
if ($code -eq 0) {
	& cmd.exe /d /c "go run ./tools/testgate -repeat 3 -strict -p $parallel $env:PLEIADES_PACKAGES 2>&1"
	$code = $LASTEXITCODE
}
Remove-Item -LiteralPath $pidFile -Force -ErrorAction SilentlyContinue
'` + oscheck.Marker("wincheck") + `' + $code
exit 0`

// stopScript ends the test run testScript started in sha's tree, and every
// process under it, by the process ID in pidFile; it does nothing when
// there is no such file or it holds anything but a number.
var stopScript = `$tree = Join-Path (Join-Path $env:USERPROFILE '` + winDir + `') $env:PLEIADES_SHA
$id = Get-Content -LiteralPath (Join-Path $tree '` + pidFile + `') -ErrorAction SilentlyContinue | Select-Object -First 1
if ($id -match '^\d+$') { & taskkill.exe /T /F /PID $id 2>&1 | Out-Null }
exit 0`
