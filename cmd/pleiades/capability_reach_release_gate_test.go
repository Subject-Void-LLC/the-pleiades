// Release gate for FAILURE_PATTERNS 344: methods whose capability no
// device type used to satisfy run through the real binary against a real
// Debian device.
package main_test

import (
	"maps"
	"regexp"
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

// TestCLI_ACreatedAccountsUndoRemovesOnlyTheHomeItMade is the release gate
// for identity.user.create's undo on a real Debian device, read back
// through the container itself: rolling back a run that made an account
// removes the home useradd made for it, and rolling back one whose home
// was used since removes the account but keeps the home, saying so.
func TestCLI_ACreatedAccountsUndoRemovesOnlyTheHomeItMade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the account undo release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")
	runID := regexp.MustCompile(`run ([0-9a-f-]{36}), journal `)

	for _, tc := range []struct {
		account  string
		useHome  bool
		wantHome bool
	}{
		{account: "pleiadesfresh", wantHome: false},
		{account: "pleiadesused", useHome: true, wantHome: true},
	} {
		t.Run(tc.account, func(t *testing.T) {
			writeFile(t, dir, "runbooks/"+tc.account+".yaml", "id: "+tc.account+"\nhosts: debian\ntasks:\n"+
				"  - name: make the account\n    identity.user.create:\n      name: "+tc.account+"\n")
			out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/"+tc.account+".yaml")
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			m := runID.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("the run named no run id:\n%s", out)
			}
			homeDir := "/home/" + tc.account
			if _, code := device.exec(t, "test", "-d", homeDir); code != 0 {
				t.Fatalf("useradd made no home %s", homeDir)
			}
			if tc.useHome {
				if got, code := device.exec(t, "sh", "-c", "echo data > "+homeDir+"/app.db"); code != 0 {
					t.Fatalf("writing into the home: exit %d %q", code, got)
				}
			}

			out, err = runPleiadesWithHome(t, dir, home, "rollback", m[1])
			if err != nil {
				t.Fatalf("rollback: %v\n%s", err, out)
			}
			if _, code := device.exec(t, "getent", "passwd", tc.account); code == 0 {
				t.Errorf("the account is still on the device after the rollback:\n%s", out)
			}
			_, code := device.exec(t, "test", "-d", homeDir)
			if gotHome := code == 0; gotHome != tc.wantHome {
				t.Errorf("home %s on the device after the rollback: %v, want %v\n%s", homeDir, gotHome, tc.wantHome, out)
			}
			if tc.wantHome && !strings.Contains(out, "kept "+homeDir) {
				t.Errorf("the rollback did not say it kept %s:\n%s", homeDir, out)
			}
		})
	}
}

// TestCLI_AnInstallsUndoRemovesWhatItPulledIn is the release gate for
// pkg.apt.install's undo on a real Debian device, through the journal:
// rolling back a run that installed jq removes jq and the two libraries
// it pulled in, leaving the package list exactly as it was; and with one
// of those libraries marked installed on purpose after the run, the
// rollback keeps it and the library it needs, and says so. The image holds
// jq's packages already downloaded, so nothing here reaches the network.
func TestCLI_AnInstallsUndoRemovesWhatItPulledIn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the package undo release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")
	runID := regexp.MustCompile(`run ([0-9a-f-]{36}), journal `)
	installed := func() map[string]bool {
		got, code := device.exec(t, "dpkg-query", "-W", "-f", "${Package} ${db:Status-Abbrev}\n")
		if code != 0 {
			t.Fatalf("dpkg-query exited %d: %q", code, got)
		}
		set := map[string]bool{}
		for _, line := range strings.Split(got, "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] == "ii" {
				set[f[0]] = true
			}
		}
		return set
	}
	writeFile(t, dir, "runbooks/jq.yaml", "id: jq\nhosts: debian\ntasks:\n"+
		"  - name: install jq\n    pkg.apt.install:\n      name: jq\n")

	for _, tc := range []struct {
		name     string
		keepLib  bool
		wantGone []string
		wantKept []string
	}{
		{name: "everything it pulled in goes", wantGone: []string{"jq", "libjq1", "libonig5"}},
		{name: "a library kept on purpose stays", keepLib: true, wantGone: []string{"jq"}, wantKept: []string{"libjq1", "libonig5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := installed()
			out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/jq.yaml")
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			m := runID.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("the run named no run id:\n%s", out)
			}
			shown, err := runPleiadesWithHome(t, dir, home, "journal", "show", m[1])
			if err != nil || !strings.Contains(shown, "libjq1") || !strings.Contains(shown, "libonig5") {
				t.Fatalf("the journal's undo does not name what the install pulled in (%v):\n%s", err, shown)
			}
			if tc.keepLib {
				if got, code := device.exec(t, "apt-mark", "manual", "libjq1"); code != 0 {
					t.Fatalf("apt-mark exited %d: %q", code, got)
				}
			}

			out, err = runPleiadesWithHome(t, dir, home, "rollback", m[1])
			if err != nil {
				t.Fatalf("rollback: %v\n%s", err, out)
			}
			after := installed()
			for _, pkg := range tc.wantGone {
				if after[pkg] {
					t.Errorf("%s is still installed after the rollback:\n%s", pkg, out)
				}
			}
			for _, pkg := range tc.wantKept {
				if !after[pkg] {
					t.Errorf("%s was removed, though it was kept on purpose:\n%s", pkg, out)
				}
				if !strings.Contains(out, pkg) {
					t.Errorf("the rollback did not say it kept %s:\n%s", pkg, out)
				}
			}
			if !tc.keepLib && !maps.Equal(after, before) {
				t.Errorf("the installed packages differ from before the run:\nbefore %v\nafter %v", before, after)
			}
			if tc.keepLib {
				// Leave the device as the next case would find it.
				if got, code := device.exec(t, "apt-get", "remove", "-y", "libjq1", "libonig5"); code != 0 {
					t.Fatalf("cleaning up: exit %d %q", code, got)
				}
			}
		})
	}
}
