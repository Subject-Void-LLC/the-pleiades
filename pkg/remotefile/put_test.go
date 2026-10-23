// Tests for Put and PredictPut against real files on a real SSH session.
package remotefile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestPut_ANewFileGetsItsModeBeforeItAppears: a file Put creates carries
// the mode it was asked for, and nothing is left beside it.
func TestPut_ANewFileGetsItsModeBeforeItAppears(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.yaml")
	r, err := remotefile.Put(context.Background(), conn, p, []byte("verify: true\n"), remotefile.Info{Kind: remotefile.KindAbsent}, remotefile.Attributes{Mode: "0644"})
	if err != nil || !r.Touched || r.InPlace {
		t.Fatalf("Put = %+v, %v", r, err)
	}
	got, _ := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "verify: true\n" || info.Mode().Perm() != 0o644 {
		t.Errorf("after Put: %q, mode %v", got, info.Mode().Perm())
	}
	leftBeside(t, dir)
}

// TestPut_KeepingAFileItOwnsRenamesAndKeepsItsMode: with nothing named, an
// existing file this login owns is replaced by a rename and keeps its mode.
func TestPut_KeepingAFileItOwnsRenamesAndKeepsItsMode(t *testing.T) {
	conn := connect(t)
	dir, p := replaceFixture(t, "old\n", 0o640)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := remotefile.Put(context.Background(), conn, p, []byte("new\n"), before, remotefile.Attributes{})
	if err != nil || !r.Touched || r.InPlace {
		t.Fatalf("Put = %+v, %v", r, err)
	}
	got, _ := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	info, _ := os.Stat(p)
	if string(got) != "new\n" || info.Mode().Perm() != 0o640 {
		t.Errorf("after Put: %q, mode %v", got, info.Mode().Perm())
	}
	leftBeside(t, dir)
}

// TestPut_KeepingAGroupItCannotGiveCopiesInPlace: keeping an existing
// file's attributes when a new file could not take its group is
// Replace's in-place copy, which keeps the file itself.
func TestPut_KeepingAGroupItCannotGiveCopiesInPlace(t *testing.T) {
	conn := connect(t)
	dir, p := replaceFixture(t, "old\n", 0o640)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	was, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	before.Group = noSuchGroup
	r, err := remotefile.Put(context.Background(), conn, p, []byte("new\n"), before, remotefile.Attributes{})
	if err != nil || !r.InPlace || !r.Touched {
		t.Fatalf("Put = %+v, %v", r, err)
	}
	now, _ := os.Stat(p)
	got, _ := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	if string(got) != "new\n" || !os.SameFile(was, now) {
		t.Errorf("after an in-place Put: %q, the same file: %v", got, os.SameFile(was, now))
	}
	leftBeside(t, dir)
}

// TestPut_AGroupItCannotGiveIsRefusedUntouched: a group the task names
// that this login may not give is refused before the rename, and the file
// keeps its bytes, its mode and its inode. This is the order the lab found
// wrong: the rename came first and the chown after it failed.
func TestPut_AGroupItCannotGiveIsRefusedUntouched(t *testing.T) {
	conn := connect(t)
	dir, p := replaceFixture(t, "old\n", 0o664)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	was, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := remotefile.Put(context.Background(), conn, p, []byte("new\n"), before, remotefile.Attributes{Group: noSuchGroup})
	var refusal *remotefile.AttributeRefusal
	if !errors.As(err, &refusal) || refusal.Field != "group" || refusal.Value != noSuchGroup || r.Touched {
		t.Fatalf("Put = %+v, %v; want an untouched group refusal", r, err)
	}
	got, _ := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	now, _ := os.Stat(p)
	if string(got) != "old\n" || now.Mode().Perm() != 0o664 || !os.SameFile(was, now) {
		t.Errorf("after a refused Put: %q, mode %v, the same file: %v", got, now.Mode().Perm(), os.SameFile(was, now))
	}
	leftBeside(t, dir)

	// A check refuses the same.
	if err := remotefile.PredictPut(context.Background(), conn, p, before, remotefile.Attributes{Group: noSuchGroup}); !errors.As(err, &refusal) || refusal.Field != "group" {
		t.Errorf("PredictPut = %v, want the same group refusal", err)
	}
}

// TestPut_AnOwnerItCannotGiveIsRefusedUntouched: a login that is not root
// may not give a file away, so naming another owner is refused before the
// rename, and a check refuses the same.
func TestPut_AnOwnerItCannotGiveIsRefusedUntouched(t *testing.T) {
	need.Require(t, "non-root", os.Geteuid() != 0, "root may give a file to any owner")
	conn := connect(t)
	dir, p := replaceFixture(t, "old\n", 0o644)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	want := remotefile.Attributes{Owner: "root"}
	var refusal *remotefile.AttributeRefusal
	if err := remotefile.PredictPut(context.Background(), conn, p, before, want); !errors.As(err, &refusal) || refusal.Field != "owner" || refusal.Login == "" {
		t.Fatalf("PredictPut = %v, want an owner refusal naming the login", err)
	}
	r, err := remotefile.Put(context.Background(), conn, p, []byte("new\n"), before, want)
	if !errors.As(err, &refusal) || refusal.Field != "owner" || r.Touched {
		t.Fatalf("Put = %+v, %v; want an untouched owner refusal", r, err)
	}
	if got, _ := os.ReadFile(p); string(got) != "old\n" { // #nosec G304 -- a path under this test's own directory
		t.Errorf("the file became %q", got)
	}
	leftBeside(t, dir)
}

// TestPredictPut_AgreesWithPutWhenItWrites: a check answers nil exactly
// where a real write succeeds: a new file, a mode change on a file this
// login owns, and keeping a file it owns. Each case then runs Put on its
// own fixture, in a directory whose group (wheel on FreeBSD) a new file
// takes there.
func TestPredictPut_AgreesWithPutWhenItWrites(t *testing.T) {
	conn := connect(t)
	cases := []struct {
		name     string
		existing bool
		want     remotefile.Attributes
	}{
		{"a new file", false, remotefile.Attributes{Mode: "0600"}},
		{"a mode change", true, remotefile.Attributes{Mode: "0600"}},
		{"keeping the file", true, remotefile.Attributes{}},
	}
	for _, c := range cases {
		dir, p := replaceFixture(t, "old\n", 0o640)
		before := remotefile.Info{Kind: remotefile.KindAbsent}
		if c.existing {
			var err error
			if before, err = remotefile.Stat(context.Background(), conn, p); err != nil {
				t.Fatal(err)
			}
		} else {
			p = filepath.Join(dir, "new")
		}
		if err := remotefile.PredictPut(context.Background(), conn, p, before, c.want); err != nil {
			t.Errorf("%s: PredictPut = %v", c.name, err)
		}
		if _, err := remotefile.Put(context.Background(), conn, p, []byte("new\n"), before, c.want); err != nil {
			t.Errorf("%s: Put = %v", c.name, err)
		}
	}
}

// TestPredictPut_RefusesWhatReplaceRefuses: keeping a file whose group a
// new file cannot take, and which this login may not write, is refused by
// a check exactly as Replace refuses it.
func TestPredictPut_RefusesWhatReplaceRefuses(t *testing.T) {
	need.Require(t, "non-root", os.Geteuid() != 0, "root writes a file whose mode forbids it")
	conn := connect(t)
	_, p := replaceFixture(t, "old\n", 0o444)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	before.Group = noSuchGroup
	predicted := remotefile.PredictPut(context.Background(), conn, p, before, remotefile.Attributes{})
	_, ran := remotefile.Put(context.Background(), conn, p, []byte("x"), before, remotefile.Attributes{})
	if predicted == nil || ran == nil {
		t.Fatalf("PredictPut = %v, Put = %v; want both refused", predicted, ran)
	}
}

// TestPut_RefusesWhatIsNotARegularFile: a directory at the path is refused
// by both, untouched.
func TestPut_RefusesWhatIsNotARegularFile(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	info, err := remotefile.Stat(context.Background(), conn, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := remotefile.Put(context.Background(), conn, dir, []byte("x"), info, remotefile.Attributes{}); err == nil || r.Touched {
		t.Errorf("Put of a directory = %+v, %v", r, err)
	}
	if err := remotefile.PredictPut(context.Background(), conn, dir, info, remotefile.Attributes{}); err == nil {
		t.Error("PredictPut of a directory = nil")
	}
}
