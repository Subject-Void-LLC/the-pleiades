// Tests of readPackagesFile.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadPackagesFile: a list by spaces and lines reads back in order, an
// option or a word that is no import path is refused, and a missing file
// says so.
func TestReadPackagesFile(t *testing.T) {
	dir := t.TempDir()
	write := func(text string) string {
		p := filepath.Join(dir, "packages.txt")
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	got, err := readPackagesFile(write("example.com/a example.com/b\r\nexample.com/c/...\n"))
	if err != nil || strings.Join(got, " ") != "example.com/a example.com/b example.com/c/..." {
		t.Errorf("readPackagesFile = %q, %v", got, err)
	}
	for _, bad := range []string{"example.com/a -exec=rm", "example.com/a & calc"} {
		if _, err := readPackagesFile(write(bad)); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := readPackagesFile(filepath.Join(dir, "none.txt")); err == nil || !strings.Contains(err.Error(), "reading the packages file") {
		t.Errorf("a missing file: %v", err)
	}
}
