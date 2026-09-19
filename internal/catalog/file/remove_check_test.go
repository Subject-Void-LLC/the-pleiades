// Package file_test: tests of file.remove's check against the real run it
// predicts.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// These prove file.remove's check against this package's real SSH server
// and real /bin/sh. What survives on disk after a check is read with
// os.Lstat, and every answer is compared with what the real Remove run
// does from an identical starting state.

// removeCheckStart builds one of the starting states the check has to
// tell apart, under a fresh temp directory, and returns its path.
func removeCheckStart(t *testing.T, shape string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target")
	switch shape {
	case "absent":
	case "file":
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	case "empty directory":
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
	case "full directory":
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
		// A hidden entry, on purpose: ls -A must count it and rmdir does.
		if err := os.WriteFile(filepath.Join(path, ".keep"), nil, 0o600); err != nil {
			t.Fatalf("filling %s: %v", path, err)
		}
	default:
		t.Fatalf("unknown shape %q", shape)
	}
	return path
}

// TestCheckRemove_PredictsWhatARealRunDoes covers every answer the check
// gives, against the real run as its control.
func TestCheckRemove_PredictsWhatARealRunDoes(t *testing.T) {
	cases := []struct {
		shape       string
		recurse     bool
		wantChanged bool
		wantRefusal bool
	}{
		{shape: "absent", wantChanged: false},
		{shape: "file", wantChanged: true},
		{shape: "empty directory", wantChanged: true},
		{shape: "full directory", wantRefusal: true},
		{shape: "full directory", recurse: true, wantChanged: true},
	}

	for _, tc := range cases {
		name := tc.shape
		if tc.recurse {
			name += " with recurse"
		}
		t.Run(name, func(t *testing.T) {
			server := startRemoveServer(t, -1)
			extra := map[string]any{"recurse": tc.recurse}

			path := removeCheckStart(t, tc.shape)
			rc := newRemoveContext(server)
			checked, checkErr := file.CheckRemove(context.Background(), rc, newRemoveDevice(server), removeParams(path, extra))

			// Whatever the check answered, the path is exactly as it was.
			if tc.shape != "absent" {
				assertRemoveStillThere(t, path)
			}
			if tc.shape == "full directory" {
				assertRemoveStillThere(t, filepath.Join(path, ".keep"))
			}
			assertNoFileInverse(t, rc.stats)

			// The control.
			realPath := removeCheckStart(t, tc.shape)
			real, realErr := file.Remove(context.Background(), newRemoveContext(server), newRemoveDevice(server), removeParams(realPath, extra))

			if tc.wantRefusal {
				if checkErr == nil || realErr == nil {
					t.Fatalf("expected both to refuse, got check %v and real %v", checkErr, realErr)
				}
				if !strings.Contains(checkErr.Error(), "not empty") || !strings.Contains(checkErr.Error(), "recurse") {
					t.Errorf("the check's refusal %q must say why and name the parameter that allows it", checkErr)
				}
				return
			}
			if checkErr != nil || realErr != nil {
				t.Fatalf("expected both to succeed, got check %v and real %v", checkErr, realErr)
			}
			if checked.Changed != tc.wantChanged || real.Changed != tc.wantChanged {
				t.Errorf("Changed: check %v, real %v, want %v", checked.Changed, real.Changed, tc.wantChanged)
			}
			if tc.wantChanged {
				assertRemoveGone(t, realPath)
				_, after := removeDiff(t, rc)
				if after["exists"] != false {
					t.Errorf("the check predicted after %v, want the path gone", after)
				}
			}
		})
	}
}

// TestCheckRemove_IsDeclared pins the registration the engine reads.
func TestCheckRemove_IsDeclared(t *testing.T) {
	d, ok := collection.Lookup("file.remove")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("file.remove must declare check support with a Check function, got %+v", d.Manifest)
	}
}
