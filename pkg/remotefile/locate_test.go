//go:build !windows

// Tests of Locate against real paths through the real SSH harness and
// /bin/sh: the identities it reads are compared with the kernel's own.
package remotefile_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
