// Tests that the error a command fails with reaches the terminal with
// nothing in it a terminal acts on, when the error carries text a remote
// system wrote: here, a device name a sync source sent, inside the error
// syncplugin.Reconcile stops with.
package main

import (
	"strings"
	"testing"
)

// TestRun_EscapesAnUpstreamNameInACommandsError syncs a Catalyst Center
// twice through the real command line. The second sync reports the device
// the first one added under a new hostname holding an ANSI escape and a
// right-to-left override, so Reconcile fails creating it (its ID is
// already in the inventory) with an error naming it. The error reaches
// stderr with both escaped, and the command still fails.
func TestRun_EscapesAnUpstreamNameInACommandsError(t *testing.T) {
	dir := newSyncProject(t)
	device := func(hostname string) string {
		return `{"response":[{"id":"id-sw1","hostname":"` + hostname + `","managementIpAddress":"10.0.0.1","family":"Switches and Hubs",` +
			`"softwareType":"IOS-XE","softwareVersion":"17.12.1","reachabilityStatus":"Reachable","collectionStatus":"Managed"}]}`
	}
	first := fakeControllerServing(t, device("sw1"))
	captureStdout(t, func() {
		if err := runInventorySync([]string{"--plugin", "catalyst_center", "--endpoint", first.URL, "--dir", dir}); err != nil {
			t.Fatalf("first sync: %v", err)
		}
	})

	// JSON's own escapes, so the server sends the raw characters.
	renamed := fakeControllerServing(t, device(`sw\u001b[2Jevil\u202eflip`))
	var code int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			code = run([]string{"inventory", "sync", "--plugin", "catalyst_center", "--endpoint", renamed.URL, "--dir", dir})
		})
	})
	if code == 0 {
		t.Fatalf("the second sync exits 0; want it to fail creating the renamed device:\n%s", stderr)
	}
	if !strings.Contains(stderr, "creating") {
		t.Fatalf("stderr does not carry Reconcile's error:\n%q", stderr)
	}
	for _, raw := range []string{"\x1b", "\u202e"} {
		if strings.Contains(stderr, raw) {
			t.Errorf("the error reached stderr with %q raw:\n%q", raw, stderr)
		}
	}
	for _, want := range []string{`sw\x1b[2Jevil`, `\u202eflip`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not show %s escaped:\n%s", want, stderr)
		}
	}
}
