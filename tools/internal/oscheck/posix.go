// A check on a POSIX machine (macOS, FreeBSD), through exec.command: the
// tree goes by file.copy and archive.extract, and the tests run under
// /bin/sh, as a job of their own so an interrupt can end them all.
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
}

// Kept is how many trees a machine keeps, the newest.
const Kept = 3

// Need is a command the machine must have on its PATH, and what to do when
// it does not.
type Need struct {
	Command, Why string
}

// Ready asks the machine for its kernel, for each of needs and for the
// space free under its login's home, refusing one whose kernel is not kernel
// (noun names such a machine: "Mac"), that lacks a command, or that has
// less than MinFreeGB free (FreeSpace).
func (x POSIX) Ready(target, kernel, noun string, needs ...Need) error {
	out, err := x.Pleiades.Adhoc(context.Background(), "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.readyScript(needs)))
	if err != nil {
		return fmt.Errorf("checking %s: %v", target, err)
	}
	return x.readyAnswer(target, kernel, noun, out, needs)
}

// readyScript prints the kernel's name, then for each need its command, or
// no- and its command when it is not on the PATH, then the whole gigabytes
// free on the file system holding the login's home, where the trees and Go's
// build cache are, one line each.
func (x POSIX) readyScript(needs []Need) string {
	lines := []string{`export PATH="$PATH:` + x.Path + `"`, "uname -s"}
	for _, n := range needs {
		lines = append(lines, fmt.Sprintf("command -v %[1]s >/dev/null 2>&1 && echo %[1]s || echo no-%[1]s", n.Command))
	}
	lines = append(lines, `df -Pk "$HOME" | awk 'NR==2 {print int($4/1048576)}'`)
	return strings.Join(lines, "\n")
}

// readyAnswer checks readyScript's output.
func (x POSIX) readyAnswer(target, kernel, noun, out string, needs []Need) error {
	lines := strings.Fields(out)
	if len(lines) != 2+len(needs) {
		return fmt.Errorf("%s answered the readiness check with %q", target, out)
	}
	if lines[0] != kernel {
		return fmt.Errorf("%s is not a %s: its kernel is %s", target, noun, lines[0])
	}
	for i, n := range needs {
		if lines[i+1] != n.Command {
			return fmt.Errorf("%s has no %s on its PATH (looked in its PATH and %s): %s", target, n.Command, x.Path, n.Why)
		}
	}
	return FreeSpace(target, lines[len(lines)-1], "go clean -cache there, or for the lab's bsd-check run runbooks/bsd/check-05-disk-cleanup.yaml")
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
// Kept-1 trees and any earlier copy of sha's, and prints the home
// directory.
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
	run := func(ctx context.Context) (Outcome, error) {
		return x.Pleiades.AdhocOutcome(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.TestScript(sha, parallel, packages)), fmt.Sprintf("--timeout=%d", TestTimeout))
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
// repeats, under Wrap. Standard error joins standard output so they read
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
exit $status`, x.Path, x.Dir, sha, p, pkgs, x.PIDFile(), x.Wrap, stopRun, RecordFiles[0], RecordFiles[1])
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
	return fmt.Sprintf(`cd "$HOME/%s/%s" || exit 0
for f in %s; do [ -f "$f" ] && cat "$f"; done
exit 0`, x.Dir, sha, strings.Join(RecordFiles, " "))
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
// it tests and their dependencies into Go's build cache, which nothing
// trims: bsd-check's grew to 7.8 GB and filled its disk on 2026-10-19, and
// every package then failed to build with no compiler message reaching the
// report.
const MinFreeGB = 4

// FreeSpace refuses a machine whose readiness check said it has fewer than
// MinFreeGB gigabytes free (free, a whole number as text), saying how much
// it has and fix, what frees space there.
func FreeSpace(target, free, fix string) error {
	gb, err := strconv.Atoi(free)
	if err != nil {
		return fmt.Errorf("%s answered its free space with %q, not a number", target, free)
	}
	if gb < MinFreeGB {
		return fmt.Errorf("%s has %d GB free where its tests unpack and build, under the %d GB a run needs: %s", target, gb, MinFreeGB, fix)
	}
	return nil
}
