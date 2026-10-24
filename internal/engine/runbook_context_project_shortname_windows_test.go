//go:build windows

// Package engine: on Windows, a project's .pleiades directory reached by its
// 8.3 short name (PLEIAD~1) is refused like .pleiades itself.
package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestProjectFiles_RefuseThePleiadesShortName: NTFS resolves a short name
// to the directory it names, so a check by name alone let PLEIAD~1 reach
// the credential store. The read and the write both refuse it, by the
// directory's identity. A volume with short names turned off has no such
// alias, and the test says so.
func TestProjectFiles_RefuseThePleiadesShortName(t *testing.T) {
	dir := projectWith(t, map[string]string{".pleiades/credentials.yaml": "secret-sentinel"})
	long := filepath.Join(dir, ".pleiades")
	buf := make([]uint16, windows.MAX_PATH)
	p, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("no short name for %s: %v", long, err)
	}
	short := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(short, ".pleiades") {
		t.Skip("short names are turned off on this volume, so .pleiades has no other name")
	}
	c := &runbookContext{projectDir: dir}
	name := short + "/credentials.yaml"
	if got, err := c.ReadProjectFile(name, 1024); err == nil || strings.Contains(string(got), "secret-sentinel") {
		t.Errorf("ReadProjectFile(%q) = %q, %v; want it refused", name, got, err)
	}
	if _, err := c.WriteProjectFile(name, []byte("x"), 0o600, true); err == nil || !strings.Contains(err.Error(), "holds its credentials") {
		t.Errorf("WriteProjectFile(%q) = %v; want it refused", name, err)
	}
}
