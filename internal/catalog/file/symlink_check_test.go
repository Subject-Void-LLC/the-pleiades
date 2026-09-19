// Package file_test: tests of file.symlink's check.
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

// TestCheckSymlink_PredictsWhatARealRunLeaves runs the registered check on
// a recording SSH harness from each start a link meets, with the real run
// from the same start as its control: only stats are sent and nothing on
// disk moves; a link already pointing at src predicts no change and one
// that is missing or points elsewhere predicts one; the prediction matches
// what the real run leaves on every key it states; no undo is recorded;
// and a regular file in the way is refused by both.
func TestCheckSymlink_PredictsWhatARealRunLeaves(t *testing.T) {
	d, ok := collection.Lookup("file.symlink")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatal("file.symlink does not declare a check")
	}
	for _, tc := range []struct {
		name        string
		start       func(dir, link string)
		wantChanged bool
	}{
		{"absent", func(string, string) {}, true},
		{"a link to somewhere else", func(dir, link string) { _ = os.Symlink(filepath.Join(dir, "elsewhere"), link) }, true},
		{"already right", func(dir, link string) { _ = os.Symlink(filepath.Join(dir, "target"), link) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, srv := recordingServer(t)
			device := newDirDevice(server)
			setup := func() (string, map[string]any) {
				dir := t.TempDir()
				link := filepath.Join(dir, "link")
				tc.start(dir, link)
				p := dirParams(link)
				p["src"] = filepath.Join(dir, "target")
				return link, p
			}

			link, params := setup()
			targetBefore, _ := os.Readlink(link)
			checkRC := newDirContext(server)
			checked, err := d.Check(context.Background(), checkRC, device, params)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			onlyStats(t, srv.Commands())
			if targetAfter, _ := os.Readlink(link); targetAfter != targetBefore {
				t.Errorf("the check moved the link from %q to %q", targetBefore, targetAfter)
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
			ran, err := file.Symlink(context.Background(), runRC, device, runParams)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if ran.Changed != checked.Changed {
				t.Errorf("the run changed %v, the check predicted %v", ran.Changed, checked.Changed)
			}
			_, actual := diffOf(t, runRC)
			knownKeysMatch(t, predictedWithoutPaths(predicted), predictedWithoutPaths(actual))
		})
	}

	t.Run("a regular file in the way is refused by both", func(t *testing.T) {
		server, _ := recordingServer(t)
		dir := t.TempDir()
		link := filepath.Join(dir, "link")
		if err := os.WriteFile(link, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		params := dirParams(link)
		params["src"] = filepath.Join(dir, "target")
		_, checkErr := d.Check(context.Background(), newDirContext(server), newDirDevice(server), params)
		_, runErr := file.Symlink(context.Background(), newDirContext(server), newDirDevice(server), params)
		if checkErr == nil || runErr == nil || checkErr.Error() != runErr.Error() {
			t.Errorf("check refused with %v, run with %v; want the same refusal", checkErr, runErr)
		}
	})
}

// predictedWithoutPaths drops the link's target from a diff half. The
// check and its control run in two temporary directories, so their
// targets differ by directory; whether the target is the requested one is
// what the kind and the Changed comparison already establish.
func predictedWithoutPaths(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k != "target" {
			out[k] = v
		}
	}
	return out
}
