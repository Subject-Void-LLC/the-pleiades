//go:build linux

// Tests for the Linux reader's cgroup part, against a stand-in cgroup tree.
package headroom

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCgroupMemory reads a cgroup v2 limit and use from a tree laid out as
// /sys/fs/cgroup is, and reports none for the root cgroup, a "max" limit,
// or a tree that is missing.
func TestCgroupMemory(t *testing.T) {
	root := t.TempDir()
	scope := filepath.Join(root, "user.slice", "run-r1.scope")
	if err := os.MkdirAll(scope, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cgroup := filepath.Join(root, "cgroup")
	write(cgroup, "0::/user.slice/run-r1.scope\n")
	write(filepath.Join(scope, "memory.max"), "4718592000\n")
	write(filepath.Join(scope, "memory.current"), "1048576000\n")
	limit, used, ok := cgroupMemory(cgroup, root)
	if !ok || limit != 4500 || used != 1000 {
		t.Errorf("cgroupMemory = %.0f, %.0f, %v; want 4500, 1000, true", limit, used, ok)
	}
	write(filepath.Join(scope, "memory.max"), "max\n")
	if _, _, ok := cgroupMemory(cgroup, root); ok {
		t.Error("a cgroup with no limit reported one")
	}
	write(cgroup, "0::/\n")
	if _, _, ok := cgroupMemory(cgroup, root); ok {
		t.Error("the root cgroup reported a limit")
	}
	if _, _, ok := cgroupMemory(filepath.Join(root, "missing"), root); ok {
		t.Error("a missing cgroup file reported a limit")
	}
	// Read on this machine folds the limit in: available never exceeds it.
	m, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if m.LimitMB > 0 && m.AvailableMB > m.LimitMB {
		t.Errorf("Read = %+v: available past the cgroup limit", m)
	}
}
