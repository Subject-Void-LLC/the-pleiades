// Package file_test: tests of file.directory's check against the real run it
// predicts.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These prove file.directory's check against the same real SSH server and
// real /bin/sh as the rest of this package's tests. Every claim that a
// check changed nothing is made against the filesystem with os.Lstat, and
// every prediction is compared against what the real Directory run
// actually leaves from the same starting state, which is the control that
// makes a prediction worth anything.

// dirCheckStart builds a starting state under a fresh temp directory:
// nothing at all when mode is zero, otherwise a directory carrying exactly
// mode. It returns the path.
func dirCheckStart(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target")
	if mode == 0 {
		return path
	}
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	// Mkdir is filtered by the umask; Chmod is not, so the fixture
	// carries exactly the mode asked for.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// dirModeOnDisk is path's permission bits, or every bit set when nothing
// is there, a value no real mode can take.
func dirModeOnDisk(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ^os.FileMode(0)
	}
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestCheckDirectory_PredictsWhatARealRunDoes covers the three answers a
// check can give (create, change the mode, nothing to do), each against the
// real run as its control.
func TestCheckDirectory_PredictsWhatARealRunDoes(t *testing.T) {
	cases := []struct {
		name        string
		start       os.FileMode
		wantMode    string
		wantChanged bool
	}{
		{name: "absent, would be created", start: 0, wantMode: "0750", wantChanged: true},
		{name: "wrong mode, would be changed", start: 0o700, wantMode: "0755", wantChanged: true},
		{name: "already right, nothing to do", start: 0o755, wantMode: "0755", wantChanged: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := startDirServer(t)

			// The check.
			path := dirCheckStart(t, tc.start)
			params := dirParams(path)
			params["mode"] = tc.wantMode
			rc := newDirContext(server)
			checked, err := file.CheckDirectory(context.Background(), rc, newDirDevice(server), params)
			if err != nil {
				t.Fatalf("CheckDirectory: %v", err)
			}
			if checked.Changed != tc.wantChanged {
				t.Errorf("check Changed = %v, want %v", checked.Changed, tc.wantChanged)
			}
			if got := dirModeOnDisk(t, path); got != dirModeOrAbsent(tc.start) {
				t.Fatalf("the check changed the path: mode on disk is now %v, it started as %v", got, dirModeOrAbsent(tc.start))
			}
			if _, recorded := rc.stats[sdk.StatInverse]; recorded {
				t.Error("a check recorded an inverse, but it changed nothing there is to undo")
			}
			if got := rc.stats["path"]; got != path {
				t.Errorf("stat path = %v, want %q", got, path)
			}
			predicted := dirDiffHalf(t, rc, "after")

			// The control: the real run, from an identical starting state.
			realPath := dirCheckStart(t, tc.start)
			realParams := dirParams(realPath)
			realParams["mode"] = tc.wantMode
			realRC := newDirContext(server)
			real, err := file.Directory(context.Background(), realRC, newDirDevice(server), realParams)
			if err != nil {
				t.Fatalf("Directory: %v", err)
			}
			if real.Changed != checked.Changed {
				t.Errorf("the check predicted Changed = %v, the real run reported %v", checked.Changed, real.Changed)
			}
			actual := dirDiffHalf(t, realRC, "after")

			// Every key the prediction states must match what the real run
			// left. A key it leaves out is one only the device decides, and
			// the create case must leave out owner and group, since this
			// task names neither.
			for key, want := range predicted {
				if actual[key] != want {
					t.Errorf("the check predicted after.%s = %v, the real run left %v", key, want, actual[key])
				}
			}
			for _, key := range []string{"exists", "kind", "mode"} {
				if _, ok := predicted[key]; !ok {
					t.Errorf("the prediction left out %q, which this task decides", key)
				}
			}
			if tc.start == 0 {
				for _, key := range []string{"owner", "group", "size", "mtime"} {
					if _, ok := predicted[key]; ok {
						t.Errorf("the prediction for a new directory states %q, which only the device decides", key)
					}
				}
			}
		})
	}
}

// dirModeOrAbsent is dirModeOnDisk's answer for a starting state built by
// dirCheckStart.
func dirModeOrAbsent(start os.FileMode) os.FileMode {
	if start == 0 {
		return ^os.FileMode(0)
	}
	return start
}

// TestCheckDirectory_RefusesWhatARealRunRefuses proves a check of a path
// holding a regular file fails exactly as the real run does, and leaves
// the file alone.
func TestCheckDirectory_RefusesWhatARealRunRefuses(t *testing.T) {
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	_, checkErr := file.CheckDirectory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if checkErr == nil {
		t.Fatal("the check predicted success where a real run refuses")
	}
	_, realErr := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if realErr == nil || checkErr.Error() != realErr.Error() {
		t.Errorf("check refused with %v, the real run with %v: the same refusal must read the same", checkErr, realErr)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep me" { // #nosec G304 -- a path under this test's t.TempDir
		t.Errorf("the file is gone or changed after the refusal: %q, %v", data, err)
	}
}

// TestCheckDirectory_IsDeclaredAndReachable proves the registration the
// engine dispatches through: check support declared, and MethodFor handing
// back the check rather than Invoke.
func TestCheckDirectory_IsDeclaredAndReachable(t *testing.T) {
	d, ok := collection.Lookup("file.directory")
	if !ok {
		t.Fatal("file.directory is not registered")
	}
	if !d.Manifest.SupportsCheck {
		t.Fatal("file.directory does not declare check support")
	}
	method, err := d.MethodFor(collection.ModeCheck)
	if err != nil {
		t.Fatalf("MethodFor(check): %v", err)
	}

	// Called through the registry, against a path that does not exist:
	// Invoke would create it, the check must not.
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "via-registry")
	result, err := method(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if err != nil {
		t.Fatalf("check through MethodFor: %v", err)
	}
	if !result.Changed {
		t.Error("the check did not predict creating a missing directory")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("the method MethodFor returned for check mode created %s", path)
	}
}
