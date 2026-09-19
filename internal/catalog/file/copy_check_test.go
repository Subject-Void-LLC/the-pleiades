// Package file_test: tests of file.copy's check.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestCheckCopy_PredictsWhatARealRunLeaves runs the registered check on a
// recording SSH harness from each start a copy meets, with the real run
// from the same start as its control: only reads are sent and the file is
// untouched; the change prediction matches the run (converged content and
// attributes predict none); the predicted diff, checksum and size
// included, matches what the run leaves on every key it states; no undo is
// recorded; and a directory in the way is refused by both.
func TestCheckCopy_PredictsWhatARealRunLeaves(t *testing.T) {
	d, ok := collection.Lookup("file.copy")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatal("file.copy does not declare a check")
	}
	write := func(content string, mode os.FileMode) func(string) {
		return func(p string) {
			_ = os.WriteFile(p, []byte(content), mode)
			_ = os.Chmod(p, mode)
		}
	}
	for _, tc := range []struct {
		name        string
		start       func(path string)
		mode        string
		wantChanged bool
	}{
		{"absent, with a mode", func(string) {}, "0640", true},
		{"absent, no mode", func(string) {}, "", true},
		{"same bytes, same mode", write("hello\n", 0o640), "0640", false},
		{"same bytes, other mode", write("hello\n", 0o600), "0644", true},
		{"other bytes, no mode named", write("old\n", 0o600), "", true},
		{"other bytes, other mode", write("old\n", 0o600), "0640", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, srv := recordingServer(t)
			device := newDirDevice(server)
			setup := func() (string, map[string]any) {
				path := filepath.Join(t.TempDir(), "f")
				tc.start(path)
				p := dirParams(path)
				delete(p, "path")
				p["dest"] = path
				p["content"] = "hello\n"
				if tc.mode != "" {
					p["mode"] = tc.mode
				}
				return path, p
			}

			path, params := setup()
			before, _ := os.ReadFile(path)
			checkRC := newDirContext(server)
			checked, err := d.Check(context.Background(), checkRC, device, params)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			onlyStats(t, srv.Commands())
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Error("the check changed the file's content")
			}
			if checked.Changed != tc.wantChanged {
				t.Errorf("the check predicts changed %v, want %v", checked.Changed, tc.wantChanged)
			}
			if _, recorded := checkRC.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}
			_, predicted := diffOf(t, checkRC)

			_, runParams := setup()
			runRC := newDirContext(server)
			ran, err := file.Copy(context.Background(), runRC, device, runParams)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if ran.Changed != checked.Changed {
				t.Errorf("the run changed %v, the check predicted %v", ran.Changed, checked.Changed)
			}
			_, actual := diffOf(t, runRC)
			knownKeysMatch(t, predicted, actual)
			if checkRC.stats["checksum"] != runRC.stats["checksum"] {
				t.Errorf("checksum stat: check %v, run %v", checkRC.stats["checksum"], runRC.stats["checksum"])
			}
		})
	}

	t.Run("a directory in the way is refused by both", func(t *testing.T) {
		server, _ := recordingServer(t)
		params := dirParams("")
		delete(params, "path")
		params["dest"] = t.TempDir()
		params["content"] = "x"
		_, checkErr := d.Check(context.Background(), newDirContext(server), newDirDevice(server), params)
		_, runErr := file.Copy(context.Background(), newDirContext(server), newDirDevice(server), params)
		if checkErr == nil || runErr == nil || checkErr.Error() != runErr.Error() {
			t.Errorf("check refused with %v, run with %v; want the same refusal", checkErr, runErr)
		}
	})
}
