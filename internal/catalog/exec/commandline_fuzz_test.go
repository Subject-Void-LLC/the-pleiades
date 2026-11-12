// FuzzShellCommandLine holds the exact line exec.shell sends (the cd prefix,
// the shell path, -c and the command) to a real POSIX shell.
//
// FuzzShellParams stops before this: with no device, exec.shell returns at
// its connect, and its only check before that is that cmd is not empty.
// pkg/remoteexec's quoting fuzz targets read their output with a Go model of
// sh, not with sh itself. This target runs commandLine's output under
// /bin/sh. A recorder script stands in for the shell, so the fuzzed command
// is passed as an argument and never run.
package exec

import (
	"bytes"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// fuzzDirs are the directories each iteration's working directory holds,
// so a seeded chdir can succeed: names a shell would read as an option, a
// substitution, a quote or a separator.
var fuzzDirs = []string{"sub dir", "-", "--", "-P", "-L", "a'b", "x;y", "$(id)"}

// recorderScript writes its working directory and each of its arguments,
// NUL-terminated, where REC_OUT names. It runs nothing it is given.
const recorderScript = "#!/bin/sh\npwd -P > \"$REC_OUT.pwd\"\nfor a; do printf '%s\\0' \"$a\"; done > \"$REC_OUT\"\n"

func FuzzShellCommandLine(f *testing.F) {
	need.POSIXShell(f)
	rec := filepath.Join(f.TempDir(), "rec")
	if err := os.WriteFile(rec, []byte(recorderScript), 0o700); err != nil { // #nosec G306 -- the recorder must be executable
		f.Fatal(err)
	}

	f.Add("echo hi", "")
	f.Add("echo hi; touch pwned", "sub dir")
	f.Add("'", "-P")
	f.Add("$(touch pwned)", "-L")
	f.Add("`touch pwned`", "a'b")
	f.Add("a\nb", "x;y")
	f.Add("", "$(id)")
	f.Add("x", "missing")
	f.Add("x", "-")
	f.Add("x", "/")
	f.Add("x", "..")
	f.Add("x", "'; touch pwned; '")

	f.Fuzz(func(t *testing.T, line, dir string) {
		// A NUL cannot reach a shell: remoteexec refuses one before sending.
		if strings.ContainsRune(line, 0) || strings.ContainsRune(dir, 0) {
			t.Skip("a NUL is refused before any command is sent")
		}
		work := t.TempDir()
		for _, d := range fuzzDirs {
			if err := os.Mkdir(filepath.Join(work, d), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		out := filepath.Join(work, "out")

		command := commandLine([]string{rec, "-c", line}, dir)
		cmd := osexec.Command("/bin/sh", "-c", command) // #nosec G204 -- the line under test, run in a temporary directory
		cmd.Dir = work
		cmd.Env = []string{"PATH=/usr/bin:/bin", "REC_OUT=" + out, "HOME=" + work}
		output, err := cmd.CombinedOutput()
		code := 0
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("running %q: %v", command, err)
		}

		recorded, readErr := os.ReadFile(out)
		switch {
		case code == cdFailedExitCode:
			if readErr == nil {
				t.Fatalf("chdir %q failed, yet the shell ran: %q", dir, command)
			}
		case code != 0:
			t.Fatalf("%q exited %d, neither the shell's answer nor a failed cd:\n%s", command, code, output)
		default:
			if readErr != nil {
				t.Fatalf("%q exited 0 without running the shell: %v\n%s", command, readErr, output)
			}
			args := strings.Split(string(bytes.TrimSuffix(recorded, []byte{0})), "\x00")
			if want := []string{"-c", line}; !slices.Equal(args, want) {
				t.Fatalf("%q handed the shell %q, want %q", command, args, want)
			}
			if dir != "" {
				pwd, err := os.ReadFile(out + ".pwd")
				if err != nil {
					t.Fatal(err)
				}
				target := dir
				if !filepath.IsAbs(target) {
					target = filepath.Join(work, target)
				}
				// cd -L, the default, removes ".." lexically before it looks:
				// "/00/.." enters "/" though /00 does not exist.
				want, err := filepath.EvalSymlinks(filepath.Clean(target))
				if err != nil {
					t.Fatalf("chdir %q was entered, but resolving it here failed: %v", dir, err)
				}
				if got := strings.TrimSuffix(string(pwd), "\n"); got != want {
					t.Fatalf("chdir %q ran the shell in %q, want %q", dir, got, want)
				}
			}
		}

		// Nothing but the seeded directories and the recorder's files.
		entries, err := os.ReadDir(work)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if name := e.Name(); !slices.Contains(fuzzDirs, name) && name != "out" && name != "out.pwd" {
				t.Fatalf("%q left %q in the working directory: the line ran something", command, name)
			}
		}
	})
}
