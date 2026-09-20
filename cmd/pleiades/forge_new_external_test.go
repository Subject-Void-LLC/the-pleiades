// Package main: tests of `pleiades forge new-external`'s go.mod handling.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestForgeNewExternal_GoMod covers the command's half of the go.mod
// decision: a program is written with a go.mod of its own; --no-go-mod,
// in either position, writes none; and a target that already holds a
// go.mod is refused, nothing is written, and that go.mod is left exactly
// as it was.
func TestForgeNewExternal_GoMod(t *testing.T) {
	t.Run("a module of its own", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "prog")
		captureStdout(t, func() {
			if err := runForgeNewExternal([]string{"acme.motd.read", "--dir", dir}); err != nil {
				t.Fatalf("new-external: %v", err)
			}
		})
		if got, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || !strings.HasPrefix(string(got), "module acme-motd-read\n") {
			t.Errorf("go.mod = %q, %v", got, err)
		}
	})

	for name, args := range map[string][]string{
		"the flag after the name":  {"acme.motd.read", "--no-go-mod", "--dir"},
		"the flag before the name": {"--no-go-mod", "acme.motd.read", "--dir"},
	} {
		t.Run("--no-go-mod, "+name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "prog")
			captureStdout(t, func() {
				if err := runForgeNewExternal(append(args, dir)); err != nil {
					t.Fatalf("new-external: %v", err)
				}
			})
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); !os.IsNotExist(err) {
				t.Errorf("--no-go-mod wrote a go.mod (stat: %v)", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
				t.Errorf("--no-go-mod wrote no program: %v", err)
			}
		})
	}

	t.Run("an existing go.mod is refused and untouched", func(t *testing.T) {
		dir := t.TempDir()
		mine := []byte("module example.com/mine\n\ngo 1.22\n\nrequire example.com/other v1.0.0\n")
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), mine, 0o600); err != nil {
			t.Fatal(err)
		}
		err := runForgeNewExternal([]string{"acme.motd.read", "--dir", dir})
		if err == nil || !strings.Contains(err.Error(), "go.mod") {
			t.Fatalf("new-external over an existing go.mod = %v, want it refused naming go.mod", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "go.mod")); string(got) != string(mine) {
			t.Errorf("the existing go.mod changed to %q", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "main.go")); !os.IsNotExist(err) {
			t.Error("a refused run still wrote the program")
		}
	})
}
