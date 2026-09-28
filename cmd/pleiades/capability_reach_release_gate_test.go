// Release gate for FAILURE_PATTERNS 344: methods whose capability no
// device type used to satisfy run through the real binary against a real
// Debian device.
package main_test

import (
	"strings"
	"testing"
)

// debianGatePassword is root's password in the throwaway image
// testdata/debian-sshd builds.
const debianGatePassword = "release-gate-debian-password"

// TestCLI_PackageAndAccountMethodsReachARealDevice classifies a real
// Debian device as linux_server/debian_family and runs identity.group.create
// and pkg.install on it through the real binary. Before the fix both were
// refused before any command was sent ("requires capability
// PosixAccountCapable"), on every real device, whatever the inventory
// said. The group is then read back from the container itself, and the
// already-installed package must report no change.
func TestCLI_PackageAndAccountMethodsReachARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the capability reach release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")

	writeFile(t, dir, "runbooks/reach.yaml", `id: reach
hosts: debian
tasks:
  - name: make a group
    identity.group.create:
      name: pleiadesgate
  - name: the ssh server is installed
    pkg.install:
      name: openssh-server
`)
	out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/reach.yaml")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tasks[0] [") || !strings.Contains(out, "]: changed") {
		t.Errorf("the group task did not report changed:\n%s", out)
	}
	if !strings.Contains(out, "tasks[1] [") || strings.Count(out, ": changed") != 1 {
		t.Errorf("want only the group task changed, the installed package unchanged:\n%s", out)
	}

	if got, code := device.exec(t, "getent", "group", "pleiadesgate"); code != 0 || !strings.Contains(got, "pleiadesgate:") {
		t.Fatalf("the group is not on the device (exit %d): %q", code, got)
	}
}
