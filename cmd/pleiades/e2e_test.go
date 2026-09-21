// Package main_test drives the actual built pleiades binary through a
// subprocess, the same way a user invokes it. RULE 0 (AGENTS.md, the
// project handoff) requires this: calling run() in-process would test the
// dispatch logic, not the CLI a user actually runs.
package main_test

import (
	"errors"
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

// runPleiadesWithStdin is runPleiades with something on standard input, for
// the commands that read a secret from a pipe rather than from a flag.
//
// It exists because a secret on a command line is visible in this process's
// argument list to anything else on the machine, which is the property the
// --*-stdin flags exist to preserve. A test that reached for a flag value
// instead would be exercising a path the platform deliberately does not
// offer.
func runPleiadesWithStdin(t *testing.T, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
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
// server, database, or broker is running (the same Crawl-tier constraint
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

// TestCLI_RunMasksRegisterMask exercises register_mask through the real
// binary: a value marked secret must never appear in cleartext anywhere
// in the CLI's own printed output, including a later, unrelated task's
// own failure message that happens to echo it back, only the mask
// placeholder should.
func TestCLI_RunMasksRegisterMask(t *testing.T) {
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
		"    register_mask: [password]\n" +
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

// exitCode extracts a subprocess's real exit code from the error
// CombinedOutput returns, failing the test if the process never ran at
// all (as opposed to running and exiting non-zero, which is not an error
// here).
func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	t.Fatalf("pleiades failed to run at all: %v", err)
	return -1
}

// TestCLI_ForgeHelp is Phase 30's own Release Gate: "pleiades forge --help
// lists its subcommands." Phase 33 adds the first two real subcommands
// (new-device, new-collection), so the assertion moved from "the usage
// block honestly says nothing is registered yet" to "the usage block
// actually lists what is now registered."
func TestCLI_ForgeHelp(t *testing.T) {
	dir := t.TempDir()
	out, err := runPleiades(t, dir, "forge", "--help")
	if err != nil {
		t.Fatalf("forge --help should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "usage: pleiades forge") {
		t.Errorf("expected forge --help to print its own usage block, got:\n%s", out)
	}
	for _, want := range []string{"new-device", "new-collection"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected forge --help to list %q, got:\n%s", want, out)
		}
	}
}

// TestCLI_ForgeUnknownSubcommand is the other half of Phase 30's Release
// Gate: "pleiades forge bogus fails with the same shape as an unknown
// top-level command," verified against the real built binary, not a
// mock. Compares directly against the real top-level "pleiades bogus"
// case rather than asserting a hardcoded string twice, so a future change
// to one shape without the other would fail this test.
func TestCLI_ForgeUnknownSubcommand(t *testing.T) {
	dir := t.TempDir()

	topOut, topErr := runPleiades(t, dir, "bogus")
	topCode := exitCode(t, topErr)
	if !strings.Contains(topOut, `unknown command "bogus"`) {
		t.Fatalf("expected top-level unknown command message, got:\n%s", topOut)
	}
	if !strings.Contains(topOut, "usage: pleiades") {
		t.Fatalf("expected top-level unknown command to print usage, got:\n%s", topOut)
	}

	forgeOut, forgeErr := runPleiades(t, dir, "forge", "bogus")
	forgeCode := exitCode(t, forgeErr)
	if !strings.Contains(forgeOut, `unknown command "bogus"`) {
		t.Errorf("expected forge unknown command message, got:\n%s", forgeOut)
	}
	if !strings.Contains(forgeOut, "usage: pleiades forge") {
		t.Errorf("expected forge unknown command to print its own usage, got:\n%s", forgeOut)
	}
	if forgeCode != topCode {
		t.Errorf("pleiades forge bogus exited %d, want the same shape as pleiades bogus (%d)", forgeCode, topCode)
	}
	if forgeCode != 2 {
		t.Errorf("pleiades forge bogus exited %d, want 2 (matching an unknown top-level command)", forgeCode)
	}
}

// repoRoot locates the module root from this test file's own package
// directory (cmd/pleiades), so the forge new-* release gate tests below
// can point the real binary's --dir default (".") at the actual
// repository checkout: a generated package imports internal/ paths, so
// unlike TestCLI_EndToEnd's throwaway t.TempDir() project, it can only
// go build/go test successfully from inside this module.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func runGoBuildAndTest(t *testing.T, dir, pkgImportPath string) {
	t.Helper()
	for _, subcmd := range []string{"build", "test"} {
		cmd := exec.Command("go", subcmd, pkgImportPath)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s %s failed: %v\n%s", subcmd, pkgImportPath, err, out)
		}
	}
}

// TestCLI_ForgeNewDevice_EndToEnd is Phase 33's Release Gate, driven
// through the actual built binary rather than devicescaffold's own
// in-process tests: "pleiades forge new-device" writes a package that go
// builds and whose generated test passes. (End-to-end registration
// through record.RegisterType is separately proven, in more depth, by
// internal/inventory/devicescaffold's own release_gate_test.go; this test
// exists to prove the real CLI surface, not just the library it calls.)
func TestCLI_ForgeNewDevice_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root := repoRoot(t)
	vendor := fmt.Sprintf("e2egate%d", os.Getpid())
	typeKey := vendor + "_widget"

	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(root, "internal", "inventory", "devices", vendor))
	})

	out, err := runPleiades(t, root, "forge", "new-device", vendor,
		"--type", typeKey, "--capabilities", "SSHTransportCapable")
	if err != nil {
		t.Fatalf("forge new-device failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not yet reachable from the stock binary") {
		t.Errorf("expected the composition-root honesty note in output, got:\n%s", out)
	}

	sourcePath := filepath.Join(root, "internal", "inventory", "devices", vendor, "widget.go")
	if _, statErr := os.Stat(sourcePath); statErr != nil {
		t.Fatalf("expected %s to exist: %v", sourcePath, statErr)
	}

	pkgImportPath := "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/" + vendor
	runGoBuildAndTest(t, root, pkgImportPath)
}

// TestCLI_ForgeNewCollection_EndToEnd is TestCLI_ForgeNewDevice_EndToEnd's
// counterpart for "pleiades forge new-collection", proving the real CLI
// surface produces a package that go builds and whose generated test
// (including its own collection.Lookup registration check) passes.
func TestCLI_ForgeNewCollection_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release gate test in -short mode")
	}

	root := repoRoot(t)
	name := fmt.Sprintf("test.e2egate%d.check", os.Getpid())

	// Remove only this test's own pid-namespaced package, never the shared
	// internal/catalog/test parent. tools/gencatalog's dogfood test writes
	// a sibling directory under the same parent, and a cleanup that took
	// the parent would delete that test's package while it was still
	// building it, which under a parallel `go test ./...` showed up as an
	// unexplainable "no required module provides package" failure in
	// whichever of the two happened to lose the race.
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(root, "internal", "catalog", "test", fmt.Sprintf("e2egate%d", os.Getpid())))
	})

	out, err := runPleiades(t, root, "forge", "new-collection", name,
		"--capabilities", "SSHTransportCapable", "--transports", "ssh")
	if err != nil {
		t.Fatalf("forge new-collection failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "declared but not implemented") {
		t.Errorf("expected the reachability honesty note in output, got:\n%s", out)
	}

	sourcePath := filepath.Join(root, "internal", "catalog", "test", fmt.Sprintf("e2egate%d", os.Getpid()), "check.go")
	if _, statErr := os.Stat(sourcePath); statErr != nil {
		t.Fatalf("expected %s to exist: %v", sourcePath, statErr)
	}

	pkgImportPath := "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/test/" + fmt.Sprintf("e2egate%d", os.Getpid())
	runGoBuildAndTest(t, root, pkgImportPath)
}

// TestCLI_Doc_MatchesGeneratedReferencePage is Phase 68's own named
// Release Gate criterion: "pleiades doc net.catalyst.device_facts and the
// generated Markdown page for that FQCN report identical Summary, Params,
// and Status fields." Both cmd/pleiades/doc.go and tools/gendocs read the
// same collection.Lookup registry, so they cannot structurally diverge in
// content, only in rendering; this test proves that empirically against
// the real committed docs/reference/modules/net/catalyst/device_facts.md
// file and the real built binary's own terminal output, rather than
// leaving "one source, two renderers" an assumption.
func TestCLI_Doc_MatchesGeneratedReferencePage(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()

	out, err := runPleiades(t, dir, "doc", "net.catalyst.device_facts")
	if err != nil {
		t.Fatalf("doc net.catalyst.device_facts failed: %v\n%s", err, out)
	}

	pagePath := filepath.Join(root, "docs", "reference", "modules", "net", "catalyst", "device_facts.md")
	page, err := os.ReadFile(pagePath) // #nosec G304 -- fixed, repo-relative path built from repoRoot(t), not user input
	if err != nil {
		t.Fatalf("reading generated reference page: %v", err)
	}

	// Fields both renderers draw from the identical Manifest: the summary
	// sentence, the "implemented" status, every parameter name, and every
	// return field name. A mismatch here means the two fell out of sync
	// with the registry, or with each other, not a rendering-format
	// difference (Markdown table cells vs plain-text columns), which this
	// check deliberately does not compare.
	for _, want := range []string{
		"Gathers every device a Cisco Catalyst Center manages, as facts.",
		"insecure_skip_verify",
		"page_size",
		"device_count",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pleiades doc output missing %q (present in the generated page)", want)
		}
		if !strings.Contains(string(page), want) {
			t.Errorf("generated reference page missing %q (present in pleiades doc output)", want)
		}
	}
	// printDocEntry (doc.go) only ever prints a status line for a
	// declared method; an implemented one prints none at all, so its
	// absence here is the positive signal, not a literal "implemented"
	// string. writeModulePage (modules.go) badges an implemented method
	// "beta" in the page's own front matter for the identical reason.
	// Both must agree that this FQCN is implemented, expressed in each
	// renderer's own idiom.
	if strings.Contains(out, "declared, not implemented") {
		t.Error("pleiades doc reports net.catalyst.device_facts as declared, but its Manifest.Status is implemented")
	}
	if !strings.Contains(string(page), "status: beta") {
		t.Error("generated reference page's front matter is not \"status: beta\", the badge writeModulePage gives an implemented method")
	}
}

// TestCLI_DocList proves `pleiades doc --list` is reachable through the
// real built binary, not just in-process (doc_test.go covers runDoc's
// own logic in detail; this is the RULE 0 subprocess check that the
// binary's "doc" dispatch entry and catalog_builtins.go's blank import
// both actually wire up together).
func TestCLI_DocList(t *testing.T) {
	dir := t.TempDir()
	out, err := runPleiades(t, dir, "doc", "--list", "net.catalyst")
	if err != nil {
		t.Fatalf("doc --list net.catalyst should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "net.catalyst.device_facts") {
		t.Errorf("expected doc --list net.catalyst to list net.catalyst.device_facts, got:\n%s", out)
	}
}

// TestCLI_Version proves `pleiades version` is reachable through the real
// built binary and prints something a user would recognize as a version
// line.
func TestCLI_Version(t *testing.T) {
	dir := t.TempDir()
	out, err := runPleiades(t, dir, "version")
	if err != nil {
		t.Fatalf("version should succeed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pleiades") {
		t.Errorf("expected version output to mention pleiades, got:\n%s", out)
	}
}
