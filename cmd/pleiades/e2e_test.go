// Package main_test drives the actual built pleiades binary through a
// subprocess, the same way a user invokes it. RULE 0 (AGENTS.md, the
// project handoff) requires this: calling run() in-process would test the
// dispatch logic, not the CLI a user actually runs.
package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "pleiades-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath = filepath.Join(tmpDir, "pleiades")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build pleiades binary: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func runPleiades(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestCLI_EndToEnd walks init -> add-host -> validate -> run against the
// real binary in a real temp directory, asserting on real files and real
// stdout, with no server, database, or broker running (Part 0 Phase W1's
// Release Gate condition).
func TestCLI_EndToEnd(t *testing.T) {
	dir := t.TempDir()

	out, err := runPleiades(t, dir, "init")
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	for _, f := range []string{"inventory.yaml", "runbooks/sample.yaml", "README.md"} {
		if _, statErr := os.Stat(filepath.Join(dir, f)); statErr != nil {
			t.Errorf("expected init to create %s: %v", f, statErr)
		}
	}

	out, err = runPleiades(t, dir, "add-host", "webserver1", "--type", "linux_server", "--set", "host=10.0.0.5")
	if err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}
	invData, err := os.ReadFile(filepath.Join(dir, "inventory.yaml"))
	if err != nil {
		t.Fatalf("failed to read inventory.yaml: %v", err)
	}
	if !strings.Contains(string(invData), "webserver1") {
		t.Errorf("expected inventory.yaml to contain the added host, got:\n%s", invData)
	}

	out, err = runPleiades(t, dir, "validate")
	if err != nil {
		t.Fatalf("validate failed on the scaffolded sample runbook: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no issues found") {
		t.Errorf("expected a clean validate report, got:\n%s", out)
	}

	out, err = runPleiades(t, dir, "run", "runbooks/sample.yaml")
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "plan for") {
		t.Errorf("expected run to print a plan, got:\n%s", out)
	}

	// Adding a duplicate host must fail, not silently double the entry.
	if _, err := runPleiades(t, dir, "add-host", "webserver1", "--type", "linux_server"); err == nil {
		t.Error("expected add-host to reject a duplicate host name")
	}
}

// TestCLI_ValidateRejectsMissingCapability is Phase W3's Release Gate run
// through the real binary end to end: a runbook targeting a device that
// lacks the required capability must be rejected with an actionable
// message, and the process must exit non-zero.
func TestCLI_ValidateRejectsMissingCapability(t *testing.T) {
	dir := t.TempDir()

	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "webserver1", "--type", "linux_server"); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}

	runbook := filepath.Join(dir, "runbooks", "needs_ios.yaml")
	content := "id: needs-ios\ntasks:\n  - name: backup\n    fqcn: ios_backup\n    params:\n      target: webserver1\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiades(t, dir, "validate", "runbooks/needs_ios.yaml")
	if err == nil {
		t.Fatalf("expected validate to reject the runbook, got success:\n%s", out)
	}
	if !strings.Contains(out, "webserver1") || !strings.Contains(out, "CiscoIOSCapable") {
		t.Errorf("expected an actionable message naming the device and capability, got:\n%s", out)
	}
}

// TestCLI_RunExecutesConditionalBranch is Part 0 Phase W5's own Release
// Gate, run through the real binary end to end: a multi-node workflow
// with a conditional edge executes locally and takes the correct branch.
// "precheck" registers a stat; "reboot"'s when_cel reads it and is true,
// so it must run and report changed; "skip-me"'s when_cel reads the same
// stat and is false, so it must be skipped, never executed at all. No
// server, database, or broker is running (the same Walk-tier constraint
// TestCLI_NoInfrastructure already exercises for validate).
func TestCLI_RunExecutesConditionalBranch(t *testing.T) {
	dir := t.TempDir()

	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	runbook := filepath.Join(dir, "runbooks", "conditional.yaml")
	content := "id: conditional-demo\n" +
		"tasks:\n" +
		"  - name: precheck\n" +
		"    fqcn: noop\n" +
		"    register: precheck\n" +
		"    params:\n" +
		"      needs_reboot: true\n" +
		"  - name: reboot\n" +
		"    fqcn: noop\n" +
		"    when_cel: 'stat.precheck[\"\"].needs_reboot == true'\n" +
		"    params:\n" +
		"      changed: true\n" +
		"  - name: skip-me\n" +
		"    fqcn: noop\n" +
		"    when_cel: 'stat.precheck[\"\"].needs_reboot == false'\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiades(t, dir, "run", "runbooks/conditional.yaml")
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}

	if !strings.Contains(out, "tasks[1]: changed") {
		t.Errorf("expected reboot (tasks[1]) to run and report changed, got:\n%s", out)
	}
	if !strings.Contains(out, "tasks[2]: skipped") {
		t.Errorf("expected skip-me (tasks[2]) to be skipped, got:\n%s", out)
	}
	if !strings.Contains(out, "run complete") {
		t.Errorf("expected a successful run to print run complete, got:\n%s", out)
	}
}

// TestCLI_AddCredential exercises add-credential through the real binary:
// a stored password never appears in cleartext anywhere in the credentials
// file it writes (internal/credential's AES-256-GCM encryption is real,
// not merely gitignored plaintext), re-running it for the same device
// updates rather than duplicates the entry, and --password/--key are
// enforced as mutually exclusive.
func TestCLI_AddCredential(t *testing.T) {
	dir := t.TempDir()

	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "webserver1", "--type", "linux_server", "--set", "host=10.0.0.5"); err != nil {
		t.Fatalf("add-host failed: %v\n%s", err, out)
	}

	const secret = "correct-horse-battery-staple"
	out, err := runPleiades(t, dir, "add-credential", "webserver1", "--username", "deploy", "--password", secret)
	if err != nil {
		t.Fatalf("add-credential failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "webserver1") {
		t.Errorf("expected confirmation to name the device, got:\n%s", out)
	}

	credData, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatalf("failed to read credentials.yaml: %v", err)
	}
	if strings.Contains(string(credData), secret) {
		t.Errorf("credentials.yaml must never contain the cleartext secret, got:\n%s", credData)
	}
	if !strings.Contains(string(credData), "deploy") {
		t.Errorf("expected the plaintext username to be stored, got:\n%s", credData)
	}

	keyData, err := os.ReadFile(filepath.Join(dir, ".pleiades", "master.key"))
	if err != nil {
		t.Fatalf("expected add-credential to generate a master key file: %v", err)
	}
	if len(keyData) == 0 {
		t.Error("master.key must not be empty")
	}

	// Re-running for the same device updates the entry rather than
	// duplicating it: the file must still contain exactly one "deploy"
	// occurrence (the username line) after a second save.
	const secondSecret = "a-different-password"
	if out, err := runPleiades(t, dir, "add-credential", "webserver1", "--username", "deploy", "--password", secondSecret); err != nil {
		t.Fatalf("second add-credential failed: %v\n%s", err, out)
	}
	credData, err = os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatalf("failed to re-read credentials.yaml: %v", err)
	}
	if strings.Count(string(credData), "webserver1") != 1 {
		t.Errorf("expected exactly one entry for webserver1 after an update, got:\n%s", credData)
	}

	if out, err := runPleiades(t, dir, "add-credential", "webserver1", "--username", "deploy", "--password", secret, "--key", "/dev/null"); err == nil {
		t.Errorf("expected --password and --key to be rejected as mutually exclusive, got success:\n%s", out)
	}
}

// TestCLI_NoInfrastructure is the literal Release Gate condition: this
// process runs with no server, database, or broker reachable, and every
// subcommand still works.
func TestCLI_NoInfrastructure(t *testing.T) {
	for _, env := range []string{"NATS_URL", "DATABASE_URL", "PLEIADES_CONTROLLER_ADDR"} {
		if os.Getenv(env) != "" {
			t.Skipf("%s is set in this environment; skipping to avoid a false pass", env)
		}
	}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init should succeed with no infrastructure reachable: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "validate"); err != nil {
		t.Fatalf("validate should succeed with no infrastructure reachable: %v\n%s", err, out)
	}
}

// TestCLI_RunReportsSetMetadata exercises "set_metadata" through the real
// binary: a task reporting custom automation statistics shows up in a
// final "metadata:" report section, the reporting surface the project
// owner asked for ("dynamic metadata will allow for reporting of custom
// automation statistics").
func TestCLI_RunReportsSetMetadata(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	runbook := filepath.Join(dir, "runbooks", "metadata.yaml")
	content := "id: metadata-demo\n" +
		"tasks:\n" +
		"  - name: report\n" +
		"    fqcn: set_metadata\n" +
		"    register: summary\n" +
		"    params:\n" +
		"      data:\n" +
		"        devices_patched: 3\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	out, err := runPleiades(t, dir, "run", "runbooks/metadata.yaml")
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "metadata:") {
		t.Errorf("expected a metadata: report section, got:\n%s", out)
	}
	if !strings.Contains(out, "summary:") {
		t.Errorf("expected the metadata report to name the register, got:\n%s", out)
	}
	if !strings.Contains(out, "devices_patched: 3") {
		t.Errorf("expected the metadata report to include the authored data, got:\n%s", out)
	}
}

// TestCLI_RunMasksSecretFields exercises secret_fields through the real
// binary: a value marked secret must never appear in cleartext anywhere
// in the CLI's own printed output, including a later, unrelated task's
// own failure message that happens to echo it back, only the mask
// placeholder should.
func TestCLI_RunMasksSecretFields(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	const secret = "sup3r-secret-password"
	runbook := filepath.Join(dir, "runbooks", "secret.yaml")
	content := "id: secret-demo\n" +
		"tasks:\n" +
		"  - name: mark-secret\n" +
		"    fqcn: noop\n" +
		"    register: creds\n" +
		"    secret_fields: [password]\n" +
		"    params:\n" +
		"      password: \"" + secret + "\"\n" +
		"  - name: leak-secret\n" +
		"    fqcn: noop\n" +
		"    params:\n" +
		"      target: \"" + secret + "\"\n"
	if err := os.WriteFile(runbook, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture runbook: %v", err)
	}

	// leak-secret's target resolves to no device, so this run is expected
	// to fail (a non-zero exit is not the point under test; the printed
	// output is).
	out, _ := runPleiades(t, dir, "run", "runbooks/secret.yaml")
	if strings.Contains(out, secret) {
		t.Errorf("expected the raw secret to never appear in CLI output, got:\n%s", out)
	}
	if !strings.Contains(out, "********") {
		t.Errorf("expected the mask placeholder to appear in CLI output, got:\n%s", out)
	}
}
