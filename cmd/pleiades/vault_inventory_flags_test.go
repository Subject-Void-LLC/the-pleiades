// Tests through the real binary for --vault and --inventory: a run reads its
// credentials and its inventory from elsewhere and writes its journal under
// --dir; a loose --vault is refused; a --vault holding no key says so.
package main_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// splitProject makes a vault holding web01's credential, an inventory file
// in a third directory naming web01 (at a port nothing answers), and a
// project directory holding only a runbook, and returns the three paths and
// the runbook.
func splitProject(t *testing.T) (vault, inv, project, runbook string) {
	t.Helper()
	vault, project = t.TempDir(), t.TempDir()
	inv = filepath.Join(t.TempDir(), "hosts.yaml")
	if out, err := runPleiadesWithStdin(t, vault, "vault-s3cret\n", "add-credential", "web01", "--username", "ops", "--password-stdin", "--no-device"); err != nil {
		t.Fatalf("add-credential in the vault: %v\n%s", err, out)
	}
	// An inventory as init writes it, moved where only --inventory finds it.
	scaffold := t.TempDir()
	if out, err := runPleiades(t, scaffold, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if err := os.Rename(filepath.Join(scaffold, "inventory.yaml"), inv); err != nil {
		t.Fatal(err)
	}
	// add-host writes the inventory --inventory names, not --dir's.
	if out, err := runPleiades(t, project, "add-host", "web01", "--inventory", inv, "--type", "linux_server", "--set", "host=127.0.0.1", "--set", "port=1"); err != nil {
		t.Fatalf("add-host --inventory: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(project, "inventory.yaml")); err == nil {
		t.Fatal("add-host --inventory wrote --dir's inventory.yaml")
	}
	runbook = filepath.Join(project, "uptime.yaml")
	if err := os.WriteFile(runbook, []byte("# Reads web01's uptime; changes nothing.\nid: uptime\nhosts: web01\ntasks:\n  - name: uptime\n    exec.command:\n      cmd: uptime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "known_hosts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return vault, inv, project, runbook
}

// TestCLI_VaultAndInventoryElsewhere: a run given --vault and --inventory
// finds web01 in the inventory file and its credential in the vault (so it
// gets as far as dialing the device, which nothing answers), and writes its
// journal under --dir, never under --vault. The same run without --vault
// has no credential to log in with, which is what proves the vault was read.
func TestCLI_VaultAndInventoryElsewhere(t *testing.T) {
	vault, inv, project, runbook := splitProject(t)
	knownHosts := filepath.Join(project, "known_hosts")

	out, _ := runPleiades(t, project, "--known-hosts", knownHosts, "run", runbook, "--vault", vault, "--inventory", inv)
	if strings.Contains(out, "no usable authentication method") || !strings.Contains(out, "web01") {
		t.Fatalf("the run did not use the vault's credential for the inventory's web01:\n%s", out)
	}
	if strings.Contains(out, "vault-s3cret") {
		t.Errorf("the run printed the password:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, ".pleiades", "journal")); err != nil {
		t.Errorf("no journal under --dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vault, ".pleiades", "journal")); err == nil {
		t.Error("the run wrote its journal under --vault")
	}
	if _, err := os.Stat(filepath.Join(project, ".pleiades", "master.key")); err == nil {
		t.Error("the run made a master key under --dir")
	}

	without, _ := runPleiades(t, project, "--known-hosts", knownHosts, "run", runbook, "--inventory", inv)
	if !strings.Contains(without, "no usable authentication method") {
		t.Errorf("control: the run without --vault found a credential:\n%s", without)
	}
	// list-credentials reads the same vault.
	if listed, err := runPleiades(t, project, "list-credentials", "--vault", vault); err != nil || !strings.Contains(listed, "web01  ops  password") {
		t.Errorf("list-credentials --vault: %v\n%s", err, listed)
	}
}

// TestCLI_VaultFlagRefusals: a --vault whose master key is loose is refused
// naming the chmod; a --vault holding no key is refused naming the key file
// it looked for; neither makes a key anywhere.
func TestCLI_VaultFlagRefusals(t *testing.T) {
	vault, inv, project, runbook := splitProject(t)
	knownHosts := filepath.Join(project, "known_hosts")

	if runtime.GOOS != "windows" {
		key := filepath.Join(vault, ".pleiades", "master.key")
		if err := os.Chmod(key, 0o640); err != nil {
			t.Fatal(err)
		}
		out, err := runPleiades(t, project, "--known-hosts", knownHosts, "run", runbook, "--vault", vault, "--inventory", inv)
		if err == nil || !strings.Contains(out, "chmod 600 "+key) {
			t.Errorf("a loose --vault: %v\n%s", err, out)
		}
		if err := os.Chmod(key, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	empty := t.TempDir()
	out, err := runPleiades(t, project, "list-credentials", "--vault", empty)
	if err == nil || !strings.Contains(out, "holds no master key") || !strings.Contains(out, filepath.Join(empty, ".pleiades", "master.key")) {
		t.Errorf("a --vault with no key: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(empty, ".pleiades")); statErr == nil {
		t.Error("a refused --vault was given a .pleiades directory")
	}
}
