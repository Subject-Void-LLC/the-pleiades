//go:build unix

// Package loader: tests of Load.
package loader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// TestLoad_RegistersEveryMethod proves the happy path end to end: two
// methods from one program, registered with the proxy as Invoke, a Check
// exactly where the manifest declares check support, and a Set that
// knows what it loaded and pinned.
func TestLoad_RegistersEveryMethod(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	dir := programDir(t)
	out := describeJSON(t, 0,
		external.DescribedMethod{Name: "loadertest.happy.checked", Manifest: implemented(true)},
		external.DescribedMethod{Name: "loadertest.happy.unchecked", Manifest: implemented(false)},
	)
	path := writeProgram(t, dir, "happy", script(out, "exit 0"))

	set, err := Load(t.Context(), dir, testOptions())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checked, ok := collection.Lookup("loadertest.happy.checked")
	if !ok || checked.Invoke == nil || checked.Check == nil || !checked.Manifest.SupportsCheck {
		t.Errorf("the check-capable method registered as %+v, want an Invoke and a Check", checked)
	}
	unchecked, ok := collection.Lookup("loadertest.happy.unchecked")
	if !ok || unchecked.Invoke == nil || unchecked.Check != nil {
		t.Errorf("the method without check support registered as %+v, want an Invoke and no Check", unchecked)
	}

	if !set.Owns("loadertest.happy.checked") || !set.Owns("loadertest.happy.unchecked") {
		t.Error("the Set does not own the methods it registered")
	}
	if set.Owns("file.directory") {
		t.Error("the Set claims a built-in method it never loaded")
	}

	programs := set.Programs()
	if len(programs) != 1 {
		t.Fatalf("Programs() = %+v, want exactly one", programs)
	}
	sum := sha256.Sum256([]byte(readFile(t, path)))
	if want := "sha256:" + hex.EncodeToString(sum[:]); programs[0].Digest != want {
		t.Errorf("pinned digest %s, want the file's own %s", programs[0].Digest, want)
	}
	if programs[0].Path != path {
		t.Errorf("program path %s, want %s", programs[0].Path, path)
	}
	if got := strings.Join(programs[0].Methods, ","); got != "loadertest.happy.checked,loadertest.happy.unchecked" {
		t.Errorf("program methods %s, want both, sorted", got)
	}
	if w := set.Warnings(); len(w) != 0 {
		t.Errorf("an unconstrained load warned: %v", w)
	}

	// The accessors hand out copies, so a caller cannot rewrite the Set.
	programs[0].Methods[0] = "tampered"
	if set.Programs()[0].Methods[0] == "tampered" {
		t.Error("Programs() returned the Set's own slice")
	}
}

// TestLoad_EmptyAndDotFiles proves an empty directory is not an error and
// that a hidden file, which is often an editor's swap file, is ignored
// rather than refused as a non-program.
func TestLoad_EmptyAndDotFiles(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	dir := programDir(t)
	if err := os.WriteFile(filepath.Join(dir, ".note.swp"), []byte("not a program"), 0o600); err != nil {
		t.Fatalf("writing a dot file: %v", err)
	}
	set, err := Load(t.Context(), dir, testOptions())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Programs()) != 0 {
		t.Errorf("an empty directory loaded %+v", set.Programs())
	}
}

// TestLoad_Refusals is every reason Load refuses a directory. Each case
// asserts the refusal names its reason and that NOTHING was registered,
// including the well-behaved program every case also puts beside the bad
// one: a directory is loaded whole or not at all.
func TestLoad_Refusals(t *testing.T) {
	requireConfinement(t)
	good := "loadertest.bystander.run"

	cases := []struct {
		name    string
		opts    func(*Options)
		arrange func(t *testing.T, dir string)
		want    string
	}{
		{
			name: "a world-writable directory",
			arrange: func(t *testing.T, dir string) {
				if err := os.Chmod(dir, 0o777); err != nil { // #nosec G302 -- the fixture under test
					t.Fatal(err)
				}
			},
			want: "group- or world-writable",
		},
		{
			name: "a group-writable program",
			arrange: func(t *testing.T, dir string) {
				p := oneMethodProgram(t, dir, "loadertest.bad.run", false, "exit 0")
				if err := os.Chmod(p, 0o770); err != nil { // #nosec G302 -- the fixture under test
					t.Fatal(err)
				}
			},
			want: "group- or world-writable",
		},
		{
			name: "a file that is not executable",
			arrange: func(t *testing.T, dir string) {
				p := oneMethodProgram(t, dir, "loadertest.bad.run", false, "exit 0")
				if err := os.Chmod(p, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "not executable",
		},
		{
			name: "a setuid program",
			arrange: func(t *testing.T, dir string) {
				p := oneMethodProgram(t, dir, "loadertest.bad.run", false, "exit 0")
				if err := os.Chmod(p, 0o700|os.ModeSetuid); err != nil {
					t.Fatal(err)
				}
			},
			want: "setuid or setgid",
		},
		{
			name: "a symlink",
			arrange: func(t *testing.T, dir string) {
				target := oneMethodProgram(t, t.TempDir(), "loadertest.bad.run", false, "exit 0")
				if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
					t.Fatal(err)
				}
			},
			want: "is a symlink",
		},
		{
			name: "a subdirectory",
			arrange: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a regular file",
		},
		{
			name: "a protocol this build does not speak",
			arrange: func(t *testing.T, dir string) {
				out := describeJSON(t, 2, external.DescribedMethod{Name: "loadertest.bad.run", Manifest: implemented(false)})
				writeProgram(t, dir, "future", script(out, "exit 0"))
			},
			want: "protocol 2",
		},
		{
			name: "a declared method",
			arrange: func(t *testing.T, dir string) {
				m := implemented(false)
				m.Status = collection.StatusDeclared
				writeProgram(t, dir, "stub", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.bad.run", Manifest: m}), "exit 0"))
			},
			want: "may only provide implemented methods",
		},
		{
			name: "an unknown capability",
			arrange: func(t *testing.T, dir string) {
				m := implemented(false)
				m.RequiredCapabilities = []capability.Name{"TeleportCapable"}
				writeProgram(t, dir, "caps", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.bad.run", Manifest: m}), "exit 0"))
			},
			want: "does not define",
		},
		{
			// A built-in's name sits in a namespace that is reserved, so a
			// program claiming it is refused for the namespace first
			// (namespace_test.go). The name check still matters for a name
			// another external program already holds, as here.
			name: "a name another external program already holds",
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "hijack", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.taken.run", Manifest: implemented(false)}), "exit 0"))
				if err := collection.Register(collection.Descriptor{
					Name:     "loadertest.taken.run",
					Manifest: collection.Manifest{Status: collection.StatusDeclared},
					Provider: &collection.Provider{Program: "/elsewhere/other", Digest: "sha256:00"},
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: "already registered",
		},
		{
			name: "a built-in's own name",
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "hijack", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertestbuiltin.taken.run", Manifest: implemented(false)}), "exit 0"))
				if err := collection.Register(collection.Descriptor{Name: "loadertestbuiltin.taken.run", Manifest: collection.Manifest{Status: collection.StatusDeclared}}); err != nil {
					t.Fatal(err)
				}
			},
			want: "belongs to The Pleiades itself",
		},
		{
			name: "a name two programs claim",
			arrange: func(t *testing.T, dir string) {
				oneMethodProgram(t, dir, "loadertest.twice.run", false, "exit 0")
				writeProgram(t, dir, "second", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.twice.run", Manifest: implemented(false)}), "exit 0"))
			},
			want: "claimed by both",
		},
		{
			name: "a name that is not lowercase dotted",
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "shout", script(describeJSON(t, 0, external.DescribedMethod{Name: "Loadertest.Bad", Manifest: implemented(false)}), "exit 0"))
			},
			want: "not a lowercase, dot-separated name",
		},
		{
			name: "describe exiting non-zero",
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "broken", "#!/bin/sh\necho 'cannot start' >&2\nexit 7\n")
			},
			want: "describe failed",
		},
		{
			name: "describe printing garbage",
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "garbage", script("this is not json", "exit 0"))
			},
			want: "invalid JSON",
		},
		{
			name: "describe printing data after its JSON",
			arrange: func(t *testing.T, dir string) {
				out := describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.bad.run", Manifest: implemented(false)})
				writeProgram(t, dir, "trailing", script(out+"\n{}", "exit 0"))
			},
			want: "data after its JSON",
		},
		{
			name: "describe listing no methods",
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "empty", script(describeJSON(t, 0), "exit 0"))
			},
			want: "lists no methods",
		},
		{
			name: "describe never finishing",
			opts: func(o *Options) { o.DescribeTimeout = 300 * time.Millisecond },
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "slow", "#!/bin/sh\nexec sleep 30\n")
			},
			want: "did not finish within",
		},
		{
			name: "describe printing more than the cap",
			opts: func(o *Options) { o.MaxResponse = 64 },
			arrange: func(t *testing.T, dir string) {
				writeProgram(t, dir, "chatty", "#!/bin/sh\nhead -c 100000 /dev/zero\n")
			},
			want: "more than 64 bytes",
		},
		{
			name: "an engine too old for the method",
			opts: func(o *Options) { o.EngineVersion = "1.4.0" },
			arrange: func(t *testing.T, dir string) {
				m := implemented(false)
				m.EngineVersion = ">=2.0.0"
				writeProgram(t, dir, "newer", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.bad.run", Manifest: m}), "exit 0"))
			},
			want: "requires engine >=2.0.0, and this build is 1.4.0",
		},
		{
			name: "a constraint in a grammar this build does not read",
			arrange: func(t *testing.T, dir string) {
				m := implemented(false)
				m.EngineVersion = "~> 1.2"
				writeProgram(t, dir, "tilde", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.bad.run", Manifest: m}), "exit 0"))
			},
			want: "unsupported engine version constraint",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			dir := programDir(t)
			oneMethodProgram(t, dir, good, false, "exit 0")
			tc.arrange(t, dir)
			opts := testOptions()
			if tc.opts != nil {
				tc.opts(&opts)
			}

			started := time.Now()
			set, err := Load(t.Context(), dir, opts)
			if err == nil {
				t.Fatalf("Load accepted the directory: %+v", set.Programs())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load refused with %q, want it to say %q", err, tc.want)
			}
			if time.Since(started) > 4*time.Second {
				t.Errorf("the refusal took %s; every bound in testOptions is far shorter", time.Since(started))
			}
			for _, name := range []string{good, "loadertest.bad.run", "loadertest.twice.run"} {
				if d, ok := collection.Lookup(name); ok && d.Invoke != nil {
					t.Errorf("%s was registered although the directory was refused", name)
				}
			}
		})
	}
}

// TestLoad_UnreleasedBuildWarnsInsteadOfChecking proves the documented
// choice for an engine constraint on a build with no release version: the
// method loads, and the Set says the check did not run. A release build
// that satisfies the constraint loads it with no warning.
func TestLoad_UnreleasedBuildWarnsInsteadOfChecking(t *testing.T) {
	requireConfinement(t)
	for _, tc := range []struct {
		engine   string
		wantWarn bool
	}{
		{engine: "dev", wantWarn: true},
		{engine: "", wantWarn: true},
		{engine: "v2.1.0", wantWarn: false},
		{engine: "2.0.0-rc1", wantWarn: false},
	} {
		t.Run("engine "+tc.engine, func(t *testing.T) {
			t.Cleanup(collection.SnapshotForTest())
			dir := programDir(t)
			m := implemented(false)
			m.EngineVersion = ">=2.0.0"
			writeProgram(t, dir, "constrained", script(describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.constrained.run", Manifest: m}), "exit 0"))

			opts := testOptions()
			opts.EngineVersion = tc.engine
			set, err := Load(t.Context(), dir, opts)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			warnings := set.Warnings()
			if tc.wantWarn {
				if len(warnings) != 1 || !strings.Contains(warnings[0], "loadertest.constrained.run") || !strings.Contains(warnings[0], ">=2.0.0") {
					t.Errorf("warnings = %v, want one naming the method and its constraint", warnings)
				}
			} else if len(warnings) != 0 {
				t.Errorf("a release build that satisfies the constraint warned: %v", warnings)
			}
		})
	}
}

// TestLoad_SecondLoadIsRefused proves loading the same directory twice
// never replaces what the first load registered.
func TestLoad_SecondLoadIsRefused(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	dir := programDir(t)
	oneMethodProgram(t, dir, "loadertest.again.run", false, "exit 0")
	if _, err := Load(t.Context(), dir, testOptions()); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if _, err := Load(t.Context(), dir, testOptions()); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("second Load = %v, want a refusal naming the taken name", err)
	}
}

// TestLoad_CanceledContext proves Load stops when its caller does.
func TestLoad_CanceledContext(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	dir := programDir(t)
	writeProgram(t, dir, "slow", "#!/bin/sh\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := Load(ctx, dir, testOptions()); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Load with a canceled context = %v, want a cancellation", err)
	}
}

// TestLoad_MissingDirectory proves a directory that is not there is an
// error naming it, not an empty load.
func TestLoad_MissingDirectory(t *testing.T) {
	// Load refuses for want of Landlock before it looks at the directory,
	// so where there is none the refusals below would be the wrong ones.
	requireConfinement(t)
	for _, dir := range []string{"", filepath.Join(t.TempDir(), "absent")} {
		if _, err := Load(t.Context(), dir, testOptions()); err == nil {
			t.Errorf("Load(%q) = nil error", dir)
		}
	}
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(t.Context(), file, testOptions()); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("Load of a file = %v, want a refusal saying it is not a directory", err)
	}
}

// TestSet_NilIsSafe proves a nil Set, what a caller with no directory
// configured holds, answers every question without panicking.
func TestSet_NilIsSafe(t *testing.T) {
	var s *Set
	if s.Owns("anything") || s.Programs() != nil || s.Warnings() != nil {
		t.Error("a nil Set claimed to hold something")
	}
}
