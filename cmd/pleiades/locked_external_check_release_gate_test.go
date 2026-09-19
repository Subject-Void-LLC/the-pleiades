// Package main_test: the Release Gate for keeping a third party's check
// away from a simulate-locked device (IMPLEMENTATION.md Phase 46,
// FAILURE_PATTERNS 253).
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

// The marker files the writing check would leave on the device.
const (
	lockGateLocked = "/tmp/pleiades-lock-gate-locked"
	lockGateActive = "/tmp/pleiades-lock-gate-active"
)

// TestCLI_AnExternalCheckNeverReachesASimulateLockedDevice proves, through
// the real binary against a real sshd, that a check from an external
// program is not run against a simulate-locked device. The program is
// testdata/writingcheck, whose Check deliberately writes. Against the
// locked device the task is reported unchecked and nothing is written.
// The control is the same program against an active device, where its
// Check runs and the write lands: that is what shows the guard, and not
// the program, kept the locked device clean. A built-in check still
// reaches the locked device, since its checks are proven to only read.
func TestCLI_AnExternalCheckNeverReachesASimulateLockedDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the simulate-lock Release Gate container test in short mode")
	}

	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(collections, "writingcheck"), "./testdata/writingcheck")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building testdata/writingcheck: %v\n%s", err, out)
	}

	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(homeDir, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": homeDir, "PLEIADES_COLLECTIONS_DIR": collections}

	dir := t.TempDir()
	if out, err := runPleiadesWithEnv(t, dir, env, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	for _, name := range []string{"container1", "container2"} {
		for _, args := range [][]string{
			{"add-host", name, "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
			{"add-credential", name, "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		} {
			if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
				t.Fatalf("%s %s failed: %v\n%s", args[0], name, err, out)
			}
		}
	}
	lockHost(t, dir, "container2")
	if out, err := runPleiadesWithEnv(t, dir, env, "collection", "approve", "writingcheck", "--yes"); err != nil {
		t.Fatalf("collection approve failed: %v\n%s", err, out)
	}

	runbook := func(file, target, path, fqcn string) {
		t.Helper()
		body := "id: lock-gate\ntasks:\n  - name: probe\n    fqcn: " + fqcn + "\n    params:\n      target: " + target + "\n      path: " + path + "\n"
		if err := os.WriteFile(filepath.Join(dir, "runbooks", file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The locked device: unchecked, and nothing written.
	runbook("locked.yaml", "container2", lockGateLocked, "gatefixture.check.writes")
	out, err := runPleiadesWithEnv(t, dir, env, "run", "runbooks/locked.yaml", "--mode", "check")
	if code := exitCode(t, err); code != 3 {
		t.Fatalf("a check that could not run exited %d, want 3:\n%s", code, out)
	}
	if l := nodeLine(t, out, "tasks[0]"); !strings.Contains(l, "COULD NOT CHECK") || !strings.Contains(l, "simulate-locked") {
		t.Errorf("the external check against the locked device should be reported unchecked, naming the lock, got %q", l)
	}
	if existsOnDevice(t, addr, lockGateLocked) {
		t.Fatal("a third party's check wrote to a simulate-locked device")
	}

	// The control: the same program against the active device. Its Check
	// runs, and its write lands.
	runbook("active.yaml", "container1", lockGateActive, "gatefixture.check.writes")
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/active.yaml", "--mode", "check")
	if err != nil {
		t.Fatalf("the control check failed: %v\n%s", err, out)
	}
	if !existsOnDevice(t, addr, lockGateActive) {
		t.Fatalf("the fixture's check did not write on the active device, so the locked-device assertion proves nothing:\n%s", out)
	}

	// A built-in check still reaches the locked device.
	runbook("builtin.yaml", "container2", lockGateLocked+"-dir", "file.directory")
	out, err = runPleiadesWithEnv(t, dir, env, "run", "runbooks/builtin.yaml", "--mode", "check")
	if err != nil {
		t.Fatalf("the built-in check of the locked device failed: %v\n%s", err, out)
	}
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("a built-in check should still reach the locked device, got %q", l)
	}
	if existsOnDevice(t, addr, lockGateLocked+"-dir") {
		t.Fatal("the built-in check changed the locked device")
	}
}
