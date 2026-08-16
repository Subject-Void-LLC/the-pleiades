package main_test

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// nodeStatusPattern matches one line of the per-node summary
// cmd/pleiades/run.go prints, which reads
// "  tasks[0] [<device id>]: changed". The device id is a generated
// UUID, so the node index is the only stable way to name a task here.
var nodeStatusPattern = regexp.MustCompile(`(?m)^\s*tasks\[(\d+)\][^:]*: *(.+)$`)

// nodeStatus returns the status the run reported for the task at index,
// failing the test when the run printed no line for it at all.
//
// Matching on the index rather than on the task's name is not a
// shortcut: the name appears only in the plan section above, so a test
// that grepped for it would pass on a run where the task was listed and
// never executed.
func nodeStatus(t *testing.T, out string, index int) string {
	t.Helper()
	for _, m := range nodeStatusPattern.FindAllStringSubmatch(out, -1) {
		if m[1] == strconv.Itoa(index) {
			return strings.TrimSpace(m[2])
		}
	}
	t.Fatalf("the run reported no outcome for tasks[%d]:\n%s", index, out)
	return ""
}

// This file is Phase 38's Release Gate for exec.command, the first
// write-capable method in the catalog.
//
// The bar that phase sets is explicit: a status moves to implemented
// only after the module runs against a real device, and flipping it on
// the strength of a unit test that mocks the transport is "the theater
// RULE 0 names." So nothing here is mocked. The device is a real,
// independently-implemented sshd (lscr.io/linuxserver/openssh-server,
// the same image and recipe internal/transport/ssh's own container tests
// already proved works in this environment). The client is the real
// built pleiades binary, driven through init, add-host, add-credential
// and run exactly as a person would. Host key verification is the real
// fail-closed path against a real known_hosts file. And every claim
// below is checked by asking the container itself over a second SSH
// connection this test opens, never by reading pleiades's own output.
//
// It shares startReleaseGateContainer, captureRealHostKey, verifyOverSSH
// and runPleiadesWithHome with ssh_release_gate_test.go rather than
// copying them: both live in package main_test, and the container recipe
// is the thing most worth having exactly one of.

// The paths this gate writes on the device. They are under /tmp because
// the container's account owns it, and they carry the method's name so a
// leftover from a killed run is identifiable.
const (
	execGateMarker   = "/tmp/pleiades-exec-command-marker"
	execGateInjected = "/tmp/pleiades-exec-command-injected"
	execGateContent  = "verified-by-exec-command"
)

// TestCLI_RunExecutesExecCommand is exec.command's Release Gate.
//
// It proves four things that together are what "implemented" means for
// this method, and it proves each of them on the device:
//
//  1. The method runs at all through the real dispatch path, with a
//     credential the CLI stored and resolved rather than one the runbook
//     names. That second half is new: until this phase the Walk tier
//     handed every Collection method an empty secret set, so no method
//     needing a credential could run from the CLI at all.
//  2. argv and stdin reach the device intact, with no shell involved.
//  3. creates makes it idempotent: the same runbook reports changed on
//     the first run and no change on the second, and the second run
//     leaves the file alone.
//  4. An argument full of shell metacharacters is an argument. Nothing
//     it contains runs.
func TestCLI_RunExecutesExecCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec.command Release Gate container test in short mode")
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	// A real known_hosts file holding the container's real host key, the
	// same way an operator's own would be populated after a first
	// legitimate connection. The runbook below sets no host key opt-out,
	// so the fail-closed default is what this run goes through.
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

	// The runbook names no credential and no connection detail. Both come
	// from what the CLI stored above, which is the point: a runbook file
	// is committed to version control and a password must never be in it.
	//
	// The first task writes through tee with the content piped as stdin,
	// so the file is created with no shell anywhere in the chain. The
	// second echoes an argument that is nothing but shell syntax.
	runbook := filepath.Join(dir, "runbooks", "exec-command.yaml")
	content := "id: exec-command-release-gate\n" +
		"hosts: container1\n" +
		"tasks:\n" +
		"  - name: write-the-marker\n" +
		"    fqcn: exec.command\n" +
		"    params:\n" +
		"      argv:\n" +
		"        - tee\n" +
		"        - " + execGateMarker + "\n" +
		"      stdin: \"" + execGateContent + "\\n\"\n" +
		"      creates: " + execGateMarker + "\n" +
		"  - name: prove-no-shell-interprets-an-argument\n" +
		"    fqcn: exec.command\n" +
		"    params:\n" +
		"      argv:\n" +
		"        - echo\n" +
		"        - \"; touch " + execGateInjected + "\"\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	// First run: both tasks do their work.
	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/exec-command.yaml")
	if err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run complete") {
		t.Fatalf("expected a successful first run, got:\n%s", out)
	}
	if strings.Contains(out, releaseGateSSHPassword) {
		t.Errorf("the stored password appeared in pleiades's own output:\n%s", out)
	}
	if got := nodeStatus(t, out, 0); got != "changed" {
		t.Errorf("first run: the marker task reported %q, want %q", got, "changed")
	}
	if got := nodeStatus(t, out, 1); got != "changed" {
		t.Errorf("first run: the echo task reported %q, want %q", got, "changed")
	}

	// Proof on the device, over a connection this test opened itself.
	// Reading the "changed" claim above is not the proof; this is.
	if got := verifyOverSSH(t, addr, "cat "+execGateMarker); !strings.Contains(got, execGateContent) {
		t.Fatalf("expected the marker written by exec.command to be readable on the device, got:\n%s", got)
	}

	// Nothing inside the metacharacter argument ran. If a shell had
	// interpreted it, this file would exist, and a runbook parameter would
	// have been remote command execution.
	if got := verifyOverSSH(t, addr, "test -e "+execGateInjected+" && echo INJECTED || echo clean"); !strings.Contains(got, "clean") {
		t.Fatalf("a shell interpreted an argument on the device and created %s: this is remote command injection", execGateInjected)
	}

	// The marker's modification time before the second run, read off the
	// device. Comparing it afterward is what proves the second run left
	// the file alone rather than rewriting it with identical content.
	before := strings.TrimSpace(verifyOverSSH(t, addr, "stat -c %Y "+execGateMarker))

	// Second run: identical runbook, identical everything. creates now
	// matches, so the marker task must report no change.
	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/exec-command.yaml")
	if err != nil {
		t.Fatalf("second run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run complete") {
		t.Fatalf("expected a successful second run, got:\n%s", out)
	}
	if got := nodeStatus(t, out, 0); got != "ok" {
		t.Errorf("second run: the marker task reported %q, want %q: creates is what makes a command idempotent, and a second changed means it did not short-circuit", got, "ok")
	}
	// The echo task carries no creates, so it has no way to know its work
	// was already done and must still report changed. Asserting that here
	// keeps the case above from passing for the wrong reason, which is
	// that this run reported ok for everything.
	if got := nodeStatus(t, out, 1); got != "changed" {
		t.Errorf("second run: the echo task reported %q, want %q: a command with no creates cannot know it already ran", got, "changed")
	}

	after := strings.TrimSpace(verifyOverSSH(t, addr, "stat -c %Y "+execGateMarker))
	if before != after {
		t.Errorf("the second run rewrote %s (mtime %s then %s), so creates did not actually short-circuit it", execGateMarker, before, after)
	}
	if got := verifyOverSSH(t, addr, "cat "+execGateMarker); !strings.Contains(got, execGateContent) {
		t.Errorf("the second run damaged the marker's contents: %s", got)
	}
}

// TestCLI_RunExecCommandReportsARealFailure proves the other half of the
// contract on a real device: a command that exits non-zero fails the
// task rather than being reported as a successful run with a status
// buried in a stat.
//
// It is a separate test from the one above because a failing run aborts
// the DAG, so folding it into that runbook would hide whatever came
// after it.
func TestCLI_RunExecCommandReportsARealFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec.command Release Gate container test in short mode")
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

	// cat on a path that does not exist is a command that genuinely runs
	// on the device and genuinely fails, rather than one this test made
	// fail by construction.
	runbook := filepath.Join(dir, "runbooks", "exec-command-failure.yaml")
	content := "id: exec-command-failure-gate\n" +
		"hosts: container1\n" +
		"tasks:\n" +
		"  - name: read-a-file-that-is-not-there\n" +
		"    fqcn: exec.command\n" +
		"    params:\n" +
		"      argv:\n" +
		"        - cat\n" +
		"        - /tmp/pleiades-exec-command-absent\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/exec-command-failure.yaml")
	if err == nil {
		t.Fatalf("expected a failing command to fail the run, got a success:\n%s", out)
	}
	if !strings.Contains(out, "FAILED") {
		t.Errorf("expected the task to be reported as failed, got:\n%s", out)
	}
	// The operator has to be able to act on this without opening a log
	// viewer, so the exit status and what the command said both belong in
	// the message.
	if !strings.Contains(out, "exited 1") {
		t.Errorf("expected the failure to name the exit status, got:\n%s", out)
	}
	if !strings.Contains(out, "No such file") {
		t.Errorf("expected the failure to carry what the command wrote to stderr, got:\n%s", out)
	}
}
