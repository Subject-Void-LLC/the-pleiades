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
