// Package file_test: tests of the file checks that cannot finish their
// reads or record their answers.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestFileChecks_FailWhenTheyCannotReadOrRecord covers every file check's
// last steps: a check that cannot record its diff or its stats, or cannot
// finish the one read a real run's refusal depends on, fails naming the
// method, as the real run does, rather than reporting a decision with
// nothing behind it. Whatever was at the path is left exactly as it was.
func TestFileChecks_FailWhenTheyCannotReadOrRecord(t *testing.T) {
	file := func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	absent := func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") }
	fullDir := func(t *testing.T) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "inside"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	with := func(extra map[string]any) func(string) map[string]any {
		return func(path string) map[string]any {
			p := dirParams(path)
			for k, v := range extra {
				p[k] = v
			}
			return p
		}
	}
	copyTo := func(path string) map[string]any {
		p := with(map[string]any{"dest": path, "content": "new\n"})(path)
		delete(p, "path")
		return p
	}

	for _, tc := range []struct {
		name    string
		fqcn    string
		start   func(*testing.T) string
		params  func(string) map[string]any
		failKey string
		budget  int
	}{
		{"copy, the diff", "file.copy", file, copyTo, sdk.StatDiff, -1},
		{"copy, the stats", "file.copy", file, copyTo, "checksum", -1},
		{"directory, the path", "file.directory", absent, dirParams, "path", -1},
		{"directory, the diff", "file.directory", absent, dirParams, sdk.StatDiff, -1},
		{"permissions, the diff", "file.permissions", file, with(map[string]any{"mode": "0640"}), sdk.StatDiff, -1},
		{"permissions, the stats", "file.permissions", file, with(map[string]any{"mode": "0640"}), "owner", -1},
		{"symlink, the diff", "file.symlink", absent, with(map[string]any{"src": "/etc/hostname"}), sdk.StatDiff, -1},
		{"touch, the diff", "file.touch", file, dirParams, sdk.StatDiff, -1},
		{"touch, the stats", "file.touch", file, dirParams, "dest", -1},
		// The stat takes the one session allowed, so whether the directory
		// is empty, which decides the real run's refusal, cannot be read.
		{"remove, a directory's contents cannot be read", "file.remove", fullDir, dirParams, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			server := startDirServerWithSessionBudget(t, tc.budget)
			path := tc.start(t)
			before, _ := os.Lstat(path)
			rc := newDirContext(server)
			rc.failKey = tc.failKey
			_, err := d.Check(context.Background(), rc, newDirDevice(server), tc.params(path))
			if err == nil || !strings.HasPrefix(err.Error(), tc.fqcn+": ") {
				t.Errorf("check = %v, want a failure named for %s", err, tc.fqcn)
			}
			after, _ := os.Lstat(path)
			if (before == nil) != (after == nil) || (before != nil && (before.Mode() != after.Mode() || before.Size() != after.Size())) {
				t.Errorf("the check changed %s", path)
			}
		})
	}
}
