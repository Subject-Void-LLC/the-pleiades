// Tests of remove-host and remove-credential through the command functions:
// a host named by another is refused until --leave-references, its row and
// state go, its credential goes only with --credential, and its host keys go
// only with --forget-host-keys and only when no other device is reached at
// the same address.
package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

func TestRemoveHost_TakesAHostAndWhatItWasGivenApart(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)
	t.Setenv("PLEIADES_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(runInit([]string{"--dir", dir}))
	must(runAddHost([]string{"r1", "--dir", dir, "--type", "linux_server", "--set", "host=192.0.2.10"}))
	must(runAddHost([]string{"r2", "--dir", dir, "--type", "linux_server", "--set", "host=192.0.2.20"}))
	must(runAddHost([]string{"twin", "--dir", dir, "--type", "linux_server", "--set", "host=192.0.2.20", "--set", "note=r1"}))
	key, err := credential.ResolveMasterKey(dir)
	must(err)
	for _, name := range []string{"r1", "r2"} {
		must(credential.SaveFileStore(dir, key, name, credential.Credential{Username: "ops", Password: "not-a-real-one"}))
	}
	keyLine := " ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"
	must(os.WriteFile(knownHosts, []byte("192.0.2.10"+keyLine+"192.0.2.20"+keyLine+"192.0.2.99"+keyLine), 0o600))

	// twin names r1 in a property, so r1 stays until that is accepted.
	if err := runRemoveHost([]string{"r1", "--dir", dir}); err == nil || !strings.Contains(err.Error(), "twin (note)") {
		t.Fatalf("a named host: %v", err)
	}
	var out removedHost
	text := captureStdout(t, func() {
		must(runRemoveHost([]string{"r1", "--dir", dir, "--leave-references", "--credential", "--forget-host-keys", "--json"}))
	})
	must(json.Unmarshal([]byte(text), &out))
	if out.Host != "r1" || out.ID == "" || !out.CredentialRemoved || out.HostKeysRemoved != 1 {
		t.Errorf("removed %+v", out)
	}
	// r2 shares its address with twin: its keys stay, and its credential
	// stays without --credential.
	text = captureStdout(t, func() { must(runRemoveHost([]string{"r2", "--dir", dir, "--forget-host-keys"})) })
	if !strings.Contains(text, "twin is also reached at 192.0.2.20") {
		t.Errorf("a shared address: %s", text)
	}
	hosts, err := inv.ReadHosts(filepath.Join(dir, inv.DefaultInventoryFilename))
	must(err)
	if len(hosts) != 1 || hosts[0].Name != "twin" {
		t.Errorf("hosts left: %+v", hosts)
	}
	lines, err := os.ReadFile(knownHosts)
	must(err)
	if strings.Contains(string(lines), "192.0.2.10 ") || !strings.Contains(string(lines), "192.0.2.20 ") || !strings.Contains(string(lines), "192.0.2.99 ") {
		t.Errorf("known_hosts now:\n%s", lines)
	}
	if err := runRemoveHost([]string{"r1", "--dir", dir}); err == nil {
		t.Error("a host already removed was removed again")
	}

	// remove-credential takes r2's, which remove-host left.
	captureStdout(t, func() { must(runRemoveCredential([]string{"r2", "--dir", dir})) })
	if err := runRemoveCredential([]string{"r2", "--dir", dir}); err == nil || !strings.Contains(err.Error(), "no credential") {
		t.Errorf("a second removal: %v", err)
	}
}

// TestRemoveHost_AConsoleNodeHasNoHostKeysToForget proves --forget-host-keys
// on a node reached only through a console server removes the node and says it
// had no host keys of its own, rather than refusing the whole command: the
// keys trust-host trusted for it are the console server's, which stay.
func TestRemoveHost_AConsoleNodeHasNoHostKeysToForget(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(runInit([]string{"--dir", dir}))
	must(runAddHost([]string{"sw1", "--dir", dir, "--type", "cisco_switch", "--set", "transport=console",
		"--set", "console_server=cml", "--set", "console_line=00000000-0000-4000-8000-000000000001"}))
	keyLine := "192.0.2.161 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"
	must(os.WriteFile(knownHosts, []byte(keyLine), 0o600))

	var out removedHost
	text := captureStdout(t, func() { must(runRemoveHost([]string{"sw1", "--dir", dir, "--forget-host-keys", "--json"})) })
	must(json.Unmarshal([]byte(text), &out))
	if out.Host != "sw1" || out.HostKeysRemoved != 0 || !strings.Contains(out.HostKeysNote, "sw1 is not reached over SSH at an address") {
		t.Errorf("a console node: %+v", out)
	}
	hosts, err := inv.ReadHosts(filepath.Join(dir, inv.DefaultInventoryFilename))
	must(err)
	if len(hosts) != 0 {
		t.Errorf("hosts left: %+v", hosts)
	}
	if lines, err := os.ReadFile(knownHosts); err != nil || string(lines) != keyLine {
		t.Errorf("the console server's keys changed: %q, %v", lines, err)
	}
}

// TestRemoveHost_ForgetsTheNetconfListenersKeysToo proves --forget-host-keys
// drops the "[host]:port" lines trust-host --netconf writes for a device's
// NETCONF listener as well as its SSH lines: a cisco_router's on 830 by
// default, a generic_netconf's on its own netconf_port, while a listener
// another device is also reached at keeps its keys.
func TestRemoveHost_ForgetsTheNetconfListenersKeysToo(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(runInit([]string{"--dir", dir}))
	must(runAddHost([]string{"r1", "--dir", dir, "--type", "cisco_router", "--set", "host=192.0.2.10", "--set", "netconf_enabled=true"}))
	must(runAddHost([]string{"nc1", "--dir", dir, "--type", "generic_netconf", "--set", "host=192.0.2.30", "--set", "netconf_port=2830"}))
	must(runAddHost([]string{"nc2", "--dir", dir, "--type", "generic_netconf", "--set", "host=192.0.2.40"}))
	must(runAddHost([]string{"nc2-twin", "--dir", dir, "--type", "generic_netconf", "--set", "host=192.0.2.40"}))
	keyLine := " ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"
	rsaLine := " ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7\n"
	must(os.WriteFile(knownHosts, []byte(
		"192.0.2.10"+keyLine+
			"[192.0.2.10]:830"+keyLine+
			"[192.0.2.10]:830"+rsaLine+
			"[192.0.2.30]:2830"+keyLine+
			"[192.0.2.30]:830"+keyLine+
			"[192.0.2.40]:830"+keyLine), 0o600))

	var out removedHost
	text := captureStdout(t, func() { must(runRemoveHost([]string{"r1", "--dir", dir, "--forget-host-keys", "--json"})) })
	must(json.Unmarshal([]byte(text), &out))
	if out.HostKeysRemoved != 3 || out.HostKeysKept != "" {
		t.Errorf("a cisco_router: %+v", out)
	}
	text = captureStdout(t, func() { must(runRemoveHost([]string{"nc1", "--dir", dir, "--forget-host-keys"})) })
	if !strings.Contains(text, "removed 1 known_hosts line(s) for 192.0.2.30:2830") {
		t.Errorf("a generic_netconf on its own port: %s", text)
	}
	text = captureStdout(t, func() { must(runRemoveHost([]string{"nc2", "--dir", dir, "--forget-host-keys"})) })
	if !strings.Contains(text, "nc2-twin is also reached at 192.0.2.40:830") {
		t.Errorf("a shared listener: %s", text)
	}
	lines, err := os.ReadFile(knownHosts)
	must(err)
	// Only the line no removed device listens at, and the shared one, stay.
	if want := "[192.0.2.30]:830" + keyLine + "[192.0.2.40]:830" + keyLine; string(lines) != want {
		t.Errorf("known_hosts now:\n%s\nwant:\n%s", lines, want)
	}
}
