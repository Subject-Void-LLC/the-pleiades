//go:build !windows

// Package remotefile_test: tests that each prediction matches what the real
// operation does.
package remotefile_test

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// These pin the promise predict.go makes: a check's answer and a real
// run's answer are the same answer. Every case below runs the real Apply
// against a real file through a real shell and compares what it did with
// what Differs and PredictApply said it would do, so a comparison that
// drifted from Apply's own would fail here rather than in a dry run that
// quietly disagrees with the run it previews.

// predictOtherGroup names a group this process may hand path to, other
// than the one it has, or "" when there is none. A real chgrp needs a
// real second group, and which ones exist depends on who runs the tests.
func predictOtherGroup(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	current := int(sys.Gid)

	candidates, err := os.Getgroups()
	if err != nil {
		t.Fatalf("reading this process's groups: %v", err)
	}
	if os.Geteuid() == 0 {
		// root may chgrp to anything, so widen the search past its own
		// supplementary set, which is usually just gid 0.
		for gid := 0; gid < 100; gid++ {
			candidates = append(candidates, gid)
		}
	}
	for _, gid := range candidates {
		if gid == current {
			continue
		}
		group, err := user.LookupGroupId(strconv.Itoa(gid))
		if err != nil {
			continue
		}
		// An all-digit name would be read by chgrp as an id, which is a
		// different question from the one this file asks.
		if _, numeric := strconv.Atoi(group.Name); numeric == nil {
			continue
		}
		return group.Name
	}
	return ""
}

// predictFixture creates a path of the given kind carrying exactly mode,
// defeating the umask, and returns it.
func predictFixture(t *testing.T, kind remotefile.Kind, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "target")
	switch kind {
	case remotefile.KindDirectory:
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
	default:
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
	}
	// os.Chmod takes Go's own special-bit flags rather than the raw octal
	// ones, so the raw bits go through syscall.Chmod instead.
	if err := syscall.Chmod(path, uint32(mode)); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// TestPrediction_MatchesWhatApplyDoes is the promise in one table: for
// each starting state and request, Differs must equal what Apply
// reported, and every key PredictApply claims to know must equal what
// the device holds after Apply ran. A key PredictApply leaves out is
// named, so a prediction cannot pass by knowing nothing.
func TestPrediction_MatchesWhatApplyDoes(t *testing.T) {
	const otherGroup = "\x00other" // replaced by a real second group, or the case skips

	tests := []struct {
		name        string
		kind        remotefile.Kind
		mode        os.FileMode
		want        remotefile.Attributes
		wantChanged bool
		wantMode    string   // what the device must carry afterward
		unknown     []string // keys the prediction must leave out

		// linuxOnly marks a case whose expected mode is the Linux kernel's
		// own clearing rule, which other kernels answer differently.
		linuxOnly bool
	}{
		{name: "a mode that differs", kind: remotefile.KindFile, mode: 0o644,
			want: remotefile.Attributes{Mode: "0600"}, wantChanged: true, wantMode: "0600"},
		{name: "the same mode written unpadded", kind: remotefile.KindFile, mode: 0o644,
			want: remotefile.Attributes{Mode: "644"}, wantChanged: false, wantMode: "0644"},
		{name: "a request naming nothing", kind: remotefile.KindFile, mode: 0o640,
			want: remotefile.Attributes{}, wantChanged: false, wantMode: "0640"},
		{
			// GNU chmod keeps a directory's setgid bit when handed four
			// digits, so without the fifth this never converged.
			name: "a setgid directory asked for a plain mode", kind: remotefile.KindDirectory, mode: 0o2755,
			want: remotefile.Attributes{Mode: "0755"}, wantChanged: true, wantMode: "0755",
		},
		{name: "a setgid directory asked for the mode it has", kind: remotefile.KindDirectory, mode: 0o2755,
			want: remotefile.Attributes{Mode: "2755"}, wantChanged: false, wantMode: "2755"},
		{
			// The kernel clears setgid on the chgrp, and the mode then
			// compares equal to what was found. The chmod has to be sent
			// anyway, or the device is left at 0755.
			name: "a setgid file whose group changes and whose mode is asked to stay", kind: remotefile.KindFile, mode: 0o2755,
			want: remotefile.Attributes{Mode: "2755", Group: otherGroup}, wantChanged: true, wantMode: "2755", linuxOnly: true,
		},
		{
			// No mode was asked for, so what the kernel does to the special
			// bit is the device's business, and the prediction says so by
			// leaving the mode out. wantMode is Linux's answer, which is
			// the kernel these tests run on.
			name: "a setgid file whose group changes with no mode named", kind: remotefile.KindFile, mode: 0o2755,
			want: remotefile.Attributes{Group: otherGroup}, wantChanged: true, wantMode: "0755", unknown: []string{"mode"},
			linuxOnly: true,
		},
		{
			// Linux exempts a directory from the clearing (its chown kills
			// the bits of a regular file only), so its mode is predictable
			// even with no mode named. Whether the bit survives on a
			// directory is each kernel's own choice, which POSIX leaves
			// implementation-defined, and this is Linux's answer.
			name: "a setgid directory whose group changes with no mode named", kind: remotefile.KindDirectory, mode: 0o2755,
			want: remotefile.Attributes{Group: otherGroup}, wantChanged: true, wantMode: "2755", linuxOnly: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.linuxOnly && runtime.GOOS != "linux" {
				t.Skip("the expected mode is the Linux kernel's clearing rule")
			}
			conn := connect(t)
			path := predictFixture(t, tt.kind, tt.mode)

			want := tt.want
			if want.Group == otherGroup {
				if want.Group = predictOtherGroup(t, path); want.Group == "" {
					t.Skip("this process belongs to no second group, so a real chgrp cannot be run here")
				}
			}

			before := statOf(t, path)
			predictedChange := remotefile.Differs(want, before)
			predicted := remotefile.PredictApply(want, before).Map()

			changed, err := remotefile.Apply(context.Background(), conn, path, want, before)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			after := statOf(t, path)

			if changed != tt.wantChanged {
				t.Errorf("Apply changed = %v, want %v", changed, tt.wantChanged)
			}
			if predictedChange != changed {
				t.Errorf("Differs = %v but Apply changed = %v: a check and a real run disagree", predictedChange, changed)
			}
			if after.Mode != tt.wantMode {
				t.Errorf("mode on the device = %s, want %s", after.Mode, tt.wantMode)
			}

			actual := after.Map()
			for _, key := range tt.unknown {
				if _, claimed := predicted[key]; claimed {
					t.Errorf("the prediction claims %s = %v, which only the device can decide here", key, predicted[key])
				}
				delete(actual, key)
			}
			if !reflect.DeepEqual(predicted, actual) {
				t.Errorf("predicted %v, but Apply left %v", predicted, actual)
			}
		})
	}
}

// TestPredictCreate_LeavesOutWhatTheDeviceDecides proves a prediction
// for a path that does not exist yet names only what the task named. A
// new directory's umask mode, its owner, its size and its mtime are all
// the device's to choose, and a guessed value in a diff reads as a fact.
func TestPredictCreate_LeavesOutWhatTheDeviceDecides(t *testing.T) {
	tests := []struct {
		name string
		want remotefile.Attributes
		out  map[string]any
	}{
		{
			name: "nothing named",
			want: remotefile.Attributes{},
			out:  map[string]any{"exists": true, "kind": "directory"},
		},
		{
			name: "a mode, written unpadded",
			want: remotefile.Attributes{Mode: "750"},
			out:  map[string]any{"exists": true, "kind": "directory", "mode": "0750"},
		},
		{
			name: "everything",
			want: remotefile.Attributes{Mode: "0700", Owner: "app", Group: "staff"},
			out:  map[string]any{"exists": true, "kind": "directory", "mode": "0700", "owner": "app", "group": "staff"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remotefile.PredictCreate(remotefile.KindDirectory, tt.want).Map()
			if !reflect.DeepEqual(got, tt.out) {
				t.Errorf("PredictCreate = %v, want %v", got, tt.out)
			}
		})
	}
}

// TestDiffers covers the comparison on its own, without a device, for
// the shapes the table above cannot reach without root: an owner change.
func TestDiffers(t *testing.T) {
	found := remotefile.Info{Kind: remotefile.KindFile, Mode: "0644", Owner: "app", Group: "app"}
	tests := []struct {
		name string
		want remotefile.Attributes
		out  bool
	}{
		{name: "nothing named", want: remotefile.Attributes{}, out: false},
		{name: "the owner it has", want: remotefile.Attributes{Owner: "app"}, out: false},
		{name: "another owner", want: remotefile.Attributes{Owner: "root"}, out: true},
		{name: "the owner and group it has", want: remotefile.Attributes{Owner: "app", Group: "app"}, out: false},
		{name: "the same owner and another group", want: remotefile.Attributes{Owner: "app", Group: "root"}, out: true},
		{name: "the mode it has, padded differently", want: remotefile.Attributes{Mode: "644"}, out: false},
		{name: "another mode", want: remotefile.Attributes{Mode: "0600"}, out: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := remotefile.Differs(tt.want, found); got != tt.out {
				t.Errorf("Differs(%+v) = %v, want %v", tt.want, got, tt.out)
			}
		})
	}
}

// TestPredictApply_AnOwnerChangeIsPredicted covers the owner half of
// PredictApply without needing root to prove it against a device: the
// owner named is the owner predicted, and a file carrying no special bit
// keeps a predictable mode through the change.
func TestPredictApply_AnOwnerChangeIsPredicted(t *testing.T) {
	found := remotefile.Info{Kind: remotefile.KindFile, Mode: "0644", Owner: "app", Group: "app", Size: 3, Mtime: 7}
	got := remotefile.PredictApply(remotefile.Attributes{Owner: "root", Group: "wheel"}, found).Map()
	want := map[string]any{
		"exists": true, "kind": "file", "mode": "0644", "owner": "root", "group": "wheel",
		"size": int64(3), "mtime": int64(7),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PredictApply = %v, want %v", got, want)
	}
}

// TestDirectoryEmpty covers the probe a check uses in place of rmdir,
// against real directories: an empty one, one holding only a hidden
// entry (which rmdir would refuse, so the probe must count it), and one
// this account cannot list.
func TestDirectoryEmpty(t *testing.T) {
	conn := connect(t)
	root := t.TempDir()

	empty := filepath.Join(root, "empty")
	hidden := filepath.Join(root, "hidden")
	for _, dir := range []string{empty, hidden} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(hidden, ".dotfile"), []byte("x"), 0o600); err != nil {
		t.Fatalf("creating the hidden entry: %v", err)
	}

	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: empty, want: true},
		{path: hidden, want: false},
	} {
		got, err := remotefile.DirectoryEmpty(context.Background(), conn, tc.path)
		if err != nil {
			t.Fatalf("DirectoryEmpty(%s): %v", tc.path, err)
		}
		if got != tc.want {
			t.Errorf("DirectoryEmpty(%s) = %v, want %v", tc.path, got, tc.want)
		}
	}

	// A directory with no read permission cannot be listed, and "could not
	// look" must never read as "nothing there". root reads through any
	// mode, so the case only exists for everyone else.
	if os.Geteuid() != 0 {
		sealed := filepath.Join(root, "sealed")
		if err := os.Mkdir(sealed, 0o300); err != nil {
			t.Fatalf("creating %s: %v", sealed, err)
		}
		t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })
		if _, err := remotefile.DirectoryEmpty(context.Background(), conn, sealed); err == nil {
			t.Error("an unreadable directory was reported as a clean answer")
		}
	}
}

// TestDirectoryEmpty_ReportsATransportFailure covers the branch where the
// probe cannot run at all: a server that authenticates and then refuses
// every session.
func TestDirectoryEmpty_ReportsATransportFailure(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{SessionLimit: remoteexectest.Limit(0)})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	auth, err := remoteexec.AuthFrom(srv.Username, srv.Password, nil, "")
	if err != nil {
		t.Fatalf("building auth: %v", err)
	}
	conn, err := runner.Connect(context.Background(), nil, remoteexec.Target{Host: srv.Host, Port: srv.Port}, auth)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := remotefile.DirectoryEmpty(context.Background(), conn, t.TempDir()); err == nil {
		t.Fatal("a probe that never ran was reported as an answer")
	}
}
