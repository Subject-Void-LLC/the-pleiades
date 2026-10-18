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
	"strings"
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

// Ready asks the machine for its kernel and for each of needs, refusing
// one whose kernel is not kernel (noun names such a machine: "Mac") or that
// lacks a command.
func (x POSIX) Ready(target, kernel, noun string, needs ...Need) error {
	out, err := x.Pleiades.Adhoc(context.Background(), "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.readyScript(needs)))
	if err != nil {
		return fmt.Errorf("checking %s: %v", target, err)
	}
	return x.readyAnswer(target, kernel, noun, out, needs)
}

// readyScript prints the kernel's name, then for each need its command, or
// no- and its command when it is not on the PATH, one line each.
func (x POSIX) readyScript(needs []Need) string {
	lines := []string{`export PATH="$PATH:` + x.Path + `"`, "uname -s"}
	for _, n := range needs {
		lines = append(lines, fmt.Sprintf("command -v %[1]s >/dev/null 2>&1 && echo %[1]s || echo no-%[1]s", n.Command))
	}
	return strings.Join(lines, "\n")
}

// readyAnswer checks readyScript's output.
func (x POSIX) readyAnswer(target, kernel, noun, out string, needs []Need) error {
	lines := strings.Fields(out)
	if len(lines) != 1+len(needs) {
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
	return nil
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
	run := func(ctx context.Context) (string, error) {
		return x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.TestScript(sha, parallel, packages)))
	}
	stop := func(ctx context.Context) error {
		_, err := x.Pleiades.Adhoc(ctx, "exec.command", "argv:="+ArgvYAML("/bin/sh", "-c", x.StopScript(sha)))
		return err
	}
	return RunTests(ctx, target, Marker(x.Tool), run, stop, stdout)
}

// TestScript is what the machine runs in the unpacked tree: pr-check's two
// passes over the container-free packages, the full pass and three strict
// repeats, under Wrap. Standard error joins standard output so they read
// in order, and the script always exits 0, ending with the marker and the
// tests' exit status (see Marker). The tests start as a job of their own
// (set -m), in a process group whose ID is written to PIDFile, so
// StopScript can end every process of the run at once; job control is then
// turned off again, so the shell prints no job notice into the output.
func (x POSIX) TestScript(sha string, parallel int, packages []string) string {
	pkgs := strings.Join(packages, " ")
	p := fmt.Sprintf("-p %d", parallel)
	return fmt.Sprintf(`exec 2>&1
export PATH="$PATH:%[1]s"
cd "$HOME/%[2]s/%[3]s" || { echo "%[4]s2"; exit 0; }
set -m
%[8]s/bin/sh -c 'go run ./tools/testgate -full %[5]s %[6]s && go run ./tools/testgate -repeat 3 -strict %[5]s %[6]s' &
job=$!
set +m
echo "$job" > %[7]s
wait "$job"
echo "%[4]s$?"`, x.Path, x.Dir, sha, Marker(x.Tool), p, pkgs, x.PIDFile(), x.Wrap)
}

// PIDFile is where, in the unpacked tree, TestScript writes its tests'
// process group.
func (x POSIX) PIDFile() string { return "." + x.Tool + ".pid" }

// StopScript ends every process of the test run TestScript started in
// sha's tree, by its process group.
func (x POSIX) StopScript(sha string) string {
	return fmt.Sprintf(`pid=$(cat "$HOME/%[1]s/%[2]s/%[3]s" 2>/dev/null) || exit 0
case "$pid" in ''|*[!0-9]*) exit 0 ;; esac
kill -TERM -- "-$pid" 2>/dev/null
exit 0`, x.Dir, sha, x.PIDFile())
}
