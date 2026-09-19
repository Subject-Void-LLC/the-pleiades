//go:build unix

// Package loader: tests against a real program built with pkg/external.
package loader

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// The well-behaved half of the contract, proven against a real program
// built with pkg/external rather than a script written to match the
// loader: testdata/extprog, which is nothing but external.Main over two
// methods. It is built once per test binary, since a Go build is the
// slowest thing in this package.

var (
	extprogOnce sync.Once
	extprogPath string
	extprogErr  error
)

// buildExtprog builds testdata/extprog once into a directory of its own
// and returns the binary's path. The directory is removed after every
// test has run (afterAll, main_test.go).
func buildExtprog(t *testing.T) string {
	t.Helper()
	extprogOnce.Do(func() {
		dir, err := os.MkdirTemp("", "loader-extprog")
		if err != nil {
			extprogErr = err
			return
		}
		afterAll = append(afterAll, func() { _ = os.RemoveAll(dir) })
		extprogPath = filepath.Join(dir, "extprog")
		build := exec.Command("go", "build", "-o", extprogPath, "./testdata/extprog")
		if out, err := build.CombinedOutput(); err != nil {
			extprogErr = err
			extprogPath = string(out)
		}
	})
	if extprogErr != nil {
		t.Fatalf("building testdata/extprog: %v\n%s", extprogErr, extprogPath)
	}
	return extprogPath
}

// installExtprog copies the built program into a fresh program directory,
// the way an operator installs one, and returns the directory.
func installExtprog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(buildExtprog(t))
	if err != nil {
		t.Fatalf("reading the built program: %v", err)
	}
	dir := programDir(t)
	writeProgram(t, dir, "extprog", string(data))
	return dir
}

// TestRealProgram_BothModesAndTheCredential proves the loader and the real
// SDK agree about the whole exchange: describe, both modes reaching the
// right function, the device crossing, and the credential usable inside
// the program without ever being echoed back.
func TestRealProgram_BothModesAndTheCredential(t *testing.T) {
	dir := installExtprog(t)
	d := loadOne(t, dir, "loadertest.goprog.run", testOptions())
	if d.Check == nil {
		t.Fatal("the program declares check support for loadertest.goprog.run, so a Check proxy must be registered")
	}
	if fail, ok := collection.Lookup("loadertest.goprog.fail"); !ok || fail.Check != nil {
		t.Fatalf("loadertest.goprog.fail registered as %+v, want an Invoke and no Check", fail)
	}

	sum := sha256.Sum256([]byte(testPassword))
	params := map[string]any{"message": "hello", "password_sha256": hex.EncodeToString(sum[:])}

	for _, tc := range []struct {
		mode        collection.Mode
		wantRan     string
		wantChanged bool
	}{
		{mode: collection.ModeExecute, wantRan: "invoke", wantChanged: true},
		{mode: collection.ModeCheck, wantRan: "check", wantChanged: false},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			method, err := d.MethodFor(tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			rc := newRecordingContext()
			result, err := method(t.Context(), rc, newSSHDevice(), params)
			if err != nil {
				t.Fatalf("%s: %v", tc.mode, err)
			}
			if result.Changed != tc.wantChanged {
				t.Errorf("Changed = %v, want %v", result.Changed, tc.wantChanged)
			}
			if rc.stats["ran"] != tc.wantRan {
				t.Errorf("the program ran %v, want %s: the mode must pick the function", rc.stats["ran"], tc.wantRan)
			}
			if rc.stats["echoed"] != "hello" || rc.stats["device_name"] != "web1" {
				t.Errorf("stats = %v, want the param and the device to have crossed", rc.stats)
			}
			if rc.stats["password_matches"] != true {
				t.Error("the credential was not usable inside the program")
			}
		})
	}
}

// TestRealProgram_AMethodsOwnErrorIsMasked proves a real method that puts
// its credential into its own error gets it masked on the way out.
func TestRealProgram_AMethodsOwnErrorIsMasked(t *testing.T) {
	dir := installExtprog(t)
	d := loadOne(t, dir, "loadertest.goprog.fail", testOptions())

	_, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
	if err == nil {
		t.Fatal("expected the method's own failure")
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatalf("the credential reached the error: %v", err)
	}
	if !strings.Contains(err.Error(), "login rejected for password") {
		t.Errorf("error %q does not carry what the method said", err)
	}
}
