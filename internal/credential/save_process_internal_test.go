// Tests of the credential file lock between real processes, which is what
// several pleiades commands run at once are: writers released together each
// keep their entry, and a holder that dies leaves no lock behind.
//
// The other processes are this test binary, started again with
// credentialHelper set; TestMain runs the helper and exits before any test
// does, so a helper is never a test of its own.
package credential

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The environment a helper process is told its work through.
const (
	// credentialHelper names the helper to be: helperHoldLock or helperSaveOne.
	credentialHelper = "PLEIADES_TEST_CREDENTIAL_HELPER"
	// credentialHelperDir is the project directory it works in.
	credentialHelperDir = "PLEIADES_TEST_CREDENTIAL_HELPER_DIR"
	// credentialHelperName is the device a helperSaveOne saves.
	credentialHelperName = "PLEIADES_TEST_CREDENTIAL_HELPER_NAME"

	helperHoldLock = "hold-lock"
	helperSaveOne  = "save-one"
)

// TestMain lets this package's test binary double as another process using
// the credential file: started with credentialHelper set it does that one
// job and exits, before the testing package reads a flag (the pattern of
// internal/adapters/native's TestMain).
func TestMain(m *testing.M) {
	if helper := os.Getenv(credentialHelper); helper != "" {
		os.Exit(runCredentialHelper(helper, os.Getenv(credentialHelperDir)))
	}
	os.Exit(m.Run())
}

// runCredentialHelper is the helper process's whole life, returning its exit
// status. Each helper says one line on stdout when it is in place and then
// waits for its stdin to close, which is how its parent releases it.
func runCredentialHelper(helper, dir string) int {
	switch helper {
	case helperHoldLock:
		// Takes the lock and holds it, never releasing it: the parent kills
		// this process, or closes its stdin, and the system lets the lock go.
		if _, err := lockCredentials(context.Background(), dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("holding the lock")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case helperSaveOne:
		// Ready, then one save the moment the parent lets every writer go.
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		if err := SaveFileStore(dir, lockTestKey, os.Getenv(credentialHelperName), Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown helper %q\n", helper)
	return 2
}

// helperProcess is one started helper.
type helperProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr *bytes.Buffer
}

// startHelper starts this test binary as helper in dir and registers its
// end with t: whatever the test does, the helper's stdin is closed, which
// ends it, and it is killed and waited for.
func startHelper(t *testing.T, helper, dir string, env ...string) *helperProcess {
	t.Helper()
	// #nosec G204 -- the test binary itself, re-run as a helper.
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(append(os.Environ(), credentialHelper+"="+helper, credentialHelperDir+"="+dir), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &helperProcess{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout), stderr: &bytes.Buffer{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.stdin.Close()
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	})
	return p
}

// waitFor reads the helper's stdout until it says line.
func (p *helperProcess) waitFor(t *testing.T, line string) {
	t.Helper()
	for p.stdout.Scan() {
		if strings.Contains(p.stdout.Text(), line) {
			return
		}
	}
	t.Fatalf("the helper process ended without saying %q: %s", line, p.stderr.String())
}

// TestSave_SeparateProcessesEachKeepTheirEntry: several processes each save a
// different device's credential, as parallel add-credential commands do, and
// every device is in the file afterwards. Each process says when it is up and
// then waits, and all are let go together, so they read the file at once
// however unevenly they started. A lock inside one process would pass the
// goroutine test (save_concurrent_test.go) and still lose these.
func TestSave_SeparateProcessesEachKeepTheirEntry(t *testing.T) {
	const writers = 10
	for round := range 3 {
		dir := t.TempDir()
		procs := make([]*helperProcess, writers)
		for i := range procs {
			// A Start that fails ends the test, and each helper already
			// started is ended and waited for by its own cleanup.
			procs[i] = startHelper(t, helperSaveOne, dir, credentialHelperName+"="+fmt.Sprintf("web%02d", i))
		}
		for _, p := range procs {
			p.waitFor(t, "ready")
		}
		for _, p := range procs {
			if err := p.stdin.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for i, p := range procs {
			if err := p.cmd.Wait(); err != nil {
				t.Fatalf("round %d, writer %d: %v\n%s", round, i, err, p.stderr)
			}
		}
		listed, err := ListCredentials(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != writers {
			t.Fatalf("round %d: %d processes each saved a device and %d are stored: %v", round, writers, len(listed), listed)
		}
	}
}

// TestSaveLock_AHolderThatDiesLeavesNoLockBehind: another process holds the
// lock, so a save here is refused; that process is then killed without ever
// releasing it, and the next save, waiting as long as any save does, goes
// through. The lock file is still there and holds nobody back, which a lock
// that was only a file's existence could not say.
func TestSaveLock_AHolderThatDiesLeavesNoLockBehind(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, masterKeyDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	holder := startHelper(t, helperHoldLock, dir)
	holder.waitFor(t, "holding the lock")

	save := func() error {
		return SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"})
	}
	// A short wait for the refusal only; the save after the kill waits the
	// real bound, so a slow machine releasing the dead holder's lock a moment
	// late does not fail it.
	usual := credentialsLockWait
	credentialsLockWait = 100 * time.Millisecond
	err := save()
	credentialsLockWait = usual
	if !errors.Is(err, ErrCredentialsBusy) {
		t.Fatalf("save while another process holds the lock: got %v, want ErrCredentialsBusy", err)
	}

	if err := holder.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.cmd.Wait()

	if _, err := os.Stat(credentialsLockPath(dir)); err != nil {
		t.Fatalf("the lock file should still be there after its holder died: %v", err)
	}
	if err := save(); err != nil {
		t.Fatalf("save after the holder was killed: %v", err)
	}
}
