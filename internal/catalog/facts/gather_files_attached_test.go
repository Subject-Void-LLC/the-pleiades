// Package facts_test: what a path carries beside its content (owner decision
// 2026-11-19): its extended attributes are listed by name, digested as one
// keyed value, and a change to a value alone changes the digest.
package facts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// setXattr sets a user extended attribute on path by this platform's own
// tool, and reports whether one was set.
func setXattr(t *testing.T, path, name, value string) bool {
	t.Helper()
	var c []string
	switch runtime.GOOS {
	case "darwin":
		c = []string{"xattr", "-w", name, value, path}
	case "freebsd":
		c = []string{"setextattr", "user", name, value, path}
	default:
		c = []string{"setfattr", "-n", "user." + name, "-v", value, path}
	}
	if _, err := exec.LookPath(c[0]); err != nil {
		return false
	}
	out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
	if err != nil {
		t.Logf("%v: %v: %s", c, err, out)
	}
	return err == nil
}

// gatherAttached gathers path's file_attached fact on this machine.
func gatherAttached(t *testing.T, path string) map[string]any {
	t.Helper()
	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"!all", "!min"}, "files": []any{path}}); err != nil {
		t.Fatal(err)
	}
	return gatherItem(t, rc, "file_attached."+gatherFileKey(path)[len("file."):])
}

func TestGather_AFilesExtendedAttributesAreListedAndDigested(t *testing.T) {
	gatherNeedsPOSIX(t)
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !setXattr(t, file, "pleiades", "one") {
		t.Skip("no tool or file system here sets an extended attribute")
	}
	first := gatherAttached(t, file)
	names, _ := first["names"].([]any)
	if first["kind"] != "xattrs" || first["state"] != "listed" || len(names) == 0 || first[collection.RawDigestField] == nil || first["sha256"] != nil {
		t.Fatalf("file_attached = %v, want the attribute listed and one raw digest", first)
	}
	if !setXattr(t, file, "pleiades", "two") {
		t.Fatal("the attribute could not be changed")
	}
	second := gatherAttached(t, file)
	if second[collection.RawDigestField] == first[collection.RawDigestField] {
		t.Fatalf("changing the attribute's value left the digest %v", second[collection.RawDigestField])
	}
}
