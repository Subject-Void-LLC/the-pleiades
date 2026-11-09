// Tests that the error a command fails with reaches the terminal with
// nothing in it a terminal acts on, when the error carries text a remote
// system wrote: here, a device name a sync source sent, inside the error
// syncplugin.Reconcile stops with.
package main

import (
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRun_EscapesAnUpstreamNameInACommandsError syncs a Catalyst Center
// through the real command line, then syncs it again reporting the device
// the first sync added under a crafted hostname: one holding an ANSI
// escape and a right-to-left override, and one holding a newline followed
// by a line that reads like the CLI's own. Reconcile fails creating it
// (its ID is already in the inventory) with an error naming it. The error
// reaches stderr with the name quoted, so nothing in it is raw and no line
// of stderr begins with the upstream's text, and the command still fails.
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

	// Each sync reports sw1 renamed, so Reconcile fails creating it. The
	// names are written with JSON's own escapes, so the server sends the
	// raw characters.
	for _, tc := range []struct {
		name     string
		upstream string
		raw      []string
		shown    []string
	}{
		{
			name:     "an ANSI escape and a direction override",
			upstream: `sw\u001b[2Jevil\u202eflip`,
			raw:      []string{"\x1b", "\u202e"},
			shown:    []string{`"sw\x1b[2Jevil\u202eflip"`},
		},
		{
			name:     "a newline starting a line of its own",
			upstream: `sw2\npleiades: approved, nothing to review`,
			raw:      []string{"\npleiades: approved"},
			shown:    []string{`"sw2\npleiades: approved, nothing to review"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			renamed := fakeControllerServing(t, device(tc.upstream))
			var code int
			stderr := captureStderr(t, func() {
				captureStdout(t, func() {
					code = run([]string{"inventory", "sync", "--plugin", "catalyst_center", "--endpoint", renamed.URL, "--dir", dir})
				})
			})
			if code == 0 {
				t.Fatalf("the sync exits 0; want it to fail creating the renamed device:\n%s", stderr)
			}
			if !strings.Contains(stderr, "creating") {
				t.Fatalf("stderr does not carry Reconcile's error:\n%q", stderr)
			}
			for _, raw := range tc.raw {
				if strings.Contains(stderr, raw) {
					t.Errorf("the error reached stderr with %q raw:\n%q", raw, stderr)
				}
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "pleiades: approved") {
					t.Errorf("a line of stderr is the upstream's own:\n%q", stderr)
				}
			}
			for _, want := range tc.shown {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr does not show the name quoted, %s:\n%s", want, stderr)
				}
			}
		})
	}
}

// TestRun_QuotesAnUpstreamNameInAFailedSave stores a device whose name
// holds a newline followed by a line that reads like the CLI's own, then
// syncs it again with a changed property into a project directory nothing
// may write, so the file repository's Save fails. Reconcile's error wraps
// the repository's, and neither may carry the name raw: no line of stderr
// begins with the upstream's text.
func TestRun_QuotesAnUpstreamNameInAFailedSave(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory refuses writes only to an unprivileged user on a POSIX system")
	}
	dir := newSyncProject(t)
	device := func(version string) string {
		return `{"response":[{"id":"id-sw3","hostname":"sw3\npleiades: approved, nothing to review","managementIpAddress":"10.0.0.3","family":"Switches and Hubs",` +
			`"softwareType":"IOS-XE","softwareVersion":"` + version + `","reachabilityStatus":"Reachable","collectionStatus":"Managed"}]}`
	}
	// One controller, so its own record is unchanged and only the device's
	// save is attempted.
	// Stored atomically, since the server's goroutine reads it.
	var version atomic.Value
	version.Store("17.12.1")
	srv := fakeControllerAnswering(t, func() string { return device(version.Load().(string)) })
	captureStdout(t, func() {
		if err := runInventorySync([]string{"--plugin", "catalyst_center", "--endpoint", srv.URL, "--dir", dir}); err != nil {
			t.Fatalf("first sync: %v", err)
		}
	})

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	version.Store("17.15.1")
	var code int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			code = run([]string{"inventory", "sync", "--plugin", "catalyst_center", "--endpoint", srv.URL, "--dir", dir})
		})
	})
	if code == 0 {
		t.Fatalf("the sync exits 0; want its save to fail:\n%s", stderr)
	}
	if !strings.Contains(stderr, "saving") || !strings.Contains(stderr, "failed to write inventory state for") {
		t.Fatalf("stderr does not carry Reconcile's and the repository's errors:\n%q", stderr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "pleiades: approved") {
			t.Errorf("a line of stderr is the upstream's own:\n%q", stderr)
		}
	}
	if n := strings.Count(stderr, `"sw3\npleiades: approved, nothing to review"`); n != 2 {
		t.Errorf("stderr quotes the name %d times, want 2 (Reconcile's error and the repository's):\n%s", n, stderr)
	}
}
