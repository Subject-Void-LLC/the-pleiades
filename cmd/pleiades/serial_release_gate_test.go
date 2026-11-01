// The release gate for serial:: four real Debian devices, one project, a
// runbook run through the real binary two devices at a time, its timing and
// its stop on failure read back from the devices themselves.
package main_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// manageMany makes one project managing every device in devices under the
// names given, each tagged tag and logged in to as root, and a home whose
// known_hosts holds every device's key.
func manageMany(t *testing.T, devices []*testdataDevice, names []string, tag string) (dir, home string) {
	t.Helper()
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, d := range devices {
		lines = append(lines, knownhosts.Line([]string{d.addr()}, testsupport.CaptureHostKey(t, d.addr(), "root", d.password)))
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	if out, err := runPleiadesWithHome(t, dir, home, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for i, d := range devices {
		args := []string{"add-host", names[i], "--classify", "linux_server,debian_family", "--set", "host=" + d.host, "--set", "port=" + strconv.Itoa(d.port), "--tag", tag}
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		if out, err := runPleiadesWithStdin(t, dir, d.password+"\n", "add-credential", names[i], "--username", "root", "--password-stdin"); err != nil {
			t.Fatalf("add-credential %s: %v\n%s", names[i], err, out)
		}
	}
	return dir, home
}

// startFour starts four Debian devices.
func startFour(t *testing.T) []*testdataDevice {
	t.Helper()
	var devices []*testdataDevice
	for range 4 {
		devices = append(devices, startTestdataDevice(t, "debian-sshd", debianGatePassword, nil))
	}
	return devices
}

// readStamp reads a nanosecond stamp a task wrote on d.
func readStamp(t *testing.T, d *testdataDevice, path string) int64 {
	t.Helper()
	out, code := d.exec(t, "cat", path)
	if code != 0 {
		t.Fatalf("reading %s: exit %d, %q", path, code, out)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatalf("stamp %q: %v", out, err)
	}
	return n
}

// TestCLI_SerialRunsTwoDevicesAtATime: with serial: 2 over four devices, the
// first two run every task before either of the last two starts its first,
// which the devices' own clocks show (the containers share the host's
// clock), and the run is one run: one journal for all four.
func TestCLI_SerialRunsTwoDevicesAtATime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the serial release gate, which builds and runs four real Debian sshds, in short mode")
	}
	devices := startFour(t)
	names := []string{"s1", "s2", "s3", "s4"}
	dir, home := manageMany(t, devices, names, "web")
	writeFile(t, dir, "runbooks/serial.yaml", `# Stamps when each device starts and finishes, two devices at a time.
# Changes: /tmp/serial-start and /tmp/serial-end on each web device.
# Undo: none recorded; delete both files by hand.
id: serial-gate
hosts: web
serial: 2
tasks:
  - name: stamp the start
    exec.shell:
      cmd: date +%s%N > /tmp/serial-start
  - name: work, then stamp the end
    exec.shell:
      cmd: sleep 1; date +%s%N > /tmp/serial-end
`)
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/serial.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"serial: batch 1 of 2: s1, s2", "serial: batch 2 of 2: s3, s4"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q:\n%s", want, out)
		}
	}
	firstEnd := max(readStamp(t, devices[0], "/tmp/serial-end"), readStamp(t, devices[1], "/tmp/serial-end"))
	secondStart := min(readStamp(t, devices[2], "/tmp/serial-start"), readStamp(t, devices[3], "/tmp/serial-start"))
	if secondStart <= firstEnd {
		t.Errorf("the second batch started (%d) before the first finished (%d)", secondStart, firstEnd)
	}
	journals, err := filepath.Glob(filepath.Join(dir, ".pleiades", "journal", "*.jsonl"))
	if err != nil || len(journals) != 1 {
		t.Errorf("want one journal for the whole run, found %v (%v)", journals, err)
	}
}

// TestCLI_SerialStopsTheRolloutAtAFailedBatch: with serial: 1, a failure on
// the second device stops the rollout there: the third and fourth never run
// the task, the run fails, and it names them.
func TestCLI_SerialStopsTheRolloutAtAFailedBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the serial release gate, which builds and runs four real Debian sshds, in short mode")
	}
	devices := startFour(t)
	names := []string{"s1", "s2", "s3", "s4"}
	dir, home := manageMany(t, devices, names, "web")
	if _, code := devices[1].exec(t, "touch", "/tmp/fail-here"); code != 0 {
		t.Fatal("marking s2 to fail")
	}
	writeFile(t, dir, "runbooks/serial.yaml", `# Marks each web device, one at a time; fails where /tmp/fail-here exists.
# Changes: /tmp/serial-mark on each device it reaches.
# Undo: none recorded; delete the file by hand.
id: serial-stop
hosts: web
serial: 1
tasks:
  - name: mark, unless told to fail
    exec.shell:
      cmd: test ! -e /tmp/fail-here && touch /tmp/serial-mark
`)
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/serial.yaml")
	if err == nil {
		t.Fatalf("the run with a failing batch passed:\n%s", out)
	}
	if _, code := devices[0].exec(t, "test", "-e", "/tmp/serial-mark"); code != 0 {
		t.Errorf("the first batch did not run:\n%s", out)
	}
	for _, i := range []int{2, 3} {
		if _, code := devices[i].exec(t, "test", "-e", "/tmp/serial-mark"); code == 0 {
			t.Errorf("%s ran after the failed batch:\n%s", names[i], out)
		}
	}
	if !strings.Contains(out, "batch 2 of 4 failed") || !strings.Contains(out, "s3, s4") {
		t.Errorf("the run does not name the stop and the devices left:\n%s", out)
	}
}
