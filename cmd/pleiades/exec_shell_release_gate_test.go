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

// This file is exec.shell's Release Gate: the real built binary, driven
// through init, add-host, add-credential and run, against a real
// openssh-server container, with real fail-closed host key verification
// and the credential coming from the store rather than from the runbook.
//
// It shares every helper with exec_command_release_gate_test.go, which is
// the same package. What it does NOT share is the claim being proven.
// That gate proves a metacharacter argument reaches the device as text.
// This one proves the opposite, on the same real device: that the
// metacharacters are interpreted, because that is the entire reason this
// method exists and the entire reason it is dangerous.
//
// The two gates asserting opposite outcomes from the same input is the
// strongest available evidence that the difference between the methods is
// real rather than a comment.
const (
	shellGateMarker  = "/tmp/pleiades-exec-shell-marker"
	shellGatePiped   = "/tmp/pleiades-exec-shell-piped"
	shellGateContent = "verified-by-exec-shell"
)

// TestCLI_RunExecutesExecShell is the gate. It runs a pipeline, a
// redirect and a variable expansion on a real device and reads the
// results back over a connection it opens itself.
func TestCLI_RunExecutesExecShell(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec.shell Release Gate container test in short mode")
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	// A real known_hosts holding the container's real key. The runbook
	// below sets no host key opt-out, so this run goes through the
	// fail-closed default.
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

	// Task 0 redirects, which exec.command cannot do at all: the ">" would
	// be an argument to echo. Its creates makes the second run a no-op.
	// Task 1 runs a real pipeline and writes the pipeline's OUTPUT, so the
	// file's contents prove the pipe was a pipe rather than text.
	runbook := filepath.Join(dir, "runbooks", "exec-shell.yaml")
	content := "id: exec-shell-release-gate\n" +
		"hosts: container1\n" +
		"tasks:\n" +
		"  - name: redirect-into-a-file\n" +
		"    fqcn: exec.shell\n" +
		"    params:\n" +
		"      cmd: \"echo " + shellGateContent + " > " + shellGateMarker + "\"\n" +
		"      creates: " + shellGateMarker + "\n" +
		"  - name: run-a-real-pipeline\n" +
		"    fqcn: exec.shell\n" +
		"    params:\n" +
		"      cmd: \"printf 'a\\\\nb\\\\nc\\\\n' | wc -l | tr -d ' ' > " + shellGatePiped + "\"\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/exec-shell.yaml")
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
		t.Errorf("first run: the redirect task reported %q, want %q", got, "changed")
	}
	if got := nodeStatus(t, out, 1); got != "changed" {
		t.Errorf("first run: the pipeline task reported %q, want %q", got, "changed")
	}

	// Proof on the device, over a connection this test opened itself.
	//
	// The redirect actually redirected: the file exists and holds the
	// value. Under exec.command the same line would have printed
	// "verified-by-exec-shell > /tmp/..." to stdout and created nothing.
	if got := verifyOverSSH(t, addr, "cat "+shellGateMarker); !strings.Contains(got, shellGateContent) {
		t.Fatalf("expected the redirect to have created %s on the device, got:\n%s", shellGateMarker, got)
	}

	// The pipeline actually piped. Three lines in, "3" out. Any failure to
	// interpret the pipe produces something else entirely: the literal
	// text, or printf's own output, or an error.
	if got := strings.TrimSpace(verifyOverSSH(t, addr, "cat "+shellGatePiped)); got != "3" {
		t.Fatalf("expected the pipeline to have written 3 to %s, got %q: the pipe was not interpreted as a pipe", shellGatePiped, got)
	}

	// The mtime before a second run, read off the device, so the creates
	// short-circuit can be proven by absence of a write rather than by the
	// platform's own claim about itself.
	before := strings.TrimSpace(verifyOverSSH(t, addr, "stat -c %Y "+shellGateMarker))

	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/exec-shell.yaml")
	if err != nil {
		t.Fatalf("second run failed: %v\n%s", err, out)
	}
	if got := nodeStatus(t, out, 0); got != "ok" {
		t.Errorf("second run: the redirect task reported %q, want %q: creates did not short-circuit it", got, "ok")
	}
	// The pipeline task carries no creates, so it cannot know its work was
	// already done and must still report changed. Without this, the case
	// above could pass for the wrong reason: a run that reported ok for
	// everything.
	if got := nodeStatus(t, out, 1); got != "changed" {
		t.Errorf("second run: the pipeline task reported %q, want %q", got, "changed")
	}

	after := strings.TrimSpace(verifyOverSSH(t, addr, "stat -c %Y "+shellGateMarker))
	if before != after {
		t.Errorf("the second run rewrote %s (mtime %s then %s), so creates did not actually short-circuit it", shellGateMarker, before, after)
	}
}

// TestCLI_RunExecShellReportsARealFailure proves the other half of the
// contract on a real device: a pipeline whose last command fails fails
// the task, with the exit status a shell would report.
//
// A separate test from the one above because a failing run aborts the
// DAG, so folding it in would hide whatever came after it.
func TestCLI_RunExecShellReportsARealFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec.shell Release Gate container test in short mode")
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

	runbook := filepath.Join(dir, "runbooks", "exec-shell-fail.yaml")
	content := "id: exec-shell-failure\n" +
		"hosts: container1\n" +
		"tasks:\n" +
		"  - name: a-pipeline-that-fails\n" +
		"    fqcn: exec.shell\n" +
		"    params:\n" +
		"      cmd: \"echo upstream | (echo why-it-failed >&2; exit 7)\"\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/exec-shell-fail.yaml")
	if err == nil {
		t.Fatalf("expected the run to fail, got success:\n%s", out)
	}
	// The status a shell reports for a pipeline is its LAST command's, so
	// 7 here also proves the pipeline ran as a pipeline rather than as one
	// command with literal arguments.
	if !strings.Contains(out, "exited 7") {
		t.Errorf("expected the run to carry the pipeline's exit status, got:\n%s", out)
	}
	if !strings.Contains(out, "why-it-failed") {
		t.Errorf("expected the run to carry what the command said, got:\n%s", out)
	}
}
