// Tests that the error a command fails with reaches the terminal with
// nothing in it a terminal acts on, when the error carries text a remote
// system wrote: here, a device name a sync source sent, inside the error
// syncplugin.Reconcile stops with.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// TestRun_EscapesAnUpstreamNameInACommandsError stores a device through the
// real command line, renames it in the inventory file to a crafted hostname
// the way a release before the name rule could have stored it (no command
// creates a device under such a name now, recordname.Check), then syncs it
// again with a changed property into a project directory nothing may write,
// so the file repository's Save fails. One name holds an ANSI escape and a
// right-to-left override, one a newline followed by a line that reads like
// the CLI's own. Reconcile's error wraps the repository's, and the error
// reaches stderr with the name quoted in both, so nothing in it is raw and
// no line of stderr begins with the upstream's text, and the command still
// fails.
func TestRun_EscapesAnUpstreamNameInACommandsError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory refuses writes only to an unprivileged user on a POSIX system")
	}
	for _, tc := range []struct {
		name     string
		upstream string
		raw      []string
	}{
		{
			name:     "an ANSI escape and a direction override",
			upstream: "sw3\x1b[2Jevil\xe2\x80\xaeflip",
			raw:      []string{"\x1b", "\xe2\x80\xae"},
		},
		{
			name:     "a newline starting a line of its own",
			upstream: "sw3\npleiades: approved, nothing to review",
			raw:      []string{"\npleiades: approved"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newSyncProject(t)
			// One controller, so its own record is unchanged and only the
			// device's save is attempted. Stored atomically, since the
			// server's goroutine reads them.
			var hostname, version atomic.Value
			hostname.Store("sw3")
			version.Store("17.12.1")
			srv := fakeControllerAnswering(t, func() string {
				// Marshalled, so the server sends JSON's own escapes and
				// the plugin decodes them to the raw characters.
				name, err := json.Marshal(hostname.Load().(string))
				if err != nil {
					t.Error(err)
				}
				return `{"response":[{"id":"id-sw3","hostname":` + string(name) +
					`,"managementIpAddress":"10.0.0.3","family":"Switches and Hubs","softwareType":"IOS-XE","softwareVersion":"` +
					version.Load().(string) + `","reachabilityStatus":"Reachable","collectionStatus":"Managed"}]}`
			})
			captureStdout(t, func() {
				if err := runInventorySync([]string{"--plugin", "catalyst_center", "--endpoint", srv.URL, "--dir", dir}); err != nil {
					t.Fatalf("first sync: %v", err)
				}
			})
			renameStoredHost(t, dir, "sw3", tc.upstream)

			if err := os.Chmod(dir, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			hostname.Store(tc.upstream)
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
			if n := strings.Count(stderr, strconv.Quote(tc.upstream)); n != 2 {
				t.Errorf("stderr quotes the name %s %d times, want 2 (Reconcile's error and the repository's):\n%s", strconv.Quote(tc.upstream), n, stderr)
			}
		})
	}
}

// renameStoredHost rewrites the inventory file in dir by hand so the host
// stored as from is stored as to, as a file written before the name rule
// could hold it: no writer adds a host under a name the rule refuses
// (inventory.EncodeHosts), so the line is edited as text. A YAML
// double-quoted scalar reads Go's escapes as the characters they stand for.
func renameStoredHost(t *testing.T, dir, from, to string) {
	t.Helper()
	path := filepath.Join(dir, inventory.DefaultInventoryFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := "name: " + from + "\n"
	if strings.Count(string(data), line) != 1 {
		t.Fatalf("the inventory does not hold exactly one %q line:\n%s", line, data)
	}
	data = []byte(strings.Replace(string(data), line, "name: "+strconv.Quote(to)+"\n", 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err := inventory.ReadHosts(path)
	if err != nil {
		t.Fatalf("the renamed inventory does not read back: %v", err)
	}
	for _, h := range hosts {
		if h.Name == to {
			return
		}
	}
	t.Fatalf("the renamed inventory holds no host %q: %+v", to, hosts)
}
