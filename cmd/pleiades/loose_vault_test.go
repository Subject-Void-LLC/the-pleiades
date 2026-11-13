// Tests through the real binary that a master key another account could read
// is refused before any secret is read, naming the chmod that fixes it.
package main_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCLI_ALooseMasterKeyIsRefused: a project whose master.key was loosened
// by hand to 0644 refuses add-credential and seal-key
// alike, naming the file and chmod 600; the vault is left as it was. After
// chmod 600 the same command works.
func TestCLI_ALooseMasterKeyIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes do not apply on Windows, where the check checks nothing by design")
	}
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "web01", "--username", "u", "--password-stdin", "--no-device"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	key := filepath.Join(dir, ".pleiades", "master.key")
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add-credential", "web02", "--username", "u", "--password-stdin", "--no-device"},
		{"seal-key"},
	} {
		out, err := runPleiadesWithStdin(t, dir, "s3cret\n", args...)
		if err == nil || !strings.Contains(out, "chmod 600 .pleiades/master.key") && !strings.Contains(out, "chmod 600 "+key) {
			t.Errorf("%s with a 0644 master key: %v\n%s", args[0], err, out)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml")); string(after) != string(before) {
		t.Error("a refused command changed the vault")
	}
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithStdin(t, dir, "s3cret\n", "add-credential", "web02", "--username", "u", "--password-stdin", "--no-device"); err != nil {
		t.Errorf("after chmod 600: %v\n%s", err, out)
	}
}
