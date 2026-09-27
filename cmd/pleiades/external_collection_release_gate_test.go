// Package main_test: the Release Gate for external Collections
// (IMPLEMENTATION.md Phases 42 and 45, the payload the user chose on
// 2026-09-18: a separate program run beside The Pleiades, never copied onto a
// device).
//
// A real program, examples/external_collection, is built from source the
// way a third party would build it, installed into a directory named by
// PLEIADES_COLLECTIONS_DIR, and then driven entirely through the real
// pleiades binary against a real, independently implemented sshd. Every
// claim about the device is checked over a connection this test opens
// itself. The method it provides, example.note.write, supports check mode,
// so the same run also proves a check crosses the process boundary as a
// check.
package main_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// externalGatePath is the file the gate's runbook manages on the device.
const externalGatePath = "/tmp/pleiades-external-collection-gate.txt"

// externalGateContent is what the runbook asks the file to hold.
const externalGateContent = "managed by an external collection"

// runPleiadesWithEnv runs the real binary in dir with the given variables
// replacing any of the same name in this process's environment.
func runPleiadesWithEnv(t *testing.T, dir string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, replaced := env[key]; !replaced {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildExampleCollection builds examples/external_collection into a fresh
// directory Load will accept, and returns the directory.
func buildExampleCollection(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "note"), "../../examples/external_collection")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the example external Collection: %v\n%s", err, out)
	}
	return dir
}

// TestCLI_ExternalCollectionRunsAgainstARealDevice is the Release Gate.
func TestCLI_ExternalCollectionRunsAgainstARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the external Collection Release Gate container test in short mode")
	}

	collections := buildExampleCollection(t)
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", sshDir, err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}
	env := map[string]string{"HOME": homeDir, "PLEIADES_COLLECTIONS_DIR": collections}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "container1", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "container1", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%s failed: %v\n%s", args[0], err, out)
		}
	}

	// Nothing an operator has not approved runs, not even to describe
	// itself: the command refuses, naming the approve command.
	refused, err := runPleiadesWithEnv(t, dir, env, "doc", "example.note.write")
	if err == nil || !strings.Contains(refused, "is not approved to run") || !strings.Contains(refused, "pleiades collection approve note") {
		t.Fatalf("an unapproved program was loaded: %v\n%s", err, refused)
	}
	// Approving shows what the build says it provides before recording it.
	approved, err := runPleiadesWithEnv(t, dir, env, "collection", "approve", "note", "--yes")
	if err != nil {
		t.Fatalf("collection approve failed: %v\n%s", err, approved)
	}
	for _, want := range []string{"digest:  sha256:", "example.note.write", "check mode: supported", "approved note (sha256:"} {
		if !strings.Contains(approved, want) {
			t.Errorf("collection approve did not show %q:\n%s", want, approved)
		}
	}
	if listed, err := runPleiadesWithEnv(t, dir, env, "collection", "list"); err != nil || !strings.Contains(listed, "note  sha256:") {
		t.Errorf("collection list does not show the approval: %v\n%s", err, listed)
	}

	// The reference reader sees the method, says where it came from, and
	// reports its check support.
	out, err := runPleiadesWithEnv(t, dir, env, "doc", "example.note.write")
	if err != nil {
		t.Fatalf("doc failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "provided by: external Collection "+filepath.Join(collections, "note")) || !strings.Contains(out, "sha256:") {
		t.Errorf("doc did not name the program and digest that provide the method:\n%s", out)
	}
	if !strings.Contains(out, "check mode          supported") {
		t.Errorf("doc did not report the method's check support:\n%s", out)
	}

	runbook := "id: external-collection-release-gate\n" +
		"tasks:\n" +
		"  - name: leave-a-note\n" +
		"    fqcn: example.note.write\n" +
		"    params:\n" +
		"      target: container1\n" +
		"      path: " + externalGatePath + "\n" +
		"      content: \"" + externalGateContent + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "note.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatalf("writing the runbook: %v", err)
	}

	if out, err := runPleiadesWithEnv(t, dir, env, "validate", "runbooks/note.yaml"); err != nil {
		t.Fatalf("validate refused a runbook calling the external method: %v\n%s", err, out)
	}

	// outputs keeps every run's output, so the leak check at the end
	// covers all of them rather than only the last.
	var outputs []string

	fileOnDevice := func() string {
		return strings.TrimSpace(verifyOverSSH(t, addr, "cat "+externalGatePath+" 2>/dev/null || echo ABSENT"))
	}

	// A check: the method is asked, across the process boundary, and
	// writes nothing.
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/note.yaml", "--mode", "check", "--verbose")
	if err != nil {
		t.Fatalf("the check failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "provided by: "+filepath.Join(collections, "note")+" (sha256:") {
		t.Errorf("the result does not say which program produced it:\n%s", out)
	}
	outputs = append(outputs, out)
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("expected the check to predict writing the note, got %q", l)
	}
	if !strings.Contains(out, "check complete: nothing was changed") {
		t.Errorf("a check with every task checkable did not complete cleanly:\n%s", out)
	}
	if got := fileOnDevice(); got != "ABSENT" {
		t.Fatalf("the check wrote the file on the device: %q", got)
	}

	// The real run writes it, which is the control for the check above.
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/note.yaml")
	if err != nil {
		t.Fatalf("the real run failed: %v\n%s", err, out)
	}
	outputs = append(outputs, out)
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, ": changed") {
		t.Errorf("expected the real run to report a change, got %q", l)
	}
	if got := fileOnDevice(); got != externalGateContent {
		t.Fatalf("the device holds %q after the real run, want %q", got, externalGateContent)
	}
	// The run journal says whose code did the work.
	if journal, _ := journalText(t, dir); !strings.Contains(journal, `"provider_program":"`+filepath.Join(collections, "note")+`"`) || !strings.Contains(journal, `"provider_digest":"sha256:`) {
		t.Errorf("the journal does not name the program and digest that wrote the note:\n%s", journal)
	}

	// And converges: a second run finds nothing to do.
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/note.yaml")
	if err != nil {
		t.Fatalf("the second run failed: %v\n%s", err, out)
	}
	outputs = append(outputs, out)
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, ": ok") {
		t.Errorf("expected the second run to report no change, got %q", l)
	}

	for _, run := range outputs {
		if strings.Contains(run, releaseGateSSHPassword) {
			t.Error("the stored password appeared in a command's output")
		}
	}

	// Granting every program read access to the project directory would
	// hand it the project's credential store, so the command refuses it,
	// naming why, and runs nothing.
	granted := map[string]string{"PLEIADES_COLLECTIONS_READ_PATHS": dir}
	for k, v := range env {
		granted[k] = v
	}
	out, err = runPleiadesWithEnv(t, dir, granted, "run", "runbooks/note.yaml")
	if err == nil || !strings.Contains(out, "holds credentials") {
		t.Fatalf("a read grant covering the project's credential store was accepted: %v\n%s", err, out)
	}

	// A directory someone else could write to is refused outright, and
	// the command stops rather than running without the method.
	if err := os.Chmod(collections, 0o777); err != nil { // #nosec G302 -- the fixture under test
		t.Fatal(err)
	}
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/note.yaml")
	if err == nil || !strings.Contains(out, "group- or world-writable") {
		t.Fatalf("a world-writable collections directory was accepted: %v\n%s", err, out)
	}
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
}
