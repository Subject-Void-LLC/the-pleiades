// Tests for bsdcheck's machine: the POSIX steps it is given, which
// tools/internal/oscheck tests run under sh, here held to FreeBSD's
// places.
package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewLeg: the machine is driven from a pleiades built under the
// worktree's own git directory, unpacks its trees in a directory of its
// own, finds go where the lab installs it without root, and writes its
// archive into the project, which file.copy reads from.
func TestNewLeg(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	leg, err := newLeg("/lab", "bsd-1")
	if err != nil {
		t.Fatal(err)
	}
	f := leg.(freebsd)
	if f.Pleiades.Binary != filepath.Join(real, ".git", "pleiades-bsdcheck", "pleiades") || f.Pleiades.Project != "/lab" || f.Pleiades.Device != "bsd-1" {
		t.Errorf("pleiades = %+v", f.Pleiades)
	}
	if f.posix.Dir != ".pleiades-bsdcheck" || f.posix.Files != ".bsdcheck" || f.posix.Wrap != "" || !strings.HasPrefix(f.posix.Path, "$HOME/sdk/go/bin:") {
		t.Errorf("posix = %+v", f.posix)
	}
	if script := f.posix.TestScript("abc", 1, []string{"a/b"}); !strings.Contains(script, `export PATH="$PATH:$HOME/sdk/go/bin:`) {
		t.Errorf("the test script does not put the lab's go on the PATH:\n%s", script)
	}
}

// TestCheck names bsdcheck's settings and file.
func TestCheck(t *testing.T) {
	if check.ProjectKey != "PLEIADES_BSD_PROJECT" || check.DeviceKey != "PLEIADES_BSD_DEVICE" || check.EnvFile != "tests/bsd/.env" || check.OS != "FreeBSD" {
		t.Errorf("check = %+v", check)
	}
}
