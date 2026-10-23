// Release Gate for noticing a second run on the same devices, through the
// real binary against a real sshd: a run held on a task waiting for a file
// is seen by a second run on its device at that run's start, the first
// names the second's change at its end, and neither is refused or made to
// wait. A check, and a run on another device, warn about nothing.
package main_test

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"
)

// heldRunbook changes box, then waits until a file appears on it, which a
// second run makes.
const heldRunbook = `# Changes box, then waits for a second run to release it.
# Blast radius: the release gate's own sshd container.
# Changes: /tmp/g14-held on box.
# Undo: file.remove of /tmp/g14-held.
id: held
hosts: box
tasks:
  - name: change box
    file.directory:
      path: /tmp/g14-held
  - name: wait for the second run
    wait.path:
      path: /tmp/g14-go
      timeout: 120
`

// TestCLI_ASecondRunOnTheSameDevicesIsNoticed is the gate.
func TestCLI_ASecondRunOnTheSameDevicesIsNoticed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// other is the same container under another inventory name: another
	// device as far as any run can tell.
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-host", "other", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "box", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
		{"add-credential", "other", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "held.yaml"), []byte(heldRunbook), 0o600); err != nil {
		t.Fatal(err)
	}

	// The first run, in the background, until it waits.
	held := exec.Command(binPath, "run", "runbooks/held.yaml", "--json")
	held.Dir = dir
	held.Env = append(os.Environ(), "HOME="+home)
	var heldOut, heldErr bytes.Buffer
	held.Stdout, held.Stderr = &heldOut, &heldErr
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- held.Wait() }()
	// A test that fails before the first run is released leaves it waiting;
	// killing a process that already ended does nothing.
	t.Cleanup(func() { _ = held.Process.Kill() })
	activeDir := filepath.Join(dir, ".pleiades", "runs", "active")
	deadline := time.Now().Add(60 * time.Second)
	for {
		records, _ := filepath.Glob(filepath.Join(activeDir, "*.json"))
		if len(records) == 1 && strings.TrimSpace(verifyOverSSH(t, addr, "test -d /tmp/g14-held && echo yes || echo no")) == "yes" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first run never recorded itself and changed box:\n%s\n%s", heldOut.String(), heldErr.String())
		}
		time.Sleep(250 * time.Millisecond)
	}

	// A check on the same device, and a real run on another, say nothing.
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "box", "file.directory", "path=/tmp/g14-check", "--mode", "check")
	if code != 0 || len(rep.Warnings) != 0 {
		t.Errorf("a check on box: exit %d, warnings %v:\n%s", code, rep.Warnings, raw)
	}
	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "adhoc", "other", "file.directory", "path=/tmp/g14-other")
	if code != 0 || len(rep.Warnings) != 0 {
		t.Errorf("a run on other: exit %d, warnings %v:\n%s", code, rep.Warnings, raw)
	}

	// A second real run on box, in the text view: warned at its start, and
	// at its end that the first is still changing box. It is not refused.
	out, err := runPleiadesWithHome(t, dir, home, "adhoc", "box", "file.directory", "path=/tmp/g14-mid")
	if err != nil || strings.Count(out, "WARNING: another run is changing box (runbooks/held.yaml, pid ") != 1 ||
		!strings.Contains(out, "WARNING: another run is still changing box (runbooks/held.yaml") || !strings.Contains(out, "run complete") {
		t.Errorf("a second run on box, text view: %v\n%s", err, out)
	}
	midRunID := runIDIn(t, out)

	// A third, in --json, which also releases the first.
	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "adhoc", "box", "file.directory", "path=/tmp/g14-go")
	if code != 0 || len(rep.Warnings) < 1 || rep.Warnings[0].Kind != "concurrent_run" || rep.Warnings[0].When != "start" ||
		rep.Warnings[0].Runbook != "runbooks/held.yaml" || rep.Warnings[0].PID != held.Process.Pid || !slices.Equal(rep.Warnings[0].Devices, []string{"box"}) {
		t.Errorf("a run releasing the first: exit %d:\n%s", code, raw)
	}
	goRunID := rep.RunID

	select {
	case <-finished:
	case <-time.After(150 * time.Second):
		t.Fatalf("the first run never finished:\n%s\n%s", heldOut.String(), heldErr.String())
	}
	var first jsonReport
	if err := json.Unmarshal(heldOut.Bytes(), &first); err != nil || first.Outcome.Status != "complete" {
		t.Fatalf("the first run's report (%v):\n%s\n%s", err, heldOut.String(), heldErr.String())
	}
	var changedBy []string
	for _, w := range first.Warnings {
		if w.Kind == "concurrent_change" && w.When == "end" && slices.Equal(w.Devices, []string{"box"}) && strings.Contains(w.Message, "pleiades journal show "+w.RunID) {
			changedBy = append(changedBy, w.RunID)
		}
		if w.When == "start" || w.RunID == "" && w.Kind == "concurrent_change" {
			t.Errorf("unexpected warning %+v", w)
		}
	}
	if !slices.Equal(changedBy, []string{midRunID, goRunID}) {
		t.Errorf("the first run named %v as having changed box, want %v then %v:\n%s", changedBy, midRunID, goRunID, heldOut.String())
	}
	if records, _ := os.ReadDir(activeDir); len(records) != 0 {
		t.Errorf("records left once every run ended: %v", records)
	}
}

// runIDIn is the run id a text view prints ("run <id>, journal <path>").
func runIDIn(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "run "); ok {
			if id, _, ok := strings.Cut(rest, ","); ok {
				return id
			}
		}
	}
	t.Fatalf("no run id in:\n%s", out)
	return ""
}
