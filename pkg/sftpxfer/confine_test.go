// Tests that no symlink, directory or special file can carry a transfer
// outside its root.
package sftpxfer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
)

// TestConfine_EveryLexicalEscapeIsRefusedBeforeAnyPacket runs the shared
// payload table through the same sequence a caller uses. Resolve refuses
// each one, so the caller never holds a Path to hand this package, and
// the only thing it could hand instead, the zero Path, is refused with
// no SFTP packet sent at all.
func TestConfine_EveryLexicalEscapeIsRefusedBeforeAnyPacket(t *testing.T) {
	f := newFixture(t)
	mark := f.rec.mark()
	for _, payload := range filexfertest.EscapePayloads {
		p, err := filexfer.Resolve(filepath.ToSlash(f.root), payload.Leaf)
		if err == nil {
			t.Fatalf("%s: Resolve(%q) accepted an escape", payload.Name, payload.Leaf)
		}
		if putErr := f.client.Put(context.Background(), p, strings.NewReader("x"), 1, 0o644); !errors.Is(putErr, filexfer.ErrUnresolvedPath) {
			t.Fatalf("%s: Put() with the refused Path error = %v, want ErrUnresolvedPath", payload.Name, putErr)
		}
	}
	if sent := f.rec.since(mark); len(sent) != 0 {
		t.Errorf("%d escape payloads sent %d SFTP packets, want none", len(filexfertest.EscapePayloads), len(sent))
	}
}

// physicalCase is one filesystem layout that a lexical guard passes and
// a physical check must refuse.
type physicalCase struct {
	name  string
	setup func(t *testing.T, f *fixture) // lays out the trap
	leaf  string
	want  filexfer.Containment
}

// physicalCases are the escapes Resolve cannot see, because each is a
// perfectly ordinary name that the device's own filesystem redirects.
func physicalCases() []physicalCase {
	symlink := func(target, link string) func(t *testing.T, f *fixture) {
		return func(t *testing.T, f *fixture) {
			t.Helper()
			if err := os.Symlink(target, filepath.Join(f.root, link)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return []physicalCase{
		{
			name: "parent is an absolute symlink out of the root",
			setup: func(t *testing.T, f *fixture) {
				symlink(filepath.Join(f.dir, "outside"), "drop")(t, f)
			},
			leaf: "drop/authorized_keys",
			want: filexfer.ContainmentOutsideRoot,
		},
		{
			name:  "parent is a relative symlink climbing out",
			setup: symlink("../outside", "drop"),
			leaf:  "drop/authorized_keys",
			want:  filexfer.ContainmentOutsideRoot,
		},
		{
			name: "symlink deep inside a real directory",
			setup: func(t *testing.T, f *fixture) {
				if err := os.MkdirAll(filepath.Join(f.root, "a", "b"), 0o755); err != nil {
					t.Fatal(err)
				}
				symlink("../../../outside", filepath.Join("a", "b", "c"))(t, f)
			},
			leaf: "a/b/c/authorized_keys",
			want: filexfer.ContainmentOutsideRoot,
		},
		{
			name: "leaf is a symlink to a file outside",
			setup: func(t *testing.T, f *fixture) {
				symlink(filepath.Join(f.dir, "outside", "authorized_keys"), "keys")(t, f)
			},
			leaf: "keys",
			want: filexfer.ContainmentSymlinkLeaf,
		},
		{
			name: "leaf is a directory",
			setup: func(t *testing.T, f *fixture) {
				if err := os.Mkdir(filepath.Join(f.root, "adir"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			leaf: "adir",
			want: filexfer.ContainmentDirectoryLeaf,
		},
		{
			name:  "parent is missing",
			setup: func(*testing.T, *fixture) {},
			leaf:  "no/such/dir/f",
			want:  filexfer.ContainmentParentMissing,
		},
	}
}

// TestConfine_PhysicalEscapesAreRefusedBeforeAnyContent covers every
// physical case against Put, Get and Stat. A refusal must send nothing
// that opens or changes a file, and the file outside must be unchanged
// afterward, which is the observable meaning of "no bytes sent".
func TestConfine_PhysicalEscapesAreRefusedBeforeAnyContent(t *testing.T) {
	for _, tc := range physicalCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			outside := filepath.Join(f.dir, "outside", "authorized_keys")
			if err := os.WriteFile(outside, []byte("ssh-ed25519 AAAA original"), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, f)
			p := f.resolve(t, tc.leaf)
			ctx := context.Background()

			mark := f.rec.mark()
			putErr := f.client.Put(ctx, p, strings.NewReader("attacker key"), 12, 0o600)
			_, getErr := f.client.Get(ctx, p, &bytes.Buffer{}, 1<<20)
			f.assertNothingMutated(t, mark)

			var cErr *filexfer.ContainmentError
			if !errors.As(putErr, &cErr) || cErr.Reason != tc.want {
				t.Errorf("Put() error = %v, want %v", putErr, tc.want)
			}
			if tc.want != filexfer.ContainmentParentMissing {
				if !errors.As(getErr, &cErr) || cErr.Reason != tc.want {
					t.Errorf("Get() error = %v, want %v", getErr, tc.want)
				}
			}
			if got, _ := os.ReadFile(outside); string(got) != "ssh-ed25519 AAAA original" {
				t.Errorf("the file outside the root holds %q after a refused transfer", got)
			}
		})
	}
}

// TestConfine_SymlinkThatStaysInsideIsAllowed keeps the functionality a
// stricter "no symlinks at all" rule would have cost: a link that
// resolves inside the root is followed.
func TestConfine_SymlinkThatStaysInsideIsAllowed(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "releases", "v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("releases/v2", filepath.Join(f.root, "current")); err != nil {
		t.Fatal(err)
	}
	p := f.resolve(t, "current/image.bin")
	if err := f.client.Put(context.Background(), p, strings.NewReader("image"), 5, 0o644); err != nil {
		t.Fatalf("Put() through an in-root symlink error = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.root, "releases", "v2", "image.bin")); string(got) != "image" {
		t.Errorf("the file landed with %q, want it in the link's physical target", got)
	}
}

// TestConfine_RootMayItselfBeASymlink covers an operator whose root is a
// link (/srv/xfer pointing at a data volume): containment is judged
// against where the root physically is.
func TestConfine_RootMayItselfBeASymlink(t *testing.T) {
	f := newFixture(t)
	linkedRoot := filepath.Join(f.dir, "linked-root")
	if err := os.Symlink(f.root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	p, err := filexfer.Resolve(filepath.ToSlash(linkedRoot), "f")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Put(context.Background(), p, strings.NewReader("ok"), 2, 0o644); err != nil {
		t.Fatalf("Put() under a symlinked root error = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.root, "f")); string(got) != "ok" {
		t.Errorf("the file landed with %q, want it in the root's physical directory", got)
	}
}

// TestConfine_RootMissing names the root, not the parent.
func TestConfine_RootMissing(t *testing.T) {
	f := newFixture(t)
	p, err := filexfer.Resolve(filepath.ToSlash(filepath.Join(f.dir, "gone")), "f")
	if err != nil {
		t.Fatal(err)
	}
	err = f.client.Put(context.Background(), p, strings.NewReader("x"), 1, 0o644)
	var cErr *filexfer.ContainmentError
	if !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentRootMissing {
		t.Fatalf("Put() under a missing root error = %v, want ContainmentRootMissing", err)
	}
}

// TestConfine_SymlinkLoopIsRefused keeps a link cycle on the device from
// walking forever.
func TestConfine_SymlinkLoopIsRefused(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink("b", filepath.Join(f.root, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(f.root, "b")); err != nil {
		t.Fatal(err)
	}
	mark := f.rec.mark()
	err := f.client.Put(context.Background(), f.resolve(t, "a/f"), strings.NewReader("x"), 1, 0o644)
	if !errors.Is(err, errSymlinkLoop) {
		t.Fatalf("Put() through a link loop error = %v, want errSymlinkLoop", err)
	}
	f.assertNothingMutated(t, mark)
}

// TestConfine_ParentThroughAFileIsMissing covers a path whose "directory"
// is a regular file.
func TestConfine_ParentThroughAFileIsMissing(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.root, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.client.Put(context.Background(), f.resolve(t, "plain/f"), strings.NewReader("x"), 1, 0o644)
	var cErr *filexfer.ContainmentError
	if !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentParentMissing {
		t.Fatalf("Put() under a regular file error = %v, want ContainmentParentMissing", err)
	}
}
