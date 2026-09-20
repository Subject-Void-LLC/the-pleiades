// Package main_test: the Release Gate for check mode (PLAN.md Section
// 34's `check`, IMPLEMENTATION.md Phase 46).
//
// The claim is the one Phase 46's own gate names: a runbook mixing tasks
// that can be checked and tasks that cannot runs as a check against a
// real device, changes nothing on that device, and names every task it
// could not check. "Changes nothing" is proven by asking the device over
// a connection this test opens itself, never by trusting the binary's own
// report, and it is proven against a control: the same runbook run for
// real afterwards DOES change the device, so the check's silence is a
// property of the mode rather than of a runbook that would never have
// done anything.
//
// It also proves PLAN.md Section 9's lock end to end: a device in the
// simulate-locked lifecycle state (the state sync plugins give every newly
// discovered device) is reached by a check, and a real run naming it is
// refused at validation before anything executes.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The paths the gate's runbook would create on the device. Everything
// lives under one root so a single existence test covers "nothing at all
// was created".
const (
	checkGateRoot       = "/tmp/pleiades-check-gate"
	checkGateActiveDir  = checkGateRoot + "/active"
	checkGateLockedDir  = checkGateRoot + "/locked"
	checkGateCommandDir = "/tmp/pleiades-check-gate-command"
)

// checkGateRunbook mixes the two kinds of task a check has to handle: two
// file.directory tasks, whose method declares check support, and one
// exec.command task, whose effect cannot be known without running it. The
// third task targets the simulate-locked device.
func checkGateRunbook() string {
	return "id: check-mode-release-gate\n" +
		"tasks:\n" +
		"  - name: make-a-directory\n" +
		"    fqcn: file.directory\n" +
		"    params:\n" +
		"      target: container1\n" +
		"      path: " + checkGateActiveDir + "\n" +
		"      mode: \"0750\"\n" +
		"  - name: run-a-command\n" +
		"    fqcn: exec.command\n" +
		"    params:\n" +
		"      target: container1\n" +
		"      cmd: \"/bin/mkdir -p " + checkGateCommandDir + "\"\n" +
		"  - name: reach-a-simulate-locked-device\n" +
		"    fqcn: file.directory\n" +
		"    params:\n" +
		"      target: container2\n" +
		"      path: " + checkGateLockedDir + "\n"
}

// nodeLine returns the one output line reporting nodeID, failing the test
// when there is not exactly one. The line carries the node's own verdict
// ("would change", "ok", "COULD NOT CHECK", ...).
func nodeLine(t *testing.T, out, nodeID string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), nodeID+" ") || strings.HasPrefix(strings.TrimSpace(line), nodeID+":") {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one output line for %s, got %d:\n%s", nodeID, len(found), out)
	}
	return found[0]
}

// existsOnDevice asks the device, over this test's own connection, whether
// path exists. The command always exits 0, since verifyOverSSH fails the
// test on a non-zero exit and "absent" is an answer here, not an error.
func existsOnDevice(t *testing.T, addr, path string) bool {
	t.Helper()
	out := verifyOverSSH(t, addr, "if [ -e "+path+" ]; then echo present; else echo absent; fi")
	return strings.TrimSpace(out) == "present"
}

// lockHost puts the inventory host named name into the simulate-locked
// lifecycle state. The CLI has no command that sets a lifecycle state (a
// sync plugin does, for every device it discovers), so the gate writes
// the state where the file repository stores it: the generated sidecar
// beside inventory.yaml, keyed by the host's stored id. A host with no
// sidecar entry reads as active, which is why add-host writes none.
func lockHost(t *testing.T, projectDir, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(projectDir, "inventory.yaml")) // #nosec G304 -- a path under this test's own t.TempDir
	if err != nil {
		t.Fatalf("reading inventory.yaml: %v", err)
	}
	var inv struct {
		Hosts []struct {
			ID   string `yaml:"id"`
			Name string `yaml:"name"`
		} `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(data, &inv); err != nil {
		t.Fatalf("parsing inventory.yaml: %v", err)
	}
	id := ""
	for _, h := range inv.Hosts {
		if h.Name == name {
			id = h.ID
		}
	}
	if id == "" {
		t.Fatalf("no host named %q in inventory.yaml", name)
	}
	sidecar, err := yaml.Marshal(map[string]any{
		"hosts": []map[string]any{{"id": id, "version": 0, "state": "simulate-locked"}},
	})
	if err != nil {
		t.Fatalf("encoding the state sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".inventory-state.generated.yaml"), sidecar, 0o600); err != nil {
		t.Fatalf("writing the state sidecar: %v", err)
	}
}

// TestCLI_CheckModeChangesNothing is the Release Gate.
func TestCLI_CheckModeChangesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the check mode Release Gate container test in short mode")
	}

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

	dir := t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "init"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	// Two inventory entries for the one container: the active one, and the
	// one this gate locks. They share an address because what differs is
	// the lifecycle state, not the machine.
	for _, name := range []string{"container1", "container2"} {
		if out, err := runPleiadesWithHome(t, dir, homeDir, "add-host", name, "--type", "linux_server",
			"--set", "host="+host, "--set", "port="+strconv.Itoa(port)); err != nil {
			t.Fatalf("add-host %s failed: %v\n%s", name, err, out)
		}
		if out, err := runPleiadesWithHome(t, dir, homeDir, "add-credential", name,
			"--username", releaseGateSSHUser, "--password", releaseGateSSHPassword); err != nil {
			t.Fatalf("add-credential %s failed: %v\n%s", name, err, out)
		}
	}
	lockHost(t, dir, "container2")

	runbook := filepath.Join(dir, "runbooks", "check.yaml")
	if err := os.WriteFile(runbook, []byte(checkGateRunbook()), 0o600); err != nil {
		t.Fatalf("failed to write the fixture runbook: %v", err)
	}

	// Phase one: the check. It must end non-zero, because exec.command
	// cannot be checked and a check that does not cover every task is not
	// allowed to read as a pass.
	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/check.yaml", "--mode", "check", "--verbose")
	if code := exitCode(t, err); code != 3 {
		t.Fatalf("an incomplete check exited %d, want 3:\n%s", code, out)
	}
	if !strings.Contains(out, "check incomplete: 1 task(s) could not be checked") {
		t.Errorf("expected the incomplete check to be counted, got:\n%s", out)
	}
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("expected the directory task to predict a change, got %q", l)
	}
	if l := nodeLine(t, out, "tasks[1]"); !strings.Contains(l, "COULD NOT CHECK") || !strings.Contains(l, "exec.command") {
		t.Errorf("expected the command task to be named as unchecked, got %q", l)
	}
	if l := nodeLine(t, out, "tasks[2]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("expected the simulate-locked device to be checked, got %q", l)
	}
	if strings.Contains(out, releaseGateSSHPassword) {
		t.Error("the stored password appeared in the check's output")
	}

	// The proof, from the device itself: nothing the runbook names exists.
	for _, path := range []string{checkGateRoot, checkGateCommandDir} {
		if existsOnDevice(t, addr, path) {
			t.Fatalf("the check created %s on the device", path)
		}
	}

	// A check writes no journal: it did nothing, so a record saying
	// "changed" would be a false history.
	if entries, err := os.ReadDir(filepath.Join(dir, ".pleiades", "journal")); err == nil && len(entries) > 0 {
		t.Errorf("the check wrote %d journal file(s); it must write none", len(entries))
	}

	// Phase two: the lock, from the real-run side. The same runbook run
	// for real is refused before anything executes, because one of its
	// tasks targets a simulate-locked device, and a lock that could be
	// escalated by simply running without --mode check would be no lock.
	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/check.yaml")
	if err == nil || !strings.Contains(out, "simulate-locked") || !strings.Contains(out, "validation failed, not executing") {
		t.Fatalf("a real run targeting a simulate-locked device was not refused at validation: %v\n%s", err, out)
	}
	for _, path := range []string{checkGateRoot, checkGateCommandDir} {
		if existsOnDevice(t, addr, path) {
			t.Fatalf("the refused real run still created %s", path)
		}
	}

	// Phase three: the control. The same tasks minus the locked device,
	// run for real, DO change the device, which is what makes phase one's
	// "nothing exists" mean something.
	controlRunbook := strings.Split(checkGateRunbook(), "  - name: reach-a-simulate-locked-device\n")[0]
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "control.yaml"), []byte(controlRunbook), 0o600); err != nil {
		t.Fatalf("failed to write the control runbook: %v", err)
	}
	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/control.yaml")
	if err != nil {
		t.Fatalf("the control run failed: %v\n%s", err, out)
	}
	if !existsOnDevice(t, addr, checkGateActiveDir) || !existsOnDevice(t, addr, checkGateCommandDir) {
		t.Fatalf("the real run did not create what the runbook names, so the check assertions above prove nothing:\n%s", out)
	}
	if mode := strings.TrimSpace(verifyOverSSH(t, addr, "stat -c %a "+checkGateActiveDir)); mode != "750" {
		t.Errorf("the real run left mode %q, want 750", mode)
	}
	if existsOnDevice(t, addr, checkGateLockedDir) {
		t.Fatal("something reached the simulate-locked device's path")
	}

	// Phase four: a check of the converged device predicts no change for
	// the directory the real run made, which is the other half of a
	// prediction being worth anything.
	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/check.yaml", "--mode", "check")
	if err == nil {
		t.Fatalf("the command task still cannot be checked, so the check must still end non-zero:\n%s", out)
	}
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, ": ok") {
		t.Errorf("expected the converged directory to check as ok, got %q", l)
	}
	if l := nodeLine(t, out, "tasks[2]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("expected the still-locked device's directory to still predict a change, got %q", l)
	}
}
