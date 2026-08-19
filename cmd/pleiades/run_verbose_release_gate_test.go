package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// The value the task below asks the device to print. It is distinctive
// enough that finding it in the CLI's output cannot be a coincidence,
// and it is produced BY the device rather than written into the runbook
// twice, so a run that never connected cannot produce it.
const verboseGateEcho = "pleiades-verbose-gate-8f2c"

// TestCLI_RunVerbosePrintsASuccessfulTasksOutput proves `run --verbose`
// reports what a task actually produced, and that the default run does
// not.
//
// The gap this closes was not cosmetic. A successful task's output was
// unreachable from the CLI: run printed "ok" or "changed" and nothing
// else, while the failure path printed the error, and a Collection
// method's error carries its stdout. The consequence was visible in this
// repository's own tests and examples, several of which ended a task
// with a deliberate non-zero exit purely so the output would be printed
// at all. A test that has to break the thing it is observing is
// measuring the failure path, not the one that ships.
//
// Both halves are asserted, and the negative half matters as much as the
// positive one: if the default run printed stats too, this test would
// pass on the strength of a flag that does nothing.
//
// Real device, real transport, real binary, per RULE 0: the value
// asserted on comes back from a command that ran inside the container,
// over the same SSH path any other run uses.
func TestCLI_RunVerbosePrintsASuccessfulTasksOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping run --verbose release gate container test in short mode")
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", sshDir, err)
	}
	knownHostsLine := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	dir := t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-host", "container1", "--type", "linux_server",
		"--set", "host="+host, "--set", "port="+strconv.Itoa(port)); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "add-credential", "container1",
		"--username", releaseGateSSHUser, "--password", releaseGateSSHPassword); err != nil {
		t.Fatalf("add-credential failed: %v\n%s", err, out)
	}

	// Two lines, because a multi-line stdout is the case the printer has
	// to get right and the one a single-line value would not exercise.
	runbook := filepath.Join(dir, "runbooks", "verbose.yaml")
	content := "id: verbose-gate\n" +
		"hosts: container1\n" +
		"tasks:\n" +
		"  - name: read-something-back-from-the-device\n" +
		"    exec.command:\n" +
		"      argv:\n" +
		"        - printf\n" +
		"        - \"" + verboseGateEcho + "\\nsecond-line\\n\"\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	quiet, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/verbose.yaml")
	if err != nil {
		t.Fatalf("default run failed: %v\n%s", err, quiet)
	}
	if !strings.Contains(quiet, "run complete") {
		t.Fatalf("expected a successful default run, got:\n%s", quiet)
	}
	if strings.Contains(quiet, verboseGateEcho) {
		t.Errorf("the default run printed the task's stdout; --verbose is then meaningless:\n%s", quiet)
	}

	loud, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/verbose.yaml", "--verbose")
	if err != nil {
		t.Fatalf("verbose run failed: %v\n%s", err, loud)
	}
	if !strings.Contains(loud, "run complete") {
		t.Fatalf("expected a successful verbose run, got:\n%s", loud)
	}
	if !strings.Contains(loud, verboseGateEcho) {
		t.Errorf("--verbose did not print the device's own answer:\n%s", loud)
	}
	if !strings.Contains(loud, "second-line") {
		t.Errorf("--verbose printed only the first line of a multi-line stdout:\n%s", loud)
	}
	if !strings.Contains(loud, "rc: 0") {
		t.Errorf("--verbose printed no exit status for a successful task:\n%s", loud)
	}
	if strings.Contains(loud, releaseGateSSHPassword) {
		t.Errorf("the stored password appeared in the verbose output:\n%s", loud)
	}

	// -v is documented as the shorthand, so it has to actually be one.
	short, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/verbose.yaml", "-v")
	if err != nil {
		t.Fatalf("-v run failed: %v\n%s", err, short)
	}
	if !strings.Contains(short, verboseGateEcho) {
		t.Errorf("-v did not behave as --verbose:\n%s", short)
	}
}
