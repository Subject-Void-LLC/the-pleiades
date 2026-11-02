//go:build !windows

// Tests of Locate against real paths through the real SSH harness and
// /bin/sh: the identities it reads are compared with the kernel's own.
package remotefile_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// identity is the "dev:ino" the kernel gives path, following links.
func identity(t *testing.T, path string) string {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d:%d", uint64(st.Dev), st.Ino) // #nosec G115 -- a device number is never negative
}

func TestLocate(t *testing.T) {
	conn := connect(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	locate := func(target string) wire.Location {
		t.Helper()
		loc, err := remotefile.Locate(context.Background(), conn, target)
		if err != nil {
			t.Fatalf("Locate(%s): %v", target, err)
		}
		if !wire.ValidLocation(loc) {
			t.Fatalf("Locate(%s) = %+v, not a valid location", target, loc)
		}
		for _, d := range loc.Dirs {
			if got := identity(t, d.Path); got != d.ID {
				t.Errorf("Locate(%s): %s has identity %s, the kernel says %s", target, d.Path, d.ID, got)
			}
		}
		return loc
	}

	// A link above the path is resolved, and the directories along it are
	// the physical ones, from the parent up to /.
	loc := locate(filepath.Join(link, "file"))
	if loc.Path != filepath.Join(real, "file") || loc.Dirs[0].Path != real || loc.Dirs[len(loc.Dirs)-1].Path != "/" {
		t.Errorf("through a link: %+v", loc)
	}
	// Names below the deepest directory that exists are kept as written.
	if loc := locate(filepath.Join(link, "new", "deeper", "file")); loc.Path != filepath.Join(real, "new", "deeper", "file") || loc.Dirs[0].Path != real {
		t.Errorf("a tail not made yet: %+v", loc)
	}
	// A directory that exists is among its own identities; a link at the
	// path itself is the link, never followed.
	if loc := locate(filepath.Join(real, "sub")); loc.Dirs[0].Path != filepath.Join(real, "sub") {
		t.Errorf("a directory: %+v", loc)
	}
	if loc := locate(link); loc.Path != link || loc.Dirs[0].Path != root {
		t.Errorf("a link itself: %+v", loc)
	}
	if loc := locate("/"); loc.Path != "/" || len(loc.Dirs) != 1 {
		t.Errorf("/: %+v", loc)
	}
	for _, bad := range []string{"relative", root + "/a/../b", root + "/"} {
		if _, err := remotefile.Locate(context.Background(), conn, bad); err == nil {
			t.Errorf("Locate(%q) located it", bad)
		}
	}
}

// TestLocate_RefusesWhatItCannotRead: a directory it cannot enter, and output
// of any other shape than its script prints, are errors, never a location.
func TestLocate_RefusesWhatItCannotRead(t *testing.T) {
	if os.Geteuid() != 0 {
		conn := connect(t)
		sealed := filepath.Join(t.TempDir(), "sealed")
		if err := os.Mkdir(sealed, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })
		if _, err := remotefile.Locate(context.Background(), conn, filepath.Join(sealed, "file")); err == nil {
			t.Error("a directory it cannot enter was located")
		}
	}
	for name, out := range map[string]string{
		"too few fields":        "/d\x00\x00",
		"a relative directory":  "d\x00\x00-\x001:2\n",
		"an unknown self flag":  "/\x00\x00x\x001:2\n",
		"too few identities":    "/d\x00\x00-\x001:2\n",
		"an identity of digits": "/\x00\x00-\x00x\n",
	} {
		if loc, err := remotefile.ParseLocation(out, "f"); err == nil {
			t.Errorf("%s: parsed as %+v", name, loc)
		}
	}
}

// TestRemovable asks of real entries: in a directory the login may write, in
// one it may not, in one with the sticky bit that it owns, and in one that is
// not there.
func TestRemovable(t *testing.T) {
	conn := connect(t)
	root := t.TempDir()
	dirs := map[string]os.FileMode{"open": 0o700, "sealed": 0o555, "sticky": 0o777 | os.ModeSticky}
	for name, mode := range dirs {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
	// The login owns the sticky directory and the entry, as the owner of a
	// file in /tmp does.
	want := map[string]bool{"open": true, "sticky": true, "missing": false}
	if os.Geteuid() != 0 {
		want["sealed"] = false
	}
	for name, allowed := range want {
		got, err := remotefile.Removable(context.Background(), conn, filepath.Join(root, name, "f"))
		if err != nil || got != allowed {
			t.Errorf("Removable(%s/f) = %v, %v; want %v", name, got, err, allowed)
		}
	}
}

// TestRemovableScript_RefusesWhatItCannotVouchFor runs the script directly:
// under a sticky directory whose owner, like the entry's, is someone else (a
// stand-in id gives the login another uid), and under BusyBox, whose test
// says writable to root for anything and sees no read-only mount, so the
// script refuses root there and reads the mount table for anyone else.
func TestRemovableScript_RefusesWhatItCannotVouchFor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sticky := filepath.Join(root, "with space")
	if err := os.Mkdir(sticky, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sticky, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sticky, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sticky, 0o700) })
	run := func(argv []string, env []string, extra ...string) int {
		t.Helper()
		args := append(append(argv[1:], "-c", remotefile.RemovableScript, "sh", sticky, filepath.Join(sticky, "f")), extra...)
		cmd := exec.Command(argv[0], args...) // #nosec G204 -- the test's own script
		cmd.Env = append(os.Environ(), env...)
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		return 0
	}
	if os.Geteuid() == 0 {
		t.Skip("needs a login other than root: as root every case here is allowed or refused for that reason alone")
	}
	sh := []string{"sh"}
	if got := run(sh, nil); got != 0 {
		t.Fatalf("the owner of a sticky directory and its entry: exit %d, want 0", got)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "id"), []byte("#!/bin/sh\necho 54321\n"), 0o700); err != nil { // #nosec G306 -- the stand-in must run
		t.Fatal(err)
	}
	if got := run(sh, []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}); got != 1 {
		t.Errorf("a login owning neither the sticky directory nor its entry: exit %d, want 1", got)
	}

	busybox, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("needs busybox: no BusyBox on this machine to run the script under")
	}
	bb := []string{busybox, "sh"}
	if got := run(bb, nil); got != 0 {
		t.Errorf("under BusyBox, a login on a read-write mount: exit %d, want 0", got)
	}
	escaped := strings.ReplaceAll(sticky, " ", "\\040")
	for name, tc := range map[string]struct {
		mountinfo string
		want      int
	}{
		"a read-only mount above it":               {"22 1 8:1 / / rw - ext4 /dev/sda1 rw\n40 22 8:2 / " + root + " ro,relatime - ext4 /dev/sda2 ro\n", 1},
		"a read-only mount named with a space":     {"22 1 8:1 / / rw - ext4 /dev/sda1 rw\n41 22 8:3 / " + escaped + " ro - ext4 /dev/sda3 ro\n", 1},
		"a read-write mount under a read-only one": {"22 1 8:1 / / ro - ext4 /dev/sda1 ro\n40 22 8:2 / " + root + " rw,relatime - ext4 /dev/sda2 rw\n", 0},
		"a read-only mount beside it":              {"22 1 8:1 / / rw - ext4 /dev/sda1 rw\n40 22 8:2 / " + root + "x ro - ext4 /dev/sda2 ro\n", 0},
	} {
		mi := filepath.Join(t.TempDir(), "mountinfo")
		if err := os.WriteFile(mi, []byte(tc.mountinfo), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := run(bb, nil, mi); got != tc.want {
			t.Errorf("under BusyBox, %s: exit %d, want %d", name, got, tc.want)
		}
	}
	if unshare, err := exec.LookPath("unshare"); err == nil && exec.Command(unshare, "-r", "true").Run() == nil { // #nosec G204 -- probing for user namespaces
		if got := run(append([]string{unshare, "-r"}, bb...), nil); got != 1 {
			t.Errorf("under BusyBox as root: exit %d, want 1", got)
		}
	}
}
