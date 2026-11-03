// The real binary's global --known-hosts option against a real sshd: it
// names the file host keys are verified against, in place of the home
// directory's, and wins over PLEIADES_KNOWN_HOSTS already set.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_KnownHostsFlagChoosesTheFileHostKeysAreVerifiedAgainst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the --known-hosts container test in short mode")
	}
	container, host, port := startReleaseGateSSHD(t)
	g := newRollbackGate(t, container, host, port)
	trusted := filepath.Join(g.home, ".ssh", "known_hosts")
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	home := map[string]string{"HOME": g.home}
	ping := []string{"adhoc", "container1", "net.ssh.ping"}

	// An empty file named by the flag refuses the device's key, though the
	// home directory's file trusts it.
	out, err := runPleiadesWithEnv(t, g.dir, home, append([]string{"--known-hosts", empty}, ping...)...)
	if err == nil || !strings.Contains(out, "unknown") {
		t.Fatalf("an empty --known-hosts file: %v\n%s", err, out)
	}
	// The trusting file named by the flag wins over an empty one in the
	// environment, in both spellings.
	withEnv := map[string]string{"HOME": g.home, "PLEIADES_KNOWN_HOSTS": empty}
	for _, flag := range [][]string{{"--known-hosts", trusted}, {"--known-hosts=" + trusted}} {
		if out, err := runPleiadesWithEnv(t, g.dir, withEnv, append(flag, ping...)...); err != nil {
			t.Errorf("%v over an empty PLEIADES_KNOWN_HOSTS: %v\n%s", flag, err, out)
		}
	}
	// A flag with no file is refused before any command runs.
	if out, err := runPleiadesWithEnv(t, g.dir, home, "--known-hosts"); err == nil || !strings.Contains(out, "needs a file") {
		t.Errorf("--known-hosts with no file: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithEnv(t, g.dir, home, "--known-hosts=", "validate"); err == nil || !strings.Contains(out, "needs a file") {
		t.Errorf("--known-hosts= with an empty value: %v\n%s", err, out)
	}
}
