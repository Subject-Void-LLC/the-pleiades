// Package exec_test: tests of exec.command's and exec.shell's checks.
package exec_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestGuardedChecks covers both methods' checks on the recording SSH
// harness: an unguarded call answers "cannot check this call" without
// connecting at all; a guarded one sends only the guard's test -e, never
// the command, and reports the skip the real run would make or predicts
// the run; and the real run from the same state agrees on changed.
func TestGuardedChecks(t *testing.T) {
	for _, fqcn := range []string{"exec.command", "exec.shell"} {
		t.Run(fqcn, func(t *testing.T) {
			d, ok := collection.Lookup(fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", fqcn)
			}
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran")
			existing := filepath.Join(dir, "present")
			if err := os.WriteFile(existing, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name        string
				guard       map[string]any
				wantChanged bool
				cannot      bool
			}{
				{"unguarded", nil, false, true},
				{"creates, present", map[string]any{"creates": existing}, false, false},
				{"creates, absent", map[string]any{"creates": filepath.Join(dir, "nothing")}, true, false},
				{"removes, present", map[string]any{"removes": existing}, true, false},
				{"removes, absent", map[string]any{"removes": filepath.Join(dir, "nothing")}, false, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					srv, err := remoteexectest.Start(remoteexectest.Options{})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(srv.Close)
					server := testSSHServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
					params := map[string]any{"cmd": "touch " + marker, "insecure_skip_host_key_verify": true}
					for k, v := range tc.guard {
						params[k] = v
					}
					rc := newStubContext(server)
					checked, err := d.Check(context.Background(), rc, newDevice(server, ""), params)
					commands := srv.Commands()
					if tc.cannot {
						var cannot *collection.CannotCheckError
						if !errors.As(err, &cannot) {
							t.Fatalf("check = %v, want a CannotCheckError", err)
						}
						if len(commands) != 0 {
							t.Errorf("an unguarded check connected and sent %v", commands)
						}
						return
					}
					if err != nil {
						t.Fatalf("check: %v", err)
					}
					for _, c := range commands {
						if !strings.Contains(c, "test -e ") || strings.Contains(c, "touch") {
							t.Errorf("the check sent %q, not just the guard", c)
						}
					}
					if _, err := os.Stat(marker); err == nil {
						t.Fatal("the check ran the command")
					}
					if checked.Changed != tc.wantChanged {
						t.Errorf("predicted changed %v, want %v", checked.Changed, tc.wantChanged)
					}

					ran, err := d.Invoke(context.Background(), newStubContext(server), newDevice(server, ""), params)
					if err != nil {
						t.Fatalf("run: %v", err)
					}
					if ran.Changed != checked.Changed {
						t.Errorf("the run changed %v, the check predicted %v", ran.Changed, checked.Changed)
					}
					_ = os.Remove(marker)
				})
			}
		})
	}
}

// TestGuardedChecks_FailWhenTheyCannotRecord covers a guarded check whose
// guard would let the command run, and which cannot record that answer.
// It fails naming the method, as the real run fails when it cannot record
// its result, rather than predicting a run with no stats behind it; and
// the command is never sent.
func TestGuardedChecks_FailWhenTheyCannotRecord(t *testing.T) {
	for _, fqcn := range []string{"exec.command", "exec.shell"} {
		t.Run(fqcn, func(t *testing.T) {
			d, _ := collection.Lookup(fqcn)
			srv, err := remoteexectest.Start(remoteexectest.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			server := testSSHServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
			marker := filepath.Join(t.TempDir(), "ran")
			params := map[string]any{"cmd": "touch " + marker, "creates": marker, "insecure_skip_host_key_verify": true}
			rc := newStubContext(server)
			rc.statErr = errStat
			_, err = d.Check(context.Background(), rc, newDevice(server, ""), params)
			if !errors.Is(err, errStat) || !strings.HasPrefix(err.Error(), fqcn+": ") {
				t.Errorf("check = %v, want the stat failure named for %s", err, fqcn)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Errorf("the check ran the command: %v", statErr)
			}
		})
	}
}
