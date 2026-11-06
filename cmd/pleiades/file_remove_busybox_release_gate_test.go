// The release gate for file.remove's check on a BusyBox device: through the
// real binary, over real SSH, to a real Alpine container whose /bin/sh is
// BusyBox (FAILURE_PATTERNS 937). As root, whether the login may remove a
// path cannot be read from the device, so a check warns rather than refusing,
// whatever check_forbidden says, and a rollback check of a recorded undo is
// not refused either. As an account without root, the device answers, and a
// path in a directory it may not write is refused unless the task asks to be
// warned.
package main_test

import (
	"regexp"
	"strings"
	"testing"
)

// alpineGatePassword is root's password in testdata/alpine-sshd.
const alpineGatePassword = "release-gate-alpine-password"

// removeCheckRunbook is a runbook removing path with the given extra
// file.remove parameters, each an indented "key: value" line.
func removeCheckRunbook(path string, extra ...string) string {
	task := "  - name: remove the gate's file\n    file.remove:\n      path: " + path + "\n"
	for _, e := range extra {
		task += "      " + e + "\n"
	}
	return "# Removes one file on the gate's device.\n# Changes: removes " + path + ".\n# Undo: none; the gate made the file.\n" +
		"id: remove\nhosts: alpine\ntasks:\n" + task
}

// TestCLI_FileRemoveCheckOnBusyBox is the gate.
func TestCLI_FileRemoveCheckOnBusyBox(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the BusyBox file.remove gate, which builds and runs a real Alpine sshd, in short mode")
	}
	device := startTestdataDevice(t, "alpine-sshd", alpineGatePassword, nil)
	const cannotTell = "cannot be read from the device"

	// As root: a file a run made, then a check of the run's rollback, whose
	// undo is the recorded file.remove, and a check naming refuse outright.
	dir, home := device.manage(t, "alpine", "linux_server")
	const made = "/root/pleiades-busybox-gate"
	copyTask := "# Writes one file under /root on the gate's device.\n# Changes: creates " + made + ".\n# Undo: pleiades rollback removes it.\n" +
		"id: make\nhosts: alpine\ntasks:\n  - name: write a file\n    file.copy:\n      dest: " + made + "\n      content: \"made for the gate\\n\"\n"
	writeFile(t, dir, "runbooks/make.yaml", copyTask)
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/make.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`run ([0-9a-f-]{36}), journal `).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("the run named no run id:\n%s", out)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "rollback", m[1], "--mode", "check"); err != nil || !strings.Contains(out, cannotTell) {
		t.Fatalf("a rollback check of the recorded undo as root under BusyBox: %v, want it to warn that the device cannot say:\n%s", err, out)
	}
	writeFile(t, dir, "runbooks/remove.yaml", removeCheckRunbook(made, "check_forbidden: refuse"))
	if out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/remove.yaml", "--mode", "check"); err != nil || !strings.Contains(out, cannotTell) {
		t.Fatalf("a check as root under BusyBox with check_forbidden: refuse: %v, want a warning, not a refusal:\n%s", err, out)
	}
	if _, code := device.exec(t, "test", "-e", made); code != 0 {
		t.Fatal("a check removed the file")
	}

	// As deploy: a file in a directory deploy may not write, which the device
	// can answer for. Refused by default, warned about when the task asks.
	const forbidden = "/etc/pleiades-busybox-gate"
	if _, code := device.exec(t, "sh", "-c", "echo x > "+forbidden); code != 0 {
		t.Fatal("could not make the gate's file under /etc")
	}
	dir, home = device.manageAs(t, "alpine", "linux_server", "deploy", debianDeployPassword)
	writeFile(t, dir, "runbooks/remove.yaml", removeCheckRunbook(forbidden))
	if out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/remove.yaml", "--mode", "check"); err == nil || !strings.Contains(out, "check_forbidden: warn") {
		t.Fatalf("a check as deploy of a file in /etc: %v, want a refusal naming check_forbidden: warn:\n%s", err, out)
	}
	writeFile(t, dir, "runbooks/remove.yaml", removeCheckRunbook(forbidden, "check_forbidden: warn"))
	if out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/remove.yaml", "--mode", "check"); err != nil || !strings.Contains(out, "may not remove "+forbidden) {
		t.Fatalf("the same check asked to warn: %v, want a warning:\n%s", err, out)
	}
	if _, code := device.exec(t, "test", "-e", forbidden); code != 0 {
		t.Fatal("a check removed the file")
	}
}
