// Tests for where a key log may be made: its directory must exist and be a
// directory; it is kept out of a directory and everything below it by
// identity, through a link, a name differing only in case where the
// filesystem ignores case, and a bind mount made in a user namespace; the
// file is created in the directory checked even when its path is changed in
// between, and a path changed after a separate check is checked again; and
// Windows, which ignores the file's mode, is refused.
package devicetls_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// keepProject keeps a key log out of dir, named as the command line names
// the project.
func keepProject(dir string) devicetls.KeepOut {
	return devicetls.KeepOut{Dir: dir, What: "the project", Why: "whose files a run's methods can read and send to devices"}
}

// refusedHere reports whether this platform refuses every key log, having
// held CreateKeyLog to refusing one, so a test of what a key log does there
// ends with that check rather than a skip.
func refusedHere(t *testing.T) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		return false
	}
	if keyLog, err := devicetls.CreateKeyLog(filepath.Join(t.TempDir(), "run.keylog")); err == nil {
		_ = keyLog.Close()
		t.Fatal("a key log was made on Windows, which ignores its mode")
	} else if !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("the refusal on Windows is %v, want it to say why", err)
	}
	return true
}

// absent fails t when path exists.
func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists (%v), want nothing made there", path, err)
	}
}

// TestKeyLog_WindowsIsRefused holds the platform rule: Windows ignores the
// mode a file is made with, so a key log there would take its directory's
// access list, and it is refused saying so; every other platform is not.
func TestKeyLog_WindowsIsRefused(t *testing.T) {
	err := devicetls.UnsupportedOn("windows")
	if err == nil || !strings.Contains(err.Error(), "Windows") || !strings.Contains(err.Error(), "mode") {
		t.Errorf("UnsupportedOn(windows) = %v, want a refusal saying Windows ignores the mode", err)
	}
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if err := devicetls.UnsupportedOn(goos); err != nil {
			t.Errorf("UnsupportedOn(%s) = %v", goos, err)
		}
	}
	refusedHere(t)
}

// TestKeyLog_ItsDirectoryMustExist refuses a key log whose directory is
// missing or is a file, at the check and at the creation, naming the
// directory, and makes nothing.
func TestKeyLog_ItsDirectoryMustExist(t *testing.T) {
	if refusedHere(t) {
		return
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want string
	}{
		{filepath.Join(dir, "missing", "run.keylog"), "does not exist"},
		{filepath.Join(file, "run.keylog"), "is not a directory"},
	} {
		if err := devicetls.CheckKeyLogPath(tc.path); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), filepath.Dir(tc.path)) {
			t.Errorf("CheckKeyLogPath(%s) = %v, want %q naming its directory", tc.path, err, tc.want)
		}
		if keyLog, err := devicetls.CreateKeyLog(tc.path); err == nil {
			_ = keyLog.Close()
			t.Errorf("CreateKeyLog(%s) made a key log", tc.path)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CreateKeyLog(%s) = %v, want %q", tc.path, err, tc.want)
		}
	}
	absent(t, filepath.Join(dir, "missing"))
	if err := devicetls.CheckKeyLogPath(filepath.Join(dir, "run.keylog")); err != nil {
		t.Errorf("a key log in an existing directory: %v", err)
	}
	if err := devicetls.CheckKeyLogPath(file); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("CheckKeyLogPath on an existing file: %v", err)
	}
}

// TestKeyLog_KeptOutOfADirectoryAndBelowIt refuses a key log in a kept-out
// directory, below it, and through links into it or into one of its
// subdirectories, from the check and from the creation alike, naming what
// it is kept out of; a name outside is taken, and a kept-out directory that
// does not exist keeps nothing out.
func TestKeyLog_KeptOutOfADirectoryAndBelowIt(t *testing.T) {
	if refusedHere(t) {
		return
	}
	base := t.TempDir()
	project, outside := filepath.Join(base, "project"), filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(project, "captures"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"into": project, "into-captures": filepath.Join(project, "captures"), "up": base} {
		if err := os.Symlink(target, filepath.Join(outside, name)); err != nil {
			t.Fatal(err)
		}
	}
	refused := []string{
		filepath.Join(project, "run.keylog"),
		filepath.Join(project, "captures", "run.keylog"),
		filepath.Join(outside, "into", "run.keylog"),
		filepath.Join(outside, "into-captures", "run.keylog"),
		filepath.Join(outside, "up", "project", "run.keylog"),
	}
	keep := []devicetls.KeepOut{keepProject(project), {Dir: filepath.Join(base, "gone"), What: "a directory that is not there", Why: "for nothing"}}
	for _, p := range refused {
		if err := devicetls.CheckKeyLogPath(p, keep...); err == nil || !strings.Contains(err.Error(), "inside the project "+project) {
			t.Errorf("CheckKeyLogPath(%s) = %v, want it refused as inside the project", p, err)
		}
		if keyLog, err := devicetls.CreateKeyLog(p, keep...); err == nil {
			_ = keyLog.Close()
			t.Errorf("CreateKeyLog(%s) made a key log inside the project", p)
		}
	}
	absent(t, filepath.Join(project, "run.keylog"))
	absent(t, filepath.Join(project, "captures", "run.keylog"))
	ok := filepath.Join(outside, "run.keylog")
	keyLog, err := devicetls.CreateKeyLog(ok, keep...)
	if err != nil {
		t.Fatalf("a key log outside the project: %v", err)
	}
	_ = keyLog.Close()
}

// TestKeyLog_KeptOutOfACaseVariantName refuses a key log named through the
// project's name in another case, on a filesystem that ignores case (macOS's
// APFS by default): the names differ and the directory is the same, so only
// an identity comparison sees it. A case-sensitive filesystem has no such
// second name, and the test says so.
func TestKeyLog_KeptOutOfACaseVariantName(t *testing.T) {
	if refusedHere(t) {
		return
	}
	base := t.TempDir()
	project := filepath.Join(base, "Project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(base, "project")
	pi, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	if vi, err := os.Stat(variant); err != nil || !os.SameFile(pi, vi) {
		t.Skipf("needs a case-insensitive filesystem: %s is case-sensitive, so %s names nothing", base, variant)
	}
	p := filepath.Join(variant, "run.keylog")
	if err := devicetls.CheckKeyLogPath(p, keepProject(project)); err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Errorf("CheckKeyLogPath(%s) = %v, want it refused as inside %s", p, err, project)
	}
	if keyLog, err := devicetls.CreateKeyLog(p, keepProject(project)); err == nil {
		_ = keyLog.Close()
		t.Errorf("CreateKeyLog(%s) made a key log inside the project", p)
	}
	absent(t, filepath.Join(project, "run.keylog"))
}

// The variables naming the bind mount TestKeyLog_KeptOutThroughABindMount
// makes, set only for its own run of itself inside the namespace.
const (
	bindProjectEnv = "DEVICETLS_TEST_BIND_PROJECT"
	bindMirrorEnv  = "DEVICETLS_TEST_BIND_MIRROR"
)

// TestKeyLog_KeptOutThroughABindMount mounts the project a second time,
// elsewhere, inside a user and mount namespace (unshare -Urm, which needs no
// root), and runs itself there: a key log named through the second mount is
// a name no link resolves to the project's, in the project's own directory,
// and it is refused. A machine that allows no such namespace says so.
func TestKeyLog_KeptOutThroughABindMount(t *testing.T) {
	if project := os.Getenv(bindProjectEnv); project != "" {
		bindMountChild(t, project, os.Getenv(bindMirrorEnv))
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux user namespaces: a bind mount without root is made in one")
	}
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("needs unshare(1): it makes the user and mount namespace")
	}
	if out, err := exec.Command(unshare, "-Urm", "true").CombinedOutput(); err != nil { // #nosec G204 -- the system's unshare
		t.Skipf("needs unprivileged user and mount namespaces, which this machine refuses: %v: %s", err, out)
	}
	project, mirror := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(unshare, "-Urm", "sh", "-c", `mount --bind "$1" "$2" && exec "$3" -test.run '^TestKeyLog_KeptOutThroughABindMount$' -test.count=1 -test.v`, // #nosec G204 -- this test's own binary
		"sh", project, mirror, os.Args[0])
	cmd.Env = append(os.Environ(), bindProjectEnv+"="+project, bindMirrorEnv+"="+mirror)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestKeyLog_KeptOutThroughABindMount") {
		t.Fatalf("the run inside the namespace: %v\n%s", err, out)
	}
	absent(t, filepath.Join(project, "run.keylog"))
}

// bindMountChild is TestKeyLog_KeptOutThroughABindMount inside the
// namespace, where mirror is the project mounted a second time.
func bindMountChild(t *testing.T, project, mirror string) {
	pi, err := os.Stat(project)
	if err != nil {
		t.Fatal(err)
	}
	mi, err := os.Stat(mirror)
	if err != nil || !os.SameFile(pi, mi) {
		t.Fatalf("%s is not the project mounted again (%v)", mirror, err)
	}
	// No link leads from the second name to the first: only the directory's
	// identity says they are one.
	if resolved, err := filepath.EvalSymlinks(mirror); err != nil || resolved == project {
		t.Fatalf("%s resolves to %q (%v), want a name of its own", mirror, resolved, err)
	}
	for _, p := range []string{filepath.Join(mirror, "run.keylog"), filepath.Join(mirror, "captures", "run.keylog")} {
		if err := devicetls.CheckKeyLogPath(p, keepProject(project)); err == nil || !strings.Contains(err.Error(), "inside the project") {
			t.Errorf("CheckKeyLogPath(%s) = %v, want it refused as inside the project", p, err)
		}
		if keyLog, err := devicetls.CreateKeyLog(p, keepProject(project)); err == nil {
			_ = keyLog.Close()
			t.Errorf("CreateKeyLog(%s) made a key log inside the project", p)
		}
	}
	absent(t, filepath.Join(project, "run.keylog"))
}

// TestCreateKeyLog_CreatesInTheDirectoryItChecked moves the checked
// directory away and puts a link into the project in its place between the
// check and the file's creation: the file is made in the directory checked,
// held open, and nothing is made in the project.
func TestCreateKeyLog_CreatesInTheDirectoryItChecked(t *testing.T) {
	if refusedHere(t) {
		return
	}
	project, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(outside, "captures")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	devicetls.SetAfterKeyLogCheck(t, func() {
		if err := os.Rename(dir, dir+".checked"); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(project, dir); err != nil {
			t.Error(err)
		}
	})
	keyLog, err := devicetls.CreateKeyLog(filepath.Join(dir, "run.keylog"), keepProject(project))
	if err != nil {
		t.Fatal(err)
	}
	_ = keyLog.Close()
	absent(t, filepath.Join(project, "run.keylog"))
	if _, err := os.Stat(filepath.Join(dir+".checked", "run.keylog")); err != nil {
		t.Errorf("the key log is not in the directory checked: %v", err)
	}
}

// TestCreateKeyLog_ChecksAgainAPathChangedSinceItsCheck passes the check,
// then swaps the directory for a link into the project before the creation,
// as the command line's check and the run's start are apart: the creation
// checks again and refuses.
func TestCreateKeyLog_ChecksAgainAPathChangedSinceItsCheck(t *testing.T) {
	if refusedHere(t) {
		return
	}
	project, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(outside, "captures")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "run.keylog")
	if err := devicetls.CheckKeyLogPath(p, keepProject(project)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+".before"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, dir); err != nil {
		t.Fatal(err)
	}
	if keyLog, err := devicetls.CreateKeyLog(p, keepProject(project)); err == nil {
		_ = keyLog.Close()
		t.Error("a path swapped for a link into the project after its check made a key log")
	} else if !strings.Contains(err.Error(), "inside the project") {
		t.Errorf("CreateKeyLog = %v, want it refused as inside the project", err)
	}
	absent(t, filepath.Join(project, "run.keylog"))
}
