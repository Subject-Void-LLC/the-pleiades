package remotefile_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// These run against a real SSH server handing every command to a real
// /bin/sh on this machine, so stat, mktemp, mv, chmod, ln and sha256sum
// are the actual programs and the actual filesystem. That is what makes
// the assertions worth anything: the subject here is which command this
// package builds and how it reads the answer, and a stand-in that
// returned canned output would only prove the canned output was canned.

// connect brings up the harness and returns a live connection to it.
func connect(t *testing.T) *remoteexec.Conn {
	t.Helper()

	srv, err := remoteexectest.Start(remoteexectest.Options{})
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
	return conn
}

func TestStat_Absent(t *testing.T) {
	conn := connect(t)

	info, err := remotefile.Stat(context.Background(), conn, filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Absence is an ANSWER, not a failure. A method that treated it as an
	// error could never create anything.
	if info.Exists() {
		t.Errorf("Exists() = true for a path that is not there")
	}
	if info.Kind != remotefile.KindAbsent {
		t.Errorf("Kind = %q, want %q", info.Kind, remotefile.KindAbsent)
	}
}

func TestStat_File(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(path, []byte("hello"), 0o640); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	info, err := remotefile.Stat(context.Background(), conn, path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Kind != remotefile.KindFile {
		t.Errorf("Kind = %q, want %q", info.Kind, remotefile.KindFile)
	}
	// Four digits, so a runbook's "0640" and stat's "640" compare equal.
	// Without this normalization every mode comparison reports a change on
	// every run, which is invisible until somebody notices nothing is ever
	// idempotent.
	if info.Mode != "0640" {
		t.Errorf("Mode = %q, want %q", info.Mode, "0640")
	}
	if info.Size != 5 {
		t.Errorf("Size = %d, want 5", info.Size)
	}
	if info.Owner == "" || info.Group == "" {
		t.Errorf("Owner/Group = %q/%q, want both populated", info.Owner, info.Group)
	}
	if info.Mtime == 0 {
		t.Error("Mtime = 0, want the file's real modification time")
	}
}

func TestStat_Directory(t *testing.T) {
	conn := connect(t)

	info, err := remotefile.Stat(context.Background(), conn, t.TempDir())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Kind != remotefile.KindDirectory {
		t.Errorf("Kind = %q, want %q", info.Kind, remotefile.KindDirectory)
	}
}

// TestStat_SymlinkIsTheLinkNotItsTarget proves a link is reported as a
// link, with where it points.
//
// Following it instead would be wrong for this package's purpose: the
// thing at the path is the link, and the link's target is what an inverse
// would have to restore. Reporting the target's kind would also make
// file.symlink unable to tell "already correct" from "points somewhere
// else".
func TestStat_SymlinkIsTheLinkNotItsTarget(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the link: %v", err)
	}

	info, err := remotefile.Stat(context.Background(), conn, link)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Kind != remotefile.KindSymlink {
		t.Errorf("Kind = %q, want %q", info.Kind, remotefile.KindSymlink)
	}
	if info.Target != target {
		t.Errorf("Target = %q, want %q", info.Target, target)
	}
}

// TestStat_DanglingSymlinkIsStillThere is the case a plain `test -e`
// gets wrong.
//
// A symlink pointing at nothing fails -e, so a probe using only -e
// concludes the path is absent. A method would then try to create over it
// and get EEXIST from the far side, reporting a confusing error instead of
// the real situation.
func TestStat_DanglingSymlinkIsStillThere(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), link); err != nil {
		t.Fatalf("creating the link: %v", err)
	}

	info, err := remotefile.Stat(context.Background(), conn, link)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.Exists() {
		t.Fatal("a dangling symlink was reported as absent, so a create would collide with it")
	}
	if info.Kind != remotefile.KindSymlink {
		t.Errorf("Kind = %q, want %q", info.Kind, remotefile.KindSymlink)
	}
}

// TestStat_HandlesAwkwardPaths proves quoting reaches the shell intact.
//
// A path is the value in this namespace most likely to arrive from a
// runbook variable, so a space or a metacharacter in one must be a path
// rather than syntax.
func TestStat_HandlesAwkwardPaths(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()

	for _, name := range []string{"with space", "with'quote", "with;semicolon", "with$dollar"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("writing %q: %v", name, err)
		}
		info, err := remotefile.Stat(context.Background(), conn, path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", name, err)
		}
		if info.Kind != remotefile.KindFile {
			t.Errorf("Stat(%q).Kind = %q, want a file", name, info.Kind)
		}
	}
}

func TestChecksum(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "content")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	sum, present, err := remotefile.Checksum(context.Background(), conn, path)
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	if !present {
		t.Fatal("present = false for a file that is there")
	}
	// The device's answer must equal the one computed locally, or the
	// comparison that decides whether a write is a change is meaningless.
	if want := remotefile.ChecksumOf([]byte("hello\n")); sum != want {
		t.Errorf("Checksum = %q, want %q", sum, want)
	}

	_, present, err = remotefile.Checksum(context.Background(), conn, filepath.Join(dir, "absent"))
	if err != nil {
		t.Fatalf("Checksum of an absent file: %v", err)
	}
	if present {
		t.Error("present = true for a file that is not there")
	}
}

// TestWrite_IsAtomicAndExact proves the content lands byte for byte and
// that no temporary is left behind.
//
// The leftover check is not incidental. The write goes through a
// temporary in the target's own directory so a reader never sees a
// partial file, and a temporary that survived would accumulate one per
// run in a directory the operator is watching.
func TestWrite_IsAtomicAndExact(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "written")

	content := []byte("first line\nsecond line\nno trailing newline")
	if err := remotefile.Write(context.Background(), conn, path, content); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(path) // #nosec G304 -- a path this test created
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the directory: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pleiades.") {
			t.Errorf("a temporary file %q was left behind", e.Name())
		}
	}
}

// TestWrite_ReplacesRatherThanTruncating is the property that makes the
// temporary worth the trouble.
//
// A plain redirect truncates the target first, so a reader arriving
// mid-transfer sees an empty file and a dropped connection leaves it
// empty forever. Replacing by rename means the old inode is intact until
// the instant the new one takes its place, which this proves by holding
// the old file open and reading it after the write.
func TestWrite_ReplacesRatherThanTruncating(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "replaced")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatalf("writing the original: %v", err)
	}

	// A handle on the ORIGINAL inode, opened before the write.
	original, err := os.Open(path) // #nosec G304 -- a path this test created
	if err != nil {
		t.Fatalf("opening the original: %v", err)
	}
	defer func() { _ = original.Close() }()

	if err := remotefile.Write(context.Background(), conn, path, []byte("new content")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The old handle still reads the old bytes, which is only true if the
	// write replaced the file rather than truncating it in place.
	buf := make([]byte, len("old content"))
	if _, err := original.Read(buf); err != nil {
		t.Fatalf("reading the original handle: %v", err)
	}
	if string(buf) != "old content" {
		t.Errorf("the original inode now reads %q: the write truncated in place rather than replacing", buf)
	}

	current, err := os.ReadFile(path) // #nosec G304 -- a path this test created
	if err != nil {
		t.Fatalf("reading the new file: %v", err)
	}
	if string(current) != "new content" {
		t.Errorf("path now holds %q, want %q", current, "new content")
	}
}

func TestRead(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "readable")
	if err := os.WriteFile(path, []byte("body\n"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	got, present, err := remotefile.Read(context.Background(), conn, path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !present || got != "body\n" {
		t.Errorf("Read = %q, %v, want %q, true", got, present, "body\n")
	}

	// Absent reports present=false rather than an error, so a caller
	// reading a file it may be about to create does not have to tell
	// "empty" from "missing" by inspecting an error string.
	_, present, err = remotefile.Read(context.Background(), conn, filepath.Join(dir, "absent"))
	if err != nil {
		t.Fatalf("Read of an absent file: %v", err)
	}
	if present {
		t.Error("present = true for a file that is not there")
	}
}
