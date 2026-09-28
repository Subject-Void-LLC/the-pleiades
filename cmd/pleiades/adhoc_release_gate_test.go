// The Release Gate for adhoc and --json: the real binary against a real
// sshd container, with every change confirmed on the device over a second,
// independent connection rather than read from the command's own output.
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

// adhocGateMarker is the file the gate's method creates on the device,
// named for the gate so a leftover from a killed run is identifiable.
const adhocGateMarker = "/tmp/pleiades-adhoc-gate-marker"

// TestCLI_AdhocReleaseGate proves, on a real device:
//
//  1. a check says what the method would change and changes nothing;
//  2. the run changes it, and a second run finds nothing to change;
//  3. the JSON document carries the device's own output;
//  4. a failure on the device is a failed task and exit 1, in both views;
//  5. the device's credential went in through --password-stdin, never argv.
func TestCLI_AdhocReleaseGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the adhoc Release Gate container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	knownHostsLine := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(homeDir, ".ssh", "known_hosts"), []byte(knownHostsLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "container1", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port), "--tags", "gate"},
	} {
		if out, err := runPleiadesWithHome(t, dir, homeDir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, releaseGateSSHPassword+"\n", "add-credential", "container1",
		"--username", releaseGateSSHUser, "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	onDevice := func() string {
		return strings.TrimSpace(verifyOverSSH(t, addr, "test -e "+adhocGateMarker+" && cat "+adhocGateMarker+" || echo absent"))
	}
	create := []string{"adhoc", "gate", "exec.command", "argv:=[sh, -c, 'echo made-by-adhoc > " + adhocGateMarker + "']", "creates=" + adhocGateMarker}

	// 1. A check predicts the change and makes none.
	rep, _, code := runPleiadesJSONWithHome(t, dir, homeDir, append(create, "--mode", "check")...)
	if code != 0 || rep.Mode != "check" || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "would_change" || rep.Tasks[0].Host != "container1" {
		t.Fatalf("check: exit %d, %+v", code, rep)
	}
	if got := onDevice(); got != "absent" {
		t.Fatalf("a check changed the device: %q", got)
	}

	// 2. The run makes the change; the same call again finds it made.
	out, err := runPleiadesWithHome(t, dir, homeDir, create...)
	if err != nil || !strings.Contains(out, "tasks[0] [") || !strings.Contains(out, ": changed") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if got := onDevice(); got != "made-by-adhoc" {
		t.Fatalf("the device holds %q after the run", got)
	}
	rep, _, code = runPleiadesJSONWithHome(t, dir, homeDir, create...)
	if code != 0 || rep.Tasks[0].Status != "ok" || rep.Outcome.Message != "run complete" {
		t.Fatalf("second run: exit %d, %+v", code, rep)
	}

	// 3. The document carries what the device printed.
	rep, _, code = runPleiadesJSONWithHome(t, dir, homeDir, "adhoc", "container1", "exec.command", "argv:=[cat, "+adhocGateMarker+"]")
	if code != 0 || strings.TrimSpace(stringStat(rep.Tasks[0].Stats, "stdout")) != "made-by-adhoc" {
		t.Fatalf("read back: exit %d, %+v", code, rep.Tasks)
	}

	// 4. A command that fails on the device fails the task, in both views.
	fail := []string{"adhoc", "gate", "exec.command", "cmd=false"}
	rep, _, code = runPleiadesJSONWithHome(t, dir, homeDir, fail...)
	if code != 1 || rep.Tasks[0].Status != "failed" || rep.Tasks[0].Error == "" || rep.Outcome.Status != "failed" {
		t.Fatalf("failure: exit %d, %+v", code, rep)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, fail...); exitCode(t, err) != 1 || !strings.Contains(out, "FAILED") {
		t.Fatalf("the text view of a failure: %v\n%s", err, out)
	}

	verifyOverSSH(t, addr, "rm -f "+adhocGateMarker)
}

// stringStat returns stats[key] when it is a string, else "".
func stringStat(stats map[string]any, key string) string {
	s, _ := stats[key].(string)
	return s
}
