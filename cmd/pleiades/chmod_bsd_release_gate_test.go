// The BSD half of FAILURE_PATTERNS 248's chmod item: file.permissions and
// file.directory, and their checks, against a real FreeBSD host's chmod
// and stat, through the real binary.
//
// The host is the VirtualBox lab's FreeBSD VM, which The Pleiades itself
// installs (virt.vbox.vm.install with installer: freebsd), clones, trusts
// from its console and onboards as generic_ssh; this gate runs in that
// project, as a user runs the binary there, so the device's credential
// comes from the project's own vault. It skips, saying what to set, where
// there is no such host. (The file is named for BSD rather than FreeBSD,
// since a name ending _freebsd_test.go builds only on FreeBSD:
// FAILURE_PATTERNS 374.)
package main_test

import (
	"os"
	"strings"
	"testing"
)

// PLEIADES_BSD_PROJECT and PLEIADES_BSD_DEVICE name a project that manages
// a FreeBSD host, onboarded, and the host's inventory name.
const (
	envBSDProject = "PLEIADES_BSD_PROJECT"
	envBSDDevice  = "PLEIADES_BSD_DEVICE"
)

// TestCLI_FreeBSDFileChecksMatchTheirRealRuns runs each case of the
// five-digit chmod item against FreeBSD: clearing a directory's setgid
// with 0755, a file's mode, a new directory, and a new directory that
// keeps setgid when asked. Each goes through checkThenRun (predicted,
// applied, matched, converged), then its mode is read back beside the
// method with BSD stat.
func TestCLI_FreeBSDFileChecksMatchTheirRealRuns(t *testing.T) {
	dir, host := os.Getenv(envBSDProject), os.Getenv(envBSDDevice)
	if dir == "" || host == "" {
		t.Skipf("this Release Gate needs a FreeBSD host onboarded as generic_ssh: set %s to the project that manages it and %s to its inventory name "+
			"(examples/virtualbox_lab's freebsd runbooks make one)", envBSDProject, envBSDDevice)
	}
	// shell runs a command on the host through exec.shell, a different
	// method from the ones under test, and returns its output.
	shell := func(cmd string) string {
		t.Helper()
		rep, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", host, "exec.shell", "cmd="+cmd)
		if code != 0 || len(rep.Tasks) != 1 {
			t.Fatalf("%q on %s (exit %d):\n%s", cmd, host, code, raw)
		}
		return strings.TrimSpace(stringStat(rep.Tasks[0].Stats, "stdout"))
	}
	// In the login's home, not /tmp: BSD gives a new file its parent
	// directory's group, which under /tmp is wheel, and setting setgid on a
	// directory whose group the login is not in is refused (EPERM), which
	// on Linux would have succeeded. Found running this gate the first time.
	root := shell("echo $HOME") + "/pleiades-chmod-gate"
	shell("rm -rf " + root + " && mkdir -p " + root + " && mkdir " + root + "/setgid && chmod 2755 " + root + "/setgid && touch " + root + "/file && chmod 0600 " + root + "/file")
	if got := shell("uname -s"); got != "FreeBSD" {
		t.Fatalf("%s is %q, not FreeBSD, so nothing below tests BSD's chmod", host, got)
	}
	// BSD stat's own form of the whole mode: %Mp is the setuid, setgid and
	// sticky digit and %Lp the permission bits (%Lp alone drops setgid).
	mode := func(path string) func() string {
		return func() string { return shell("stat -f %Mp%Lp " + path + " 2>/dev/null || echo absent") }
	}
	if got := mode(root + "/setgid")(); got != "2755" {
		t.Fatalf("the setgid fixture is %s, want 2755", got)
	}

	for _, tc := range []struct {
		name, method, path, mode, want string
	}{
		{"clearing a directory's setgid", "file.permissions", root + "/setgid", "0755", "0755"},
		{"a file's mode", "file.permissions", root + "/file", "0640", "0640"},
		{"a new directory", "file.directory", root + "/made", "0750", "0750"},
		{"a new directory keeping setgid when asked", "file.directory", root + "/shared", "2750", "2750"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkThenRun(t, dir, "", host, mode(tc.path), tc.method, "path="+tc.path, "mode="+tc.mode)
			if got := mode(tc.path)(); got != tc.want {
				t.Errorf("the run left mode %s, want %s", got, tc.want)
			}
		})
	}
	shell("rm -rf " + root)
}
