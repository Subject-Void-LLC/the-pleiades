// A check on a POSIX machine (macOS, FreeBSD), through exec.command: the
// tree goes by file.copy and archive.extract, and the tests run under
// /bin/sh, as a job of their own so an interrupt can end them all; so does
// govulncheck, as a scan here would run it.
package oscheck

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

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/headroom"
)

// POSIX is a check's machine when it runs a POSIX shell.
type POSIX struct {
	// Pleiades drives the machine.
	Pleiades Pleiades
	// Tool names the check, and its marker and process group file.
	Tool string
	// Machine names the kind of machine in what it prints ("the Mac").
	Machine string
	// Dir is where trees are unpacked, under the login's home, one
	// directory per commit; the newest Kept stay.
	Dir string
	// Path is added to PATH, since a non-interactive SSH session does not
	// read the profile that puts go on it.
	Path string
	// Wrap is what the tests run under, ending in a space ("caffeinate
	// -dims ", which keeps a Mac awake until they end), or "".
	Wrap string
	// Files is the directory, relative to Pleiades.Project, the archive is
	// written in to be sent; each archive is deleted once sent.
	Files string
	// Data is the absolute directory on the machine every run's variable data
	// goes under: Dir with the trees and the build cache, the module cache
	// (gomod) and the temporary directory (tmp); on a lab VM a second disk
	// mounted there. Empty, the trees go under the login's home and the
	// caches and temporary files where the login's own are.
	Data string
	// Dedicated says the machine exists to run checks (a VM a check starts and
	// puts back), so readyScript also clears the login's own Go caches before
	// each run, which on any other machine are the owner's (dedicatedClean).
	Dedicated bool
	// DropCache removes each run's Go build cache (CacheDir) when the run
	// ends, pass, fail or interrupt, rather than only when the machine runs
	// short of space: the Mac's grew to 20 GB in two days (2026-10-28) on a
	// disk it shares with its owner.
	DropCache bool
}

// Kept is how many trees a machine keeps, the newest.
const Kept = 3

// root is the shell text for the directory the trees' directory is under:
// Data, or the login's home.
func (x POSIX) root() string {
	if x.Data != "" {
		return x.Data
	}
	return "$HOME"
}

// base is the shell text for the trees' directory, under root.
func (x POSIX) base() string {
	return x.root() + "/" + x.Dir
}

// dataEnv is the shell that points a run's module cache and temporary
// directory under Data, making both, or nothing without a Data: go test's
// work folders, the tests' temporary folders and every module downloaded
// then live on the data disk too, with the trees and the build cache.
func (x POSIX) dataEnv() string {
	if x.Data == "" {
		return ""
	}
	return fmt.Sprintf(`mkdir -p "%[1]s/tmp" "%[1]s/gomod" || exit 2
export TMPDIR="%[1]s/tmp" GOMODCACHE="%[1]s/gomod"
`, x.Data)
}

// Need is a command the machine must have on its PATH, and what to do when
// it does not.
type Need struct {
	Command, Why string
}

// Ready asks the machine for its kernel, for each of needs and for the
// space free under its data directory (its login's home when it has
// none), making room first (readyScript),
// refusing one whose kernel is not kernel (noun names such a machine:
// "Mac"), that lacks a command, or that still has less than MinFreeGB free
// (Room). Room it made is said on standard output.
func (x POSIX) Ready(target, kernel, noun string, needs ...Need) error {
	out, err := x.Pleiades.Adhoc(context.Background(), "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.readyScript(needs)))
	if err != nil {
		return fmt.Errorf("checking %s: %v", target, err)
	}
	note, err := x.readyAnswer(target, kernel, noun, out, needs)
	if note != "" {
		fmt.Printf("%s: %s\n", x.Tool, note)
	}
	return err
}

// readyScript prints the kernel's name, then for each need its command, or
// no- and its command when it is not on the PATH, then the whole gigabytes
// free on the file system holding the data directory (the login's home
// without one), where the trees and the check's build cache are, before and
// after making room, one line each. Making room removes what is this
// check's own, under Dir: every tree but the newest Kept-1 (ls skips the
// dot-named CacheDir), and, while the space free is still under MinFreeGB,
// CacheDir. On a Dedicated machine it first clears the login's own Go build
// cache and the go-build folders it left in /tmp, every run, and its module
// cache while still short (dedicatedClean).
func (x POSIX) readyScript(needs []Need) string {
	lines := []string{`export PATH="$PATH:` + x.Path + `"`, "uname -s"}
	for _, n := range needs {
		lines = append(lines, fmt.Sprintf("command -v %[1]s >/dev/null 2>&1 && echo %[1]s || echo no-%[1]s", n.Command))
	}
	lines = append(lines, fmt.Sprintf(`free_gb() { df -Pk "%[6]s" | awk 'NR==2 {print int($4/1048576)}'; }
base="%[1]s"
before=$(free_gb)
%[5]s
if [ -d "$base" ]; then
	ls -1t "$base" | tail -n +%[2]d | while read -r old; do rm -rf "${base:?}/$old"; done
	if [ "$(free_gb)" -lt %[3]d ]; then rm -rf "${base:?}/%[4]s"; fi
fi
echo "$before"
free_gb`, x.base(), Kept, MinFreeGB, CacheDir, x.dedicatedClean(), x.root()))
	return strings.Join(lines, "\n")
}

// dedicatedClean is what readyScript frees first on a Dedicated machine: the
// login's default Go build cache (GOCACHE emptied names it), what a stopped
// go test left in /tmp and $TMPDIR that belongs to this login (its go-build
// work folders and its tests' Test* temporary folders: 5 GB of them filled
// bsd-check after a day of interrupted gates, 2026-10-27), and, while still
// under MinFreeGB, the login's module cache, which the next build downloads
// again. With a Data directory, the run's own temporary directory under it
// is emptied every run, and its module cache there while short. Nothing on
// another machine.
func (x POSIX) dedicatedClean() string {
	if !x.Dedicated {
		return ""
	}
	dirs := `/tmp "${TMPDIR:-/tmp}"`
	modcache := ""
	if x.Data != "" {
		dirs += fmt.Sprintf(` "%s/tmp"`, x.Data)
		modcache = fmt.Sprintf(`
if [ "$(free_gb)" -lt %d ]; then rm -rf "%s/gomod"; fi`, MinFreeGB, x.Data)
	}
	return fmt.Sprintf(`if command -v go >/dev/null 2>&1; then GOCACHE= go clean -cache >/dev/null 2>&1; fi
for d in %s; do [ -d "$d" ] && find "$d" -mindepth 1 -maxdepth 1 \( -name 'go-build*' -o -name 'Test*' \) -user "$(id -u)" -exec rm -rf {} + 2>/dev/null; done
if [ "$(free_gb)" -lt %d ] && command -v go >/dev/null 2>&1; then go clean -modcache >/dev/null 2>&1; fi%s`, dirs, MinFreeGB, modcache)
}

// readyAnswer checks readyScript's output, returning what room it made.
func (x POSIX) readyAnswer(target, kernel, noun, out string, needs []Need) (string, error) {
	lines := strings.Fields(out)
	if len(lines) != 3+len(needs) {
		return "", fmt.Errorf("%s answered the readiness check with %q", target, out)
	}
	if lines[0] != kernel {
		return "", fmt.Errorf("%s is not a %s: its kernel is %s", target, noun, lines[0])
	}
	for i, n := range needs {
		if lines[i+1] != n.Command {
			return "", fmt.Errorf("%s has no %s on its PATH (looked in its PATH and %s): %s", target, n.Command, x.Path, n.Why)
		}
	}
	cache := strings.Replace(x.base(), "$HOME", "~", 1) + "/" + CacheDir
	if x.Dedicated {
		return Room(target, lines[len(lines)-2], lines[len(lines)-1], cache+"; and, on this machine kept for checks, the login's Go build and module caches and its /tmp/go-build folders",
			"what is left is outside Go's caches")
	}
	return Room(target, lines[len(lines)-2], lines[len(lines)-1], cache,
		"for the lab's bsd-check, runbooks/bsd/check-05-disk-cleanup.yaml clears its login's own Go cache")
}

// Ship sends commit sha's tree: exec.command removes all but the newest
// trees and answers the directory the trees are under (the data directory,
// or the login's home), git archive writes the tree into the project,
// file.copy sends it, and archive.extract unpacks it into a directory of
// its own and deletes the archive.
func (x POSIX) Ship(sha string) error {
	ctx := context.Background()
	out, err := x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.prepareScript(sha)))
	if err != nil {
		return fmt.Errorf("preparing %s: %v", x.Machine, err)
	}
	home := strings.TrimSpace(out)
	if !strings.HasPrefix(home, "/") {
		return fmt.Errorf("%s named %q as the directory its trees go under", x.Machine, home)
	}
	src := path.Join(x.Files, sha+".tar.gz")
	local := filepath.Join(x.Pleiades.Project, filepath.FromSlash(src))
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	defer func() { _ = os.Remove(local) }()
	if out, err := exec.Command("git", "archive", "--format=tar.gz", "-o", local, sha).CombinedOutput(); err != nil { // #nosec G204 -- a commit rev-parse resolved
		return fmt.Errorf("git archive: %v: %s", err, strings.TrimSpace(string(out)))
	}
	archive := path.Join(home, x.Dir, sha+".tar.gz")
	if _, err := x.Pleiades.Adhoc(ctx, "file.copy", "src="+src, "dest="+archive, "mode=0600"); err != nil {
		return err
	}
	_, err = x.Pleiades.Adhoc(ctx, "archive.extract", "src="+archive, "dest="+path.Join(home, x.Dir, sha), "remove:=true")
	return err
}

// prepareScript makes the trees' directory, removes all but the newest
// Kept-1 trees (ls lists no dot name, so CacheDir stays) and any earlier
// copy of sha's, and prints the directory the trees are under.
func (x POSIX) prepareScript(sha string) string {
	return fmt.Sprintf(`set -e
base="%[1]s"
mkdir -p "$base"
ls -1t "$base" | tail -n +%[2]d | while read -r old; do rm -rf "${base:?}/$old"; done
rm -rf "${base:?}/%[3]s" "${base:?}/%[3]s.tar.gz"
printf '%%s\n' "%[4]s"`, x.base(), Kept, sha, x.root())
}

// Test runs the packages on sha's tree through exec.command (see
// TestScript), stopping them with StopScript when ctx ends first.
func (x POSIX) Test(ctx context.Context, target, sha string, parallel int, packages []string, stdout io.Writer) (bool, error) {
	if !FileExists(x.Pleiades.Binary) {
		return false, fmt.Errorf("this worktree has no pleiades for %s yet; run -phase prepare first", target)
	}
	fmt.Fprintf(stdout, "%s: running on %s; Pleiades returns the output when the run ends, since it does not stream it yet\n", x.Tool, x.Machine)
	script := x.TestScript(sha, parallel, packages)
	run := func(ctx context.Context) (Outcome, error) {
		return x.Pleiades.AdhocOutcome(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", script), fmt.Sprintf("--timeout=%d", TestTimeout))
	}
	stop := func(ctx context.Context) error {
		out, err := x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.StopScript(sha)))
		if s := strings.TrimSpace(out); s != "" {
			fmt.Fprintln(stdout, s)
		}
		return err
	}
	return RunTests(ctx, target, run, stop, stdout)
}

// TestTimeout bounds a test run on the machine, in seconds, as the task's
// own timeout: go test's own per-package timeout is 30 minutes, and a run
// is two passes over every package the branch changes.
const TestTimeout = 4 * 60 * 60

// TestScript is what the machine runs in the unpacked tree: pr-check's two
// passes over the container-free packages, the full pass and three strict
// repeats, under Wrap, building into the check's own CacheDir. Standard error joins standard output so they read
// in order, and the script exits with the tests' status, which the failed
// task reports with their output. The tests start as a job of their own
// (set -m), which a shell with job control (macOS's, FreeBSD's) puts in a
// process group of its own; job control is then turned off again, so the
// shell prints no job notice into the output. The job's ID is written to
// PIDFile, so StopScript can end the run, and removed when the run ends, so
// a later stop cannot reach a process that reused the number. A SIGTERM to
// this shell (the task's timeout running out, which sshd delivers to the
// command it started) ends the tests the same way, rather than leaving
// them running behind it.
func (x POSIX) TestScript(sha string, parallel int, packages []string) string {
	pkgs := strings.Join(packages, " ")
	p := fmt.Sprintf("-p %d", parallel)
	return fmt.Sprintf(`exec 2>&1
export PATH="$PATH:%[1]s"
export GOCACHE="%[2]s/%[11]s"
%[13]scd "%[2]s/%[3]s" || exit 2
%[8]s
rm -f %[9]s %[10]s
set -m
%[7]s/bin/sh -c 'go run ./tools/testgate -full -report-out %[9]s %[4]s %[5]s && go run ./tools/testgate -repeat 3 -strict -report-out %[10]s %[4]s %[5]s' &
job=$!
set +m
echo "$job" > %[6]s
trap 'stop_run "$job"; rm -f %[6]s; %[12]sexit 143' TERM HUP INT
wait "$job"
status=$?
rm -f %[6]s
%[12]sexit $status`, x.Path, x.base(), sha, p, pkgs, x.PIDFile(), x.Wrap, stopRun, RecordFiles[0], RecordFiles[1], CacheDir, x.dropCache(CacheDir), x.dataEnv())
}

// dropCache is the shell command, ending in "; ", that removes the check's
// build cache (CacheDir) when DropCache is set, or nothing.
func (x POSIX) dropCache(dir string) string {
	if !x.DropCache {
		return ""
	}
	return fmt.Sprintf(`rm -rf "%s/%s"; `, x.base(), dir)
}

// Records reads back the testgate records TestScript's passes wrote in
// sha's tree (RecordFiles), those that exist.
func (x POSIX) Records(ctx context.Context, sha string) ([]flakegate.Run, error) {
	out, err := x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.RecordsScript(sha)))
	if err != nil {
		return nil, err
	}
	return ParseRecords(out)
}

// RecordsScript prints each of RecordFiles in sha's tree that exists, one
// after another.
func (x POSIX) RecordsScript(sha string) string {
	return fmt.Sprintf(`cd "%s/%s" || exit 0
for f in %s; do [ -f "$f" ] && cat "$f"; done
exit 0`, x.base(), sha, strings.Join(RecordFiles, " "))
}

// stopRun defines the shell function both scripts end a run with: SIGTERM to
// the run's process group, which a shell with job control gave it, then to
// every process under the run, read with pgrep -P (macOS, FreeBSD and Linux
// all have it), for a shell without job control (Linux's dash has none
// without a terminal) or a process that left the group. Each process is
// stopped before its children, listed just before it, so a parent (go test)
// cannot start another child while its current ones are being stopped; the
// for loop's list is expanded once, so the recursion reusing kids is safe.
const stopRun = `stop_tree() { kids=$(pgrep -P "$1" 2>/dev/null); kill -TERM "$1" 2>/dev/null; for c in $kids; do stop_tree "$c"; done; }
stop_run() { kill -TERM -- "-$1" 2>/dev/null; stop_tree "$1"; }`

// PIDFile is where, in the unpacked tree, TestScript writes its tests'
// job.
func (x POSIX) PIDFile() string {
	return "." + x.Tool + ".pid"
}

// StopScript ends every process of the test run TestScript started in
// sha's tree (see stopRun), and does nothing when there is no run.
func (x POSIX) StopScript(sha string) string {
	return fmt.Sprintf(`pid=$(cat "%[1]s/%[2]s/%[3]s" 2>/dev/null) || exit 0
case "$pid" in ''|*[!0-9]*) exit 0 ;; esac
%[4]s
stop_run "$pid"
exit 0`, x.base(), sha, x.PIDFile(), stopRun)
}

// MinFreeGB is the space, in whole gigabytes, a machine must have free
// before a check runs on it: the one policy's (headroom.MinFreeGB), which
// testgate holds the run to again on the machine itself. A run unpacks a
// tree and builds every package it tests and their dependencies into Go's
// build cache: bsd-check's grew to 7.8 GB and filled its disk on 2026-10-19,
// and every package then failed to build with no compiler message reaching
// the report.
const MinFreeGB = headroom.MinFreeGB

// CacheDir is the Go build cache (GOCACHE) a check's runs build into, in its
// trees' directory, so the cache is the check's own to remove when the
// machine runs short of space. The login's default cache is shared with
// whatever else that login builds, so no check ever removes it. A dot name
// keeps it out of the trees a prune removes.
const CacheDir = ".gocache"

// Room reads a readiness check's free space before and after it made room
// (whole gigabytes as text): note says what was removed when the machine
// started under MinFreeGB, and err refuses a machine still under it, saying
// how much it has and hint, what frees the rest there. cache names the
// check's own cache, which making room removed.
func Room(target, before, after, cache, hint string) (note string, err error) {
	was, err := strconv.Atoi(before)
	if err != nil {
		return "", fmt.Errorf("%s answered its free space with %q, not a number", target, before)
	}
	now, err := strconv.Atoi(after)
	if err != nil {
		return "", fmt.Errorf("%s answered its free space with %q, not a number", target, after)
	}
	if was < MinFreeGB {
		note = fmt.Sprintf("%s had %d GB free, under the %d GB a run needs, so this check removed its own older trees and build cache (%s): %d GB free now, and this run builds from a cold cache",
			target, was, MinFreeGB, cache, now)
	}
	if now < MinFreeGB {
		return note, fmt.Errorf("%s has %d GB free where its tests unpack and build, under the %d GB a run needs, after this check removed what is its own to remove (its older trees and its build cache, %s); the rest is not this check's to remove: free space there yourself (%s)",
			target, now, MinFreeGB, cache, hint)
	}
	return note, nil
}
