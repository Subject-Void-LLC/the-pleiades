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
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
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
	// Containers, when set, makes Test run the container-dependent packages
	// against the machine's Podman (ContainerTestScript) in place of the
	// container-free passes.
	Containers *Containers
}

// Kept is how many trees a machine keeps, the newest.
const Kept = 3

// Need is a command the machine must have on its PATH, and what to do when
// it does not.
type Need struct {
	Command, Why string
}

// Ready asks the machine for its kernel, for each of needs and for the
// space free under its login's home, making room first (readyScript),
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
// free on the file system holding the login's home, where the trees and the
// check's build cache are, before and after making room, one line each.
// Making room removes only what is this check's own, under Dir: every tree
// but the newest Kept-1 (ls skips the dot-named CacheDir), and, while the
// space free is still under MinFreeGB, CacheDir.
func (x POSIX) readyScript(needs []Need) string {
	lines := []string{`export PATH="$PATH:` + x.Path + `"`, "uname -s"}
	for _, n := range needs {
		lines = append(lines, fmt.Sprintf("command -v %[1]s >/dev/null 2>&1 && echo %[1]s || echo no-%[1]s", n.Command))
	}
	lines = append(lines, fmt.Sprintf(`free_gb() { df -Pk "$HOME" | awk 'NR==2 {print int($4/1048576)}'; }
base="$HOME/%[1]s"
before=$(free_gb)
if [ -d "$base" ]; then
	ls -1t "$base" | tail -n +%[2]d | while read -r old; do rm -rf "${base:?}/$old"; done
	if [ "$(free_gb)" -lt %[3]d ]; then rm -rf "${base:?}/%[4]s"; fi
fi
echo "$before"
free_gb`, x.Dir, Kept, MinFreeGB, CacheDir))
	return strings.Join(lines, "\n")
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
	return Room(target, lines[len(lines)-2], lines[len(lines)-1], "~/"+x.Dir+"/"+CacheDir,
		"for the lab's bsd-check, runbooks/bsd/check-05-disk-cleanup.yaml clears its login's own Go cache")
}

// Ship sends commit sha's tree: exec.command removes all but the newest
// trees and answers the login's home directory, git archive writes the
// tree into the project, file.copy sends it, and archive.extract unpacks it
// into a directory of its own and deletes the archive.
func (x POSIX) Ship(sha string) error {
	ctx := context.Background()
	out, err := x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.prepareScript(sha)))
	if err != nil {
		return fmt.Errorf("preparing %s: %v", x.Machine, err)
	}
	home := strings.TrimSpace(out)
	if !strings.HasPrefix(home, "/") {
		return fmt.Errorf("%s named %q as its home directory", x.Machine, home)
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
// copy of sha's, and prints the home directory.
func (x POSIX) prepareScript(sha string) string {
	return fmt.Sprintf(`set -e
base="$HOME/%[1]s"
mkdir -p "$base"
ls -1t "$base" | tail -n +%[2]d | while read -r old; do rm -rf "${base:?}/$old"; done
rm -rf "${base:?}/%[3]s" "${base:?}/%[3]s.tar.gz"
printf '%%s\n' "$HOME"`, x.Dir, Kept, sha)
}

// Test runs the packages on sha's tree through exec.command (see
// TestScript), stopping them with StopScript when ctx ends first.
func (x POSIX) Test(ctx context.Context, target, sha string, parallel int, packages []string, stdout io.Writer) (bool, error) {
	if !FileExists(x.Pleiades.Binary) {
		return false, fmt.Errorf("this worktree has no pleiades for %s yet; run -phase prepare first", target)
	}
	fmt.Fprintf(stdout, "%s: running on %s; Pleiades returns the output when the run ends, since it does not stream it yet\n", x.Tool, x.Machine)
	script := x.TestScript(sha, parallel, packages)
	if x.Containers != nil {
		script = x.ContainerTestScript(sha, parallel, packages)
	}
	run := func(ctx context.Context) (Outcome, error) {
		return x.Pleiades.AdhocOutcome(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", script), fmt.Sprintf("--timeout=%d", TestTimeout))
	}
	stop := func(ctx context.Context) error {
		_, err := x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.StopScript(sha)))
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
export GOCACHE="$HOME/%[2]s/%[11]s"
cd "$HOME/%[2]s/%[3]s" || exit 2
%[8]s
rm -f %[9]s %[10]s
set -m
%[7]s/bin/sh -c 'go run ./tools/testgate -full -report-out %[9]s %[4]s %[5]s && go run ./tools/testgate -repeat 3 -strict -report-out %[10]s %[4]s %[5]s' &
job=$!
set +m
echo "$job" > %[6]s
trap 'stop_run "$job"; rm -f %[6]s; exit 143' TERM HUP INT
wait "$job"
status=$?
rm -f %[6]s
exit $status`, x.Path, x.Dir, sha, p, pkgs, x.PIDFile(), x.Wrap, stopRun, RecordFiles[0], RecordFiles[1], CacheDir)
}

// VulnTimeout bounds a govulncheck run on the machine, in seconds, as the
// task's own timeout: a scan took 8 to 35 seconds on a Mac in October 2026,
// and the first one also downloads govulncheck and a Go release.
const VulnTimeout = 30 * 60

// goRelease is what this machine's Go release must look like to be named to
// another machine's go command, which fetches it.
var goRelease = regexp.MustCompile(`^go[0-9]+\.[0-9]+(\.[0-9]+)?$`)

// Vuln runs govulncheck release version over sha's tree through
// exec.command (VulnScript), analyzing it for this machine's own operating
// system, architecture and Go release, so its answer is the one a scan here
// gives. An interrupt ends the pleiades here; a scan it leaves on the
// machine ends by itself.
func (x POSIX) Vuln(ctx context.Context, sha, version string, stdout io.Writer) (bool, error) {
	if !FileExists(x.Pleiades.Binary) {
		return false, fmt.Errorf("this worktree has no pleiades for %s yet; run -phase prepare first", x.Machine)
	}
	if !goRelease.MatchString(runtime.Version()) {
		return false, fmt.Errorf("this machine's Go is %s, not a release %s can fetch to scan as this machine would", runtime.Version(), x.Machine)
	}
	fmt.Fprintf(stdout, "%s: govulncheck %s runs on %s, scanning for %s/%s with %s as a scan here would\n", x.Tool, version, x.Machine, runtime.GOOS, runtime.GOARCH, runtime.Version())
	out, err := x.Pleiades.AdhocOutcome(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.VulnScript(sha, version, runtime.GOOS, runtime.GOARCH, runtime.Version())), fmt.Sprintf("--timeout=%d", VulnTimeout))
	if err != nil {
		return false, err
	}
	fmt.Fprintln(stdout, strings.TrimRight(out.Output, "\r\n"))
	return out.ExitCode == 0, nil
}

// VulnScript is what the machine runs for Vuln, in sha's tree: it installs
// govulncheck release version into the tree, where it goes when the tree is
// pruned (Go's build cache makes the next install cheap), then runs it over
// every package under Wrap, exiting with its status. GOTOOLCHAIN names
// goVersion for the scan, since the machine's own go may be newer than the
// toolchain line and would otherwise judge its own standard library; GOOS
// and GOARCH name goos and goarch for the scan alone, since the install
// builds a program the machine runs. Both build into the check's CacheDir.
func (x POSIX) VulnScript(sha, version, goos, goarch, goVersion string) string {
	return fmt.Sprintf(`exec 2>&1
export PATH="$PATH:%[1]s"
export GOCACHE="$HOME/%[2]s/%[9]s"
cd "$HOME/%[2]s/%[3]s" || exit 2
GOBIN="$PWD/.pleiades-bin" go install golang.org/x/vuln/cmd/govulncheck@%[4]s || exit $?
GOTOOLCHAIN=%[7]s GOOS=%[5]s GOARCH=%[6]s %[8]s"$PWD/.pleiades-bin/govulncheck" ./...`, x.Path, x.Dir, sha, version, goos, goarch, goVersion, x.Wrap, CacheDir)
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
	files := RecordFiles
	if x.Containers != nil {
		files = []string{ContainerRecordFile}
	}
	return fmt.Sprintf(`cd "$HOME/%s/%s" || exit 0
for f in %s; do [ -f "$f" ] && cat "$f"; done
exit 0`, x.Dir, sha, strings.Join(files, " "))
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
func (x POSIX) PIDFile() string { return "." + x.Tool + ".pid" }

// StopScript ends every process of the test run TestScript started in
// sha's tree (see stopRun), and does nothing when there is no run.
func (x POSIX) StopScript(sha string) string {
	return fmt.Sprintf(`pid=$(cat "$HOME/%[1]s/%[2]s/%[3]s" 2>/dev/null) || exit 0
case "$pid" in ''|*[!0-9]*) exit 0 ;; esac
%[4]s
stop_run "$pid"
exit 0`, x.Dir, sha, x.PIDFile(), stopRun)
}

// MinFreeGB is the space, in whole gigabytes, a machine must have free
// before a check runs on it. A run unpacks a tree and builds every package
// it tests and their dependencies into Go's build cache: bsd-check's grew to
// 7.8 GB and filled its disk on 2026-10-19, and every package then failed to
// build with no compiler message reaching the report.
const MinFreeGB = 4

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
