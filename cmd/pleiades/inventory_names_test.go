// Package main: `pleiades inventory sync` skips an upstream device whose name
// no URL can carry, says so in its report, and adds the rest.
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// TestRunInventorySync_RefusesANameNoURLCarries syncs a Catalyst Center
// whose devices are named "sw1", "." and "..", through the real plugin and
// the project's own inventory file. The sync succeeds, sw1 is added (beside
// the controller, which a Catalyst Center sync records too), and the two
// others are counted and listed as refused, with the reason, and not
// written.
func TestRunInventorySync_RefusesANameNoURLCarries(t *testing.T) {
	dir := newSyncProject(t)
	device := func(id, hostname, ip string) string {
		return `{"id":"` + id + `","hostname":"` + hostname + `","managementIpAddress":"` + ip + `","family":"Switches and Hubs",` +
			`"softwareType":"IOS-XE","softwareVersion":"17.12.1","reachabilityStatus":"Reachable","collectionStatus":"Managed"}`
	}
	srv := fakeControllerServing(t, `{"response":[`+
		device("id-sw1", "sw1", "10.0.0.1")+","+
		device("id-dot", ".", "10.0.0.2")+","+
		device("id-dotdot", "..", "10.0.0.3")+`]}`)

	out := captureStdout(t, func() {
		if err := runInventorySync([]string{"--plugin", "catalyst_center", "--endpoint", srv.URL, "--dir", dir}); err != nil {
			t.Fatalf("inventory sync: %v", err)
		}
	})
	for _, want := range []string{"2 refused", "devices needing review", `"."`, `".."`, "steps between folders"} {
		if !strings.Contains(out, want) {
			t.Errorf("the sync report does not say %q:\n%s", want, out)
		}
	}

	hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatalf("ReadHosts: %v", err)
	}
	written := map[string]bool{}
	for _, h := range hosts {
		written[h.Name] = true
	}
	if !written["sw1"] {
		t.Errorf("sw1 was not added beside the refused records: %v", hosts)
	}
	for _, name := range []string{".", ".."} {
		if written[name] {
			t.Errorf("the refused record %q was written to the inventory", name)
		}
	}
}
