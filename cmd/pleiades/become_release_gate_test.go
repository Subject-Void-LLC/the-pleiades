// The release gate for become: a runbook run through the real binary, logged
// in to a real Debian device as an account without root, writes where only
// root may, and its rollback undoes the change as root too.
package main_test

import (
	"regexp"
	"strings"
	"testing"
)

// debianDeployPassword is the deploy account's password in
// testdata/debian-sshd, an account with a NOPASSWD sudo rule and no root.
const debianDeployPassword = "release-gate-deploy-password"

// TestCLI_BecomeRunsATaskAndItsUndoAsRoot logs in as deploy and shows that
// without become the login cannot write under /root, that with become the
// same task writes a file root owns and the journal records root, and that
// rolling the run back removes the file, which the login alone could not.
func TestCLI_BecomeRunsATaskAndItsUndoAsRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the become release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manageAs(t, "debian", "linux_server,debian_family", "deploy", debianDeployPassword)
	const target = "/root/pleiades-become-gate"
	task := "  - name: write where only root may\n    file.copy:\n      dest: " + target + "\n      content: \"made through sudo\\n\"\n"
	header := "# Writes one file under /root on the gate's device.\n# Changes: creates " + target + ".\n# Undo: pleiades rollback removes it.\n"

	writeFile(t, dir, "runbooks/plain.yaml", header+"id: plain\nhosts: debian\ntasks:\n"+task)
	// The login's own refusal, not a refusal of the runbook, is what makes
	// the elevated run below mean something.
	if out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/plain.yaml"); err == nil || !strings.Contains(strings.ToLower(out), "permission denied") {
		t.Fatalf("without become the run should fail on the device's permission denied (err %v):\n%s", err, out)
	}
	if _, code := device.exec(t, "test", "-e", target); code == 0 {
		t.Fatal("the file exists after the run without become")
	}

	writeFile(t, dir, "runbooks/become.yaml", header+"id: become\nhosts: debian\ntasks:\n"+task+"    become: true\n")
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/become.yaml")
	if err != nil {
		t.Fatalf("run with become: %v\n%s", err, out)
	}
	if owner, code := device.exec(t, "stat", "-c", "%U", target); code != 0 || owner != "root" {
		t.Fatalf("the file is owned by %q (stat exited %d), want root:\n%s", owner, code, out)
	}
	m := regexp.MustCompile(`run ([0-9a-f-]{36}), journal `).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("the run named no run id:\n%s", out)
	}
	shown, err := runPleiadesWithHome(t, dir, home, "journal", "show", "--json", m[1])
	if err != nil || !strings.Contains(shown, `"become": "root"`) {
		t.Fatalf("the journal does not record the task ran as root (%v):\n%s", err, shown)
	}

	out, err = runPleiadesWithHome(t, dir, home, "rollback", m[1])
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if _, code := device.exec(t, "test", "-e", target); code == 0 {
		t.Fatalf("the file is still there after the rollback, so its undo did not run as root:\n%s", out)
	}
}
