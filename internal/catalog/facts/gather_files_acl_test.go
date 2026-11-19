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
		// The user nobody, not everyone: everyone includes this login,
		// which would then not reach the file at all.
		tries = [][]string{{"chmod", "+a", "user:nobody deny " + perm, path}}
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

// worldDir makes a directory every user may search, with no link on the way
// (on macOS /tmp is a link to /private/tmp, and a linked directory makes a
// file private by itself), under the first of /tmp, the system's temporary
// directory and the home directory whose every ancestor others may search.
func worldDir(t *testing.T) string {
	t.Helper()
	return worldDirWhere(t, func(string) bool { return true })
}

// worldDirWhere is worldDir under the first base ok accepts, given a scratch
// directory made there (an ACL test asks for one whose file system takes a
// list: FreeBSD's /tmp is often UFS mounted without acls).
func worldDirWhere(t *testing.T, ok func(dir string) bool) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	// /pleiades-acl is the ZFS pool the FreeBSD lab machine keeps for these
	// tests (its UFS is mounted without acls); elsewhere it does not exist.
	for _, base := range []string{"/tmp", os.TempDir(), home, aclPoolMount} {
		real, err := filepath.EvalSymlinks(base)
		if err != nil || real == "" {
			continue
		}
		if !searchableUp(real) {
			continue
		}
		root, err := os.MkdirTemp(real, "pleiades-world-")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		if real == aclPoolMount {
			ownDataset(t, root)
		}
		if err := os.Chmod(root, 0o755); err != nil || !ok(root) {
			continue
		}
		return root
	}
	t.Skip("no directory here that others may search to (and that the test's file system needs)")
	return ""
}

// aclPoolMount and aclPool are the FreeBSD lab machine's ZFS pool for ACL
// tests (its runbook check-08-zfs-acl), whose login may create and destroy
// datasets in it.
const (
	aclPoolMount = "/pleiades-acl"
	aclPool      = "pleiadesacl"
)

// ownDataset mounts a dataset of this run's own at root, destroyed when the
// test ends, so nothing one run leaves in the pool reaches another; where the
// pool refuses one, root stays a plain directory in it, which its own Cleanup
// removes.
func ownDataset(t *testing.T, root string) {
	t.Helper()
	name := aclPool + "/" + filepath.Base(root)
	if out, err := exec.Command("zfs", "create", "-o", "mountpoint="+root, name).CombinedOutput(); err != nil {
		t.Logf("zfs create %s: %v: %s (using a plain directory in the pool)", name, err, out)
		return
	}
	t.Cleanup(func() {
		if out, err := exec.Command("zfs", "destroy", "-f", name).CombinedOutput(); err != nil {
			t.Errorf("zfs destroy %s: %v: %s", name, err, out)
		}
	})
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
}

// searchableUp reports whether dir and every directory above it let others
// search it.
func searchableUp(dir string) bool {
	for d := dir; ; d = filepath.Dir(d) {
		info, err := os.Stat(d)
		if err != nil || info.Mode().Perm()&0o001 == 0 {
			return false
		}
		if d == "/" || filepath.Dir(d) == d {
			return true
		}
	}
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
			probe := func(root string) bool {
				f := filepath.Join(root, "probe")
				return os.WriteFile(f, nil, 0o644) == nil && aclDenies(t, f, "read")
			}
			dir := filepath.Join(worldDirWhere(t, probe), "d")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "f")
			if err := os.WriteFile(file, []byte("pin1234\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := gatherFile(t, file); got["world_readable"] != true || got["sha256"] == nil || got["acl"] != "listed" {
				t.Fatalf("without a list %v, want a plain digest and the lists read", got)
			}
			target, perm := file, "read"
			if on == "its directory" {
				target, perm = dir, "search"
			}
			if !aclDenies(t, target, perm) {
				t.Skip("this file system or system takes no access control list here")
			}
			got := gatherFile(t, file)
			if got["sha256"] != nil || got["world_readable"] != false || got["readable"] != true {
				t.Fatalf("with a list on %s withholding %s: %v, want it read and no plain digest", on, perm, got)
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

// TestGather_AListTheSystemCannotShowIsSaidSo: where no tool can show an
// access control list (BusyBox's ls marks none, and there is no getfacl), the
// mode decides as before and the file fact says the lists went unseen
// (owner decision 2026-11-19), so a gap never reads as a clean answer.
func TestGather_AListTheSystemCannotShowIsSaidSo(t *testing.T) {
	gatherNeedsPOSIX(t)
	if runtime.GOOS == "darwin" {
		t.Skip("macOS's ls always shows a list")
	}
	if _, err := exec.LookPath("getfacl"); err == nil {
		t.Skip("getfacl is here, so the lists are read")
	}
	if exec.Command("ls", "--version").Run() == nil {
		t.Skip("this ls marks a list with +, so the lists are read")
	}
	file := filepath.Join(worldDir(t), "f")
	if err := os.WriteFile(file, []byte("pin1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := gatherFile(t, file)
	if got["acl"] != "unlisted" || got["world_readable"] != true || got["sha256"] == nil {
		t.Fatalf("on a system that cannot show a list: %v, want acl unlisted and the mode's answer", got)
	}
}
