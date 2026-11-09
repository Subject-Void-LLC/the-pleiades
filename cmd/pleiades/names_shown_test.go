// Package main: no command adds a device whose name would not show as
// itself. add-host and import-kubeconfig refuse the name they were given,
// with recordname's reason naming the character, and `pleiades inventory
// sync` reports such an upstream device as refused, quoted, and adds the
// rest. Every character outside printable ASCII is built from its code
// point, so this file holds none (FAILURE_PATTERNS 256, 834).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddHost_RefusesANameThatWouldNotShowAsItself: each name is refused
// with the reason naming the character and nothing is written; a name in a
// real script with the joiner its spelling needs is added.
func TestAddHost_RefusesANameThatWouldNotShowAsItself(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, tc := range []struct{ name, says string }{
		{"sw2\npleiades: approved", "U+000A at byte 3"},
		{"sw\x1b[2J1", "U+001B at byte 2"},
		{"abc" + string(rune(0x202E)) + "def", "U+202E at byte 3"},
		{"web1" + string(rune(0x200B)), "U+200B at byte 4"},
	} {
		err := runAddHost([]string{tc.name, "--dir", dir, "--type", "linux_server", "--set", "host=10.0.0.1"})
		if err == nil || !strings.Contains(err.Error(), "host not added") || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("add-host %q = %v, want a refusal naming %s", tc.name, err, tc.says)
		}
	}
	if names := writtenHostNames(t, dir); len(names) != 0 {
		t.Errorf("refused add-hosts wrote %v", names)
	}
	persian := "sw-" + string([]rune{0x0645, 0x06CC, 0x200C, 0x062E})
	if err := runAddHost([]string{persian, "--dir", dir, "--type", "linux_server", "--set", "host=10.0.0.1"}); err != nil {
		t.Fatalf("add-host %q = %v, want nil", persian, err)
	}
	if names := writtenHostNames(t, dir); !names[persian] {
		t.Errorf("add-host %q wrote %v", persian, names)
	}
}

// TestImportKubeconfig_RefusesANameThatWouldNotShowAsItself imports a valid
// kubeconfig under a name holding a newline. The refusal names the
// character and comes before the kubeconfig is read, so no credential is
// stored.
func TestImportKubeconfig_RefusesANameThatWouldNotShowAsItself(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config")
	kubeconfig := kubeconfigText("https://k.example:6443", clusterCAPEM(t), "", "    token: imported-token-value\n")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readOrEmpty(t, credentialsPath(dir))
	err := runImportKubeconfig([]string{"k8s\npleiades: approved", "--dir", dir, "--kubeconfig", path})
	if err == nil || !strings.Contains(err.Error(), "cluster not imported") || !strings.Contains(err.Error(), "U+000A at byte 3") {
		t.Errorf("import-kubeconfig = %v, want the name refused naming the newline", err)
	}
	if after := readOrEmpty(t, credentialsPath(dir)); after != before {
		t.Errorf("a refused import changed the credential file:\n%s", after)
	}
}

// TestRunInventorySync_RefusesANameThatWouldNotShowAsItself syncs a
// Catalyst Center whose devices are named "sw1", one whose hostname would
// print a line of its own and one holding a direction override, through
// the real plugin and the project's own inventory file. The sync succeeds,
// sw1 is added, and the two others are counted and listed as refused,
// each quoted so its character shows as an escape, with the reason, and
// not written.
func TestRunInventorySync_RefusesANameThatWouldNotShowAsItself(t *testing.T) {
	dir := newSyncProject(t)
	newline := "sw2\npleiades: approved"
	override := "abc" + string(rune(0x202E)) + "def"
	device := func(id, hostname, ip string) string {
		// The hostname is a JSON string's content, so its newline is
		// written as the escape a controller would send.
		quoted, err := json.Marshal(hostname)
		if err != nil {
			t.Fatal(err)
		}
		return `{"id":"` + id + `","hostname":` + string(quoted) + `,"managementIpAddress":"` + ip + `","family":"Switches and Hubs",` +
			`"softwareType":"IOS-XE","softwareVersion":"17.12.1","reachabilityStatus":"Reachable","collectionStatus":"Managed"}`
	}
	srv := fakeControllerServing(t, `{"response":[`+
		device("id-sw1", "sw1", "10.0.0.1")+","+
		device("id-newline", newline, "10.0.0.2")+","+
		device("id-override", override, "10.0.0.3")+`]}`)

	out := captureStdout(t, func() {
		if err := runInventorySync([]string{"--plugin", "catalyst_center", "--endpoint", srv.URL, "--dir", dir}); err != nil {
			t.Fatalf("inventory sync: %v", err)
		}
	})
	for _, want := range []string{"2 refused", "devices needing review", `"sw2\npleiades: approved"`, `"abc\u202edef"`, "U+000A at byte 3", "U+202E at byte 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("the sync report does not say %q:\n%s", want, out)
		}
	}
	for _, raw := range []string{"\npleiades: approved", string(rune(0x202E))} {
		if strings.Contains(out, raw) {
			t.Errorf("the sync report printed %q raw:\n%q", raw, out)
		}
	}

	names := writtenHostNames(t, dir)
	if !names["sw1"] {
		t.Errorf("sw1 was not added beside the refused records: %v", names)
	}
	for _, name := range []string{newline, override} {
		if names[name] {
			t.Errorf("the refused record %q was written to the inventory", name)
		}
	}
}
