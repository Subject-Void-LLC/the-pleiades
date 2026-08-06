package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	inv "github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// newSyncProject scaffolds a project with a stored credential, the state a
// user is in immediately before their first `inventory sync`.
func newSyncProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := runAddCredential([]string{"catalyst_center", "--username", "devnetuser", "--password", "secret", "--dir", dir}); err != nil {
		t.Fatalf("add-credential: %v", err)
	}
	return dir
}

// fakeController serves the two endpoints a sync touches, reporting one
// switch. It is enough to drive the whole command without reaching the
// network.
func fakeController(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/dna/system/api/v1/auth/token":
			user, pass, ok := r.BasicAuth()
			if !ok || user != "devnetuser" || pass != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
		case r.Header.Get("X-Auth-Token") != "tok":
			w.WriteHeader(http.StatusUnauthorized)
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			_, _ = w.Write([]byte(`{"response":1}`))
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device"):
			_, _ = w.Write([]byte(`{"response":[{"id":"id-sw1","hostname":"sw1",` +
				`"managementIpAddress":"10.0.0.1","family":"Switches and Hubs",` +
				`"softwareType":"IOS-XE","softwareVersion":"17.12.1",` +
				`"reachabilityStatus":"Reachable","collectionStatus":"Managed"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// hostCount reports how many hosts the project's inventory document holds.
func hostCount(t *testing.T, dir string) int {
	t.Helper()

	hosts, err := inv.ReadHosts(filepath.Join(dir, "inventory.yaml"))
	if err != nil {
		t.Fatalf("reading inventory: %v", err)
	}
	return len(hosts)
}

// TestRunInventory_Dispatch covers the nested dispatcher's own behavior,
// mirroring how runForge is tested.
func TestRunInventory_Dispatch(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "no subcommand", args: nil, wantErr: true},
		{name: "unknown subcommand", args: []string{"frobnicate"}, wantErr: true},
		{name: "help", args: []string{"--help"}},
		{name: "help word", args: []string{"help"}},
		{name: "plugins", args: []string{"plugins"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runInventory(tt.args)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestRunInventoryPlugins_ListsRegisteredPlugins proves the discovery
// command names what --plugin accepts, so a user does not have to read the
// source to find out.
func TestRunInventoryPlugins_ListsRegisteredPlugins(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runInventoryPlugins(nil); err != nil {
			t.Fatalf("inventory plugins: %v", err)
		}
	})

	for _, want := range []string{"catalyst_center", "static_yaml", "implemented"} {
		if !strings.Contains(out, want) {
			t.Errorf("plugin listing does not mention %q:\n%s", want, out)
		}
	}
}

// TestRunInventorySync_Rejects covers the argument errors that happen
// before any upstream call.
func TestRunInventorySync_Rejects(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "no plugin named",
			args:    []string{"--dir", dir},
			wantErr: "usage:",
		},
		{
			name:    "unknown plugin",
			args:    []string{"--plugin", "netbox", "--dir", dir},
			wantErr: "unknown sync plugin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runInventorySync(tt.args)
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestRunInventorySync_UnknownPluginNamesTheKnownOnes proves the error is
// actionable rather than merely correct: it lists what the user could have
// typed instead.
func TestRunInventorySync_UnknownPluginNamesTheKnownOnes(t *testing.T) {
	err := runInventorySync([]string{"--plugin", "nope", "--dir", t.TempDir()})
	if err == nil {
		t.Fatal("expected an unknown plugin to fail")
	}
	if !strings.Contains(err.Error(), "catalyst_center") {
		t.Errorf("error = %q, want it to list the known plugins", err)
	}
}

// TestRunInventorySync_WritesInventory is the command's happy path: a real
// sync against a controller, writing the project's inventory document.
func TestRunInventorySync_WritesInventory(t *testing.T) {
	dir := newSyncProject(t)
	srv := fakeController(t)

	if before := hostCount(t, dir); before != 0 {
		t.Fatalf("a freshly scaffolded project already holds %d hosts", before)
	}

	out := captureStdout(t, func() {
		err := runInventorySync([]string{
			"--plugin", "catalyst_center",
			"--endpoint", srv.URL,
			"--dir", dir,
		})
		if err != nil {
			t.Fatalf("inventory sync: %v", err)
		}
	})

	if !strings.Contains(out, "2 added") {
		t.Errorf("expected the report to name two added devices (the switch and the controller), got:\n%s", out)
	}
	if got := hostCount(t, dir); got != 2 {
		t.Errorf("inventory holds %d hosts after sync, want 2", got)
	}
}

// TestRunInventorySync_ReadOnlyIsADryRun proves --read-only reports what
// would change and writes nothing. This is the simulate-first proof, and
// the distinction that matters is that it reports rather than aborting: an
// earlier version failed on the first device, which answered nothing.
func TestRunInventorySync_ReadOnlyIsADryRun(t *testing.T) {
	dir := newSyncProject(t)
	srv := fakeController(t)

	out := captureStdout(t, func() {
		err := runInventorySync([]string{
			"--plugin", "catalyst_center",
			"--endpoint", srv.URL,
			"--read-only",
			"--dir", dir,
		})
		if err != nil {
			t.Fatalf("inventory sync --read-only: %v", err)
		}
	})

	if !strings.Contains(out, "read-only") {
		t.Errorf("expected the report to say it was read-only, got:\n%s", out)
	}
	if !strings.Contains(out, "would be added") {
		t.Errorf("expected the report to say what would be added, got:\n%s", out)
	}
	if got := hostCount(t, dir); got != 0 {
		t.Errorf("a read-only sync wrote %d hosts to the inventory", got)
	}
}

// TestRunInventorySync_IsIdempotent proves a second run of the command
// reports everything unchanged and does not duplicate hosts.
func TestRunInventorySync_IsIdempotent(t *testing.T) {
	dir := newSyncProject(t)
	srv := fakeController(t)
	args := []string{"--plugin", "catalyst_center", "--endpoint", srv.URL, "--dir", dir}

	if err := runInventorySync(args); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	out := captureStdout(t, func() {
		if err := runInventorySync(args); err != nil {
			t.Fatalf("second sync: %v", err)
		}
	})

	if !strings.Contains(out, "2 unchanged") {
		t.Errorf("expected the second sync to report everything unchanged, got:\n%s", out)
	}
	if got := hostCount(t, dir); got != 2 {
		t.Errorf("inventory holds %d hosts after a re-sync, want 2", got)
	}
}

// TestRunInventorySync_ReportsQuarantinedDevices proves a device the
// classifier cannot place is listed individually with its reason rather
// than only counted. Section 6g's whole point is that such a device stays
// visible for manual review.
func TestRunInventorySync_ReportsQuarantinedDevices(t *testing.T) {
	dir := newSyncProject(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/dna/system/api/v1/auth/token":
			_, _ = w.Write([]byte(`{"Token":"tok"}`))
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device/count"):
			_, _ = w.Write([]byte(`{"response":1}`))
		case strings.HasPrefix(r.URL.Path, "/dna/intent/api/v1/network-device"):
			// A software type this codebase has no classification rule for.
			_, _ = w.Write([]byte(`{"response":[{"id":"id-x","hostname":"mystery1",` +
				`"managementIpAddress":"10.0.0.9","softwareType":"NX-OS"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	out := captureStdout(t, func() {
		err := runInventorySync([]string{
			"--plugin", "catalyst_center",
			"--endpoint", srv.URL,
			"--dir", dir,
		})
		if err != nil {
			t.Fatalf("inventory sync: %v", err)
		}
	})

	if !strings.Contains(out, "needing review") {
		t.Errorf("expected a devices-needing-review section, got:\n%s", out)
	}
	if !strings.Contains(out, "mystery1") {
		t.Errorf("expected the quarantined device to be named, got:\n%s", out)
	}
	if !strings.Contains(out, "NX-OS") {
		t.Errorf("expected the quarantine reason to explain why, got:\n%s", out)
	}
}

// TestRunInventorySync_FailsOnUnreachableEndpoint proves an unreachable
// controller is an error rather than an empty successful sync. An empty
// fleet and an unreachable controller must not look alike.
func TestRunInventorySync_FailsOnUnreachableEndpoint(t *testing.T) {
	dir := newSyncProject(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	srv.Close() // closed immediately: nothing is listening

	err := runInventorySync([]string{
		"--plugin", "catalyst_center",
		"--endpoint", srv.URL,
		"--dir", dir,
	})
	if err == nil {
		t.Fatal("expected a sync against an unreachable controller to fail")
	}
	if got := hostCount(t, dir); got != 0 {
		t.Errorf("a failed sync wrote %d hosts", got)
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// what was written.
//
// The commands under test print their report rather than returning it,
// which is the right shape for a CLI and the wrong shape for assertions, so
// this is what bridges the two.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}

	original := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		done <- b.String()
	}()

	func() {
		defer func() {
			os.Stdout = original
			_ = w.Close()
		}()
		fn()
	}()

	out := <-done
	_ = r.Close()
	return out
}
