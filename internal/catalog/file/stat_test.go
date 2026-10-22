// Tests for file.stat against the real-shell SSH harness the touch tests
// use: what it reports is held to the local filesystem itself (os.Lstat),
// and nothing at the path changes.
package file_test

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestStat_IsAReadOnlyMethodWhoseCheckIsTheSameRead(t *testing.T) {
	d, ok := collection.Lookup("file.stat")
	if !ok || d.Manifest.Status != collection.StatusImplemented {
		t.Fatalf("file.stat is not an implemented method: %+v", d.Manifest.Status)
	}
	if !d.Manifest.Reversibility.ReadOnly || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Errorf("manifest %+v, want a read-only method with a check", d.Manifest.Reversibility)
	}
}

func TestStat_ReportsWhatIsAtThePathAndChangesNothing(t *testing.T) {
	server := startTouchServer(t)
	dir := t.TempDir()
	regular := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(regular, []byte("port: 8080\n"), 0o640); err != nil { // #nosec G306 -- a test fixture
		t.Fatal(err)
	}
	if err := os.Chmod(regular, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "current")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		want map[string]any
	}{
		{"a file", regular, map[string]any{"exists": true, "kind": "file", "mode": "0640", "owner": me.Username, "size": int64(11)}},
		{"a directory", dir, map[string]any{"exists": true, "kind": "directory", "owner": me.Username}},
		{"a symlink, not what it points to", link, map[string]any{"exists": true, "kind": "symlink", "target": regular}},
		{"nothing", filepath.Join(dir, "absent"), map[string]any{"exists": false, "kind": "absent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := os.Lstat(tc.path)
			rc := newTouchContext(server)
			res, err := file.Stat(context.Background(), rc, newTouchDevice(server), touchParams(tc.path, nil))
			if err != nil {
				t.Fatalf("Stat: %v", err)
			}
			if res.Changed {
				t.Error("a read reported a change")
			}
			if rc.stats["path"] != tc.path {
				t.Errorf("path = %v, want %s", rc.stats["path"], tc.path)
			}
			for key, want := range tc.want {
				if fmt.Sprint(rc.stats[key]) != fmt.Sprint(want) {
					t.Errorf("%s = %v, want %v (all: %v)", key, rc.stats[key], want, rc.stats)
				}
			}
			if _, ok := rc.stats["owner"]; !ok && tc.want["exists"] == true {
				t.Errorf("no owner reported for something that exists: %v", rc.stats)
			}
			if _, ok := rc.stats["owner"]; ok && tc.want["exists"] == false {
				t.Errorf("an owner reported for nothing: %v", rc.stats)
			}
			after, _ := os.Lstat(tc.path)
			if (before == nil) != (after == nil) || (before != nil && (before.ModTime() != after.ModTime() || before.Mode() != after.Mode())) {
				t.Error("reading the path changed it")
			}
		})
	}
}

func TestStat_Refusals(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	if _, err := file.Stat(context.Background(), rc, newTouchDevice(server), map[string]any{"insecure_skip_host_key_verify": true}); err == nil || !strings.Contains(err.Error(), "path") {
		t.Errorf("no path: err = %v", err)
	}
	if _, err := file.Stat(context.Background(), rc, newTouchUnreachableDevice(), touchParams("/tmp", nil)); err == nil {
		t.Error("a device with no SSH accessor was reached")
	}
	rc = newTouchContext(server)
	rc.failKey = "kind"
	if _, err := file.Stat(context.Background(), rc, newTouchDevice(server), touchParams(t.TempDir(), nil)); err == nil || !strings.Contains(err.Error(), "file.stat") {
		t.Errorf("a stat that cannot be recorded: err = %v", err)
	}
	limit := 0
	limited := startTouchServerWithLimit(t, &limit)
	if _, err := file.Stat(context.Background(), newTouchContext(limited), newTouchDevice(limited), touchParams("/tmp", nil)); err == nil {
		t.Error("Stat succeeded with its session refused")
	}
}
