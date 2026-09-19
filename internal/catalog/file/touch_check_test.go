// Package file_test: tests of file.touch's check.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// recordingServer starts the SSH harness, whose Commands() says what a
// method sent, and returns it with the server shape this package's device
// and context helpers take.
func recordingServer(t *testing.T) (dirServer, *remoteexectest.Server) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	return dirServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}, srv
}

// onlyStats fails t if any command is not one of remotefile's reads: Stat
// (the stat itself, and readlink for a link) and Checksum, the only reads
// a file method's check may send.
func onlyStats(t *testing.T, commands []string) {
	t.Helper()
	if len(commands) == 0 {
		t.Fatal("the check sent nothing, so it read nothing")
	}
	for _, c := range commands {
		if !strings.HasPrefix(c, "if [ -e ") && !strings.HasPrefix(c, "readlink '") && !strings.HasPrefix(c, "if [ -f ") {
			t.Errorf("the check sent %q, which is not a stat", c)
		}
	}
}

// knownKeysMatch fails t unless every key the prediction states has the
// value the real run left.
func knownKeysMatch(t *testing.T, predicted, actual map[string]any) {
	t.Helper()
	if len(predicted) == 0 || len(actual) == 0 {
		t.Fatalf("nothing to compare: predicted %v, actual %v", predicted, actual)
	}
	for key, want := range predicted {
		if actual[key] != want {
			t.Errorf("predicted %s = %v, the real run left %v", key, want, actual[key])
		}
	}
}

// diffOf returns a recorded diff's before and after halves.
func diffOf(t *testing.T, rc *dirContext) (before, after map[string]any) {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	before, _ = diff["before"].(map[string]any)
	after, _ = diff["after"].(map[string]any)
	return before, after
}

// TestCheckTouch_PredictsWhatARealRunLeaves runs the registered check
// against a recording SSH harness from each start a touch meets, then the
// real run from the same start as its control: the check sends only
// stats, predicts the change a touch always makes, records no undo
// instruction, and predicts exactly what the real run leaves on every key
// it states; a directory in the way is refused by both.
func TestCheckTouch_PredictsWhatARealRunLeaves(t *testing.T) {
	d, ok := collection.Lookup("file.touch")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("file.touch does not declare a check")
	}
	for _, tc := range []struct {
		name  string
		start func(path string)
		mode  string
	}{
		{"absent, with a mode", func(string) {}, "0640"},
		{"absent, no mode", func(string) {}, ""},
		{"present, wrong mode", func(p string) { _ = os.WriteFile(p, []byte("x"), 0o600) }, "0644"},
		{"present, already right", func(p string) { _ = os.WriteFile(p, []byte("x"), 0o600); _ = os.Chmod(p, 0o600) }, "0600"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, srv := recordingServer(t)
			device := newDirDevice(server)
			params := func(path string) map[string]any {
				p := dirParams(path)
				if tc.mode != "" {
					p["mode"] = tc.mode
				}
				return p
			}

			checkPath := filepath.Join(t.TempDir(), "f")
			tc.start(checkPath)
			beforeCheck, _ := os.Lstat(checkPath)
			checkRC := newDirContext(server)
			checked, err := d.Check(context.Background(), checkRC, device, params(checkPath))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			onlyStats(t, srv.Commands())
			afterCheck, _ := os.Lstat(checkPath)
			if (beforeCheck == nil) != (afterCheck == nil) || (beforeCheck != nil && (beforeCheck.Mode() != afterCheck.Mode() || !beforeCheck.ModTime().Equal(afterCheck.ModTime()))) {
				t.Error("the check changed the file")
			}
			if !checked.Changed {
				t.Error("the check predicts no change; a touch always changes the file")
			}
			if _, recorded := checkRC.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}
			_, predicted := diffOf(t, checkRC)

			runPath := filepath.Join(t.TempDir(), "f")
			tc.start(runPath)
			runRC := newDirContext(server)
			if _, err := file.Touch(context.Background(), runRC, device, params(runPath)); err != nil {
				t.Fatalf("run: %v", err)
			}
			_, actual := diffOf(t, runRC)
			knownKeysMatch(t, predicted, actual)
		})
	}

	t.Run("a directory in the way is refused by both", func(t *testing.T) {
		server, srv := recordingServer(t)
		path := t.TempDir()
		_, checkErr := d.Check(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
		_, runErr := file.Touch(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
		if checkErr == nil || runErr == nil || checkErr.Error() != runErr.Error() {
			t.Errorf("check refused with %v, run with %v; want the same refusal", checkErr, runErr)
		}
		_ = srv
	})
}
