// Package facts_test: an access control list that denies anyone, or withholds
// read from a user it names, makes a file private whatever its mode (Phase
// 41c, owner decision 2026-11-19), on the file itself or on a directory above
// it. Each case is first shown keeping a plain digest without the list, so
// the private answer is the list's doing.
package facts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// aclDenies puts on path an entry that withholds perm ("read" for a file,
// "search" for a directory) from someone, by this platform's own tool, and
// reports whether the file system took one.
func aclDenies(t *testing.T, path, perm string) bool {
	t.Helper()
	var tries [][]string
	switch runtime.GOOS {
	case "darwin":
		tries = [][]string{{"chmod", "+a", "everyone deny " + perm, path}}
	default:
		// A POSIX.1e entry naming nobody without the permission (r-- keeps
		// read but not search on a directory), else an NFSv4 deny entry.
		posix, nfs := "---", "r"
		if perm == "search" {
			posix, nfs = "r--", "x"
		}
		tries = [][]string{
			{"setfacl", "-m", "u:nobody:" + posix, path},
			{"setfacl", "-a", "0", "everyone@:" + nfs + "::deny", path},
		}
	}
	for _, c := range tries {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err == nil {
			return true
		} else {
			t.Logf("%v: %v: %s", c, err, out)
		}
	}
	return false
}

// gatherNeedsPOSIX skips on Windows, whose files probe is not the POSIX one.
func gatherNeedsPOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX files probe; Windows reads its own")
	}
}

// worldDir makes a directory every user may search, under the system's own
// temporary directory with no link on the way (on macOS /tmp is a link to
// /private/tmp, and a linked directory makes a file private by itself).
func worldDir(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Skipf("no /tmp: %v", err)
	}
	root, err := os.MkdirTemp(base, "pleiades-acl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for d := filepath.Dir(root); d != "/"; d = filepath.Dir(d) {
		if info, err := os.Stat(d); err != nil || info.Mode().Perm()&0o001 == 0 {
			t.Skipf("%s keeps others out, so nothing under it reads as world-readable", d)
		}
	}
	return root
}

// gatherFile gathers one path's file fact on this machine.
func gatherFile(t *testing.T, path string) map[string]any {
	t.Helper()
	server := startGatherServer(t)
	rc := newGatherContext(server)
	if _, err := gatherRun(t, server, rc, map[string]any{"gather_subset": []any{"!all", "!min"}, "files": []any{path}}); err != nil {
		t.Fatal(err)
	}
	return gatherItem(t, rc, gatherFileKey(path))
}

func TestGather_AnACLThatWithholdsReadMakesAFilePrivate(t *testing.T) {
	gatherNeedsPOSIX(t)
	for _, on := range []string{"the file", "its directory"} {
		t.Run(on, func(t *testing.T) {
			dir := filepath.Join(worldDir(t), "d")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "f")
			if err := os.WriteFile(file, []byte("pin1234\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := gatherFile(t, file); got["world_readable"] != true || got["sha256"] == nil {
				t.Fatalf("without a list %v, want a plain digest", got)
			}
			target, perm := file, "read"
			if on == "its directory" {
				target, perm = dir, "search"
			}
			if !aclDenies(t, target, perm) {
				t.Skip("this file system or system takes no access control list here")
			}
			got := gatherFile(t, file)
			if got["sha256"] != nil || got["world_readable"] != false {
				t.Fatalf("with a list on %s withholding %s: %v, want no plain digest", on, perm, got)
			}
		})
	}
}

// TestGather_AnACLThatOnlyGrantsLeavesAFileWorldReadable: an entry that only
// allows (macOS) restricts nobody.
func TestGather_AnACLThatOnlyGrantsLeavesAFileWorldReadable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("an allow-only list in ls -le's form is macOS's")
	}
	file := filepath.Join(worldDir(t), "f")
	if err := os.WriteFile(file, []byte("pin1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("chmod", "+a", "staff allow read", file).CombinedOutput(); err != nil {
		t.Skipf("chmod +a: %v: %s", err, out)
	}
	if got := gatherFile(t, file); got["world_readable"] != true || got["sha256"] == nil {
		t.Fatalf("an allow-only list: %v, want a plain digest", got)
	}
}
