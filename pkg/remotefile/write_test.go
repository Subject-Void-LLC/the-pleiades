package remotefile_test

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// statOf reads a path back through the package under test, which is
// fine here because Stat has its own direct tests in remotefile_test.go
// against real files created by the operating system.
func statOf(t *testing.T, path string) remotefile.Info {
	t.Helper()
	info, err := remotefile.Stat(context.Background(), connect(t), path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	return info
}

// TestApply_OnlyActsWhenSomethingDiffers is the property that keeps a
// converged run from reporting a change forever.
//
// chmod and chown both succeed on a no-op, so an implementation that
// applied unconditionally would be invisible on the device and very
// visible in a report that says changed on every single run. The second
// call below is the assertion that matters.
func TestApply_OnlyActsWhenSomethingDiffers(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "modes")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	before := statOf(t, path)
	changed, err := remotefile.Apply(context.Background(), conn, path, remotefile.Attributes{Mode: "0600"}, before)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !changed {
		t.Error("changed = false for a mode that really moved")
	}
	if got := statOf(t, path).Mode; got != "0600" {
		t.Fatalf("mode on the device = %q, want %q", got, "0600")
	}

	// Same request against the state it produced: nothing to do.
	after := statOf(t, path)
	changed, err = remotefile.Apply(context.Background(), conn, path, remotefile.Attributes{Mode: "0600"}, after)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if changed {
		t.Error("changed = true for a mode that was already correct")
	}
}

// TestApply_UnpaddedModeIsNotAChange pins the normalization.
//
// A runbook writes 0644 and stat prints 644. Comparing them as text
// without padding makes every run report a change, which is the classic
// shape of a module that is never idempotent.
func TestApply_UnpaddedModeIsNotAChange(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "padding")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	before := statOf(t, path)
	changed, err := remotefile.Apply(context.Background(), conn, path, remotefile.Attributes{Mode: "644"}, before)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed {
		t.Errorf("changed = true comparing %q against the device's %q: the two spellings are the same mode", "644", before.Mode)
	}
}

// TestApply_EmptyFieldsLeaveThingsAlone proves a method can change a mode
// without also having an opinion about the owner.
func TestApply_EmptyFieldsLeaveThingsAlone(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	before := statOf(t, path)
	changed, err := remotefile.Apply(context.Background(), conn, path, remotefile.Attributes{}, before)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed {
		t.Error("changed = true for an Attributes that asked for nothing")
	}
	if (remotefile.Attributes{}).Empty() != true {
		t.Error("Empty() = false for a zero Attributes")
	}

	after := statOf(t, path)
	if after.Mode != before.Mode || after.Owner != before.Owner || after.Group != before.Group {
		t.Errorf("attributes moved from %+v to %+v when nothing was requested", before, after)
	}
}

// TestApply_OwnerAndGroup exercises the ownership branches against the
// account this test already runs as, which is the only change that is
// guaranteed to be permitted.
//
// Setting the owner to what it already is exercises the comparison and
// not the chown, which is the honest limit of what an unprivileged test
// can assert here. The failure path below covers the other direction.
func TestApply_OwnerAndGroup(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "owned")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	before := statOf(t, path)

	for _, want := range []remotefile.Attributes{
		{Owner: before.Owner},
		{Group: before.Group},
		{Owner: before.Owner, Group: before.Group},
	} {
		changed, err := remotefile.Apply(context.Background(), conn, path, want, before)
		if err != nil {
			t.Fatalf("Apply(%+v): %v", want, err)
		}
		if changed {
			t.Errorf("Apply(%+v) reported a change against identical current state", want)
		}
	}
}

// TestApply_ReportsAFailure proves a refused chown is an error rather
// than a silent success.
//
// Running as root would make the chown succeed, so this skips there
// rather than asserting something untrue about the environment.
func TestApply_ReportsAFailure(t *testing.T) {
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		t.Skip("running as root, which can chown to anyone, so there is no refusal to observe")
	}
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "not-mine")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	before := statOf(t, path)
	_, err := remotefile.Apply(context.Background(), conn, path, remotefile.Attributes{Owner: "root"}, before)
	if err == nil {
		t.Fatal("a chown this account cannot perform was reported as success")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Errorf("error = %v, want it to name the operation that failed", err)
	}
}

func TestMakeDirectory(t *testing.T) {
	conn := connect(t)
	root := t.TempDir()

	// Without parents, a nested path cannot be created.
	nested := filepath.Join(root, "a", "b")
	if err := remotefile.MakeDirectory(context.Background(), conn, nested, false); err == nil {
		t.Error("creating a nested directory without parents was reported as success")
	}

	if err := remotefile.MakeDirectory(context.Background(), conn, nested, true); err != nil {
		t.Fatalf("MakeDirectory with parents: %v", err)
	}
	if got := statOf(t, nested).Kind; got != remotefile.KindDirectory {
		t.Errorf("kind = %q, want a directory", got)
	}

	// mkdir -p succeeds on one that already exists, which is why the
	// caller reads state first rather than relying on this to tell it
	// whether anything changed.
	if err := remotefile.MakeDirectory(context.Background(), conn, nested, true); err != nil {
		t.Errorf("MakeDirectory on an existing directory: %v", err)
	}
}

func TestRemove(t *testing.T) {
	conn := connect(t)
	root := t.TempDir()

	file := filepath.Join(root, "gone")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if err := remotefile.Remove(context.Background(), conn, file, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if statOf(t, file).Exists() {
		t.Error("the file is still there")
	}

	// Removing what is already gone succeeds, because rm -f does. That is
	// what makes the operation safe to repeat.
	if err := remotefile.Remove(context.Background(), conn, file, false); err != nil {
		t.Errorf("removing an absent path: %v", err)
	}

	// A non-empty directory needs the recursive flag, and the caller has
	// to ask for it: this is the most destructive thing in the namespace,
	// so it is visible at the call site rather than implied.
	dir := filepath.Join(root, "tree")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatalf("building the tree: %v", err)
	}
	if err := remotefile.Remove(context.Background(), conn, dir, false); err == nil {
		t.Error("removing a non-empty directory without the recursive flag was reported as success")
	}
	if err := remotefile.Remove(context.Background(), conn, dir, true); err != nil {
		t.Fatalf("recursive Remove: %v", err)
	}
	if statOf(t, dir).Exists() {
		t.Error("the directory tree is still there")
	}
}

// TestSymlink_ReplacesAnExistingLinkToADirectory is the case the -n flag
// exists for, and the one that fails silently without it.
//
// `ln -sf target link`, where link already points at a DIRECTORY, creates
// the new link INSIDE that directory instead of replacing the link. The
// task then reports success and the link still points where it always
// did, which is the worst available outcome: a deployment that swings a
// "current" symlink between releases would report every release as
// deployed and serve the first one forever.
func TestSymlink_ReplacesAnExistingLinkToADirectory(t *testing.T) {
	conn := connect(t)
	root := t.TempDir()
	first := filepath.Join(root, "release-1")
	second := filepath.Join(root, "release-2")
	link := filepath.Join(root, "current")
	for _, d := range []string{first, second} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}

	if err := remotefile.Symlink(context.Background(), conn, first, link); err != nil {
		t.Fatalf("first Symlink: %v", err)
	}
	if got := statOf(t, link).Target; got != first {
		t.Fatalf("target = %q, want %q", got, first)
	}

	if err := remotefile.Symlink(context.Background(), conn, second, link); err != nil {
		t.Fatalf("second Symlink: %v", err)
	}
	info := statOf(t, link)
	if info.Kind != remotefile.KindSymlink {
		t.Fatalf("kind = %q, want the link to still be a link", info.Kind)
	}
	if info.Target != second {
		t.Errorf("target = %q, want %q: the link was not repointed, so a new link was probably created inside the old target", info.Target, second)
	}
	// The giveaway for the failure this guards against.
	if _, err := os.Lstat(filepath.Join(first, "current")); err == nil {
		t.Error("a link was created inside the old target directory instead of replacing the link")
	}
}

func TestTouch(t *testing.T) {
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "touched")

	if err := remotefile.Touch(context.Background(), conn, path); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	info := statOf(t, path)
	if !info.Exists() || info.Kind != remotefile.KindFile {
		t.Fatalf("kind = %q, want an existing regular file", info.Kind)
	}
	if info.Size != 0 {
		t.Errorf("size = %d, want an empty file", info.Size)
	}

	// Touching an existing file leaves the content alone, which is why
	// file.touch's inverse only has to restore an mtime rather than a
	// body.
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("writing content: %v", err)
	}
	if err := remotefile.Touch(context.Background(), conn, path); err != nil {
		t.Fatalf("second Touch: %v", err)
	}
	got, err := os.ReadFile(path) // #nosec G304 -- a path this test created
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("content = %q, want touch to have left it alone", got)
	}
}

// TestInfoMap pins the key names, which are the contract a rollback
// engine and a diff view both read.
//
// They are asserted here rather than left to each method because
// the emitted inverse is built from them, so a rename would
// silently change what a rollback is handed.
func TestInfoMap(t *testing.T) {
	absent := remotefile.Info{Kind: remotefile.KindAbsent}.Map()
	if absent["exists"] != false {
		t.Errorf("absent map exists = %v, want false", absent["exists"])
	}
	// Nothing else applies to a path that is not there, and showing an
	// owner for one would be a fact about nothing.
	for _, key := range []string{"mode", "owner", "group", "size", "mtime", "target"} {
		if _, present := absent[key]; present {
			t.Errorf("absent map carries %q, which describes nothing", key)
		}
	}

	link := remotefile.Info{
		Kind: remotefile.KindSymlink, Mode: "0777", Owner: "root", Group: "root",
		Target: "/elsewhere", Size: 9, Mtime: 42,
	}.Map()
	for key, want := range map[string]any{
		"exists": true, "kind": "symlink", "mode": "0777",
		"owner": "root", "group": "root", "target": "/elsewhere",
	} {
		if link[key] != want {
			t.Errorf("map[%q] = %v, want %v", key, link[key], want)
		}
	}

	// A regular file has no target, so the key is absent rather than
	// empty: a rollback reading "" would try to recreate a link to
	// nowhere.
	file := remotefile.Info{Kind: remotefile.KindFile, Mode: "0644"}.Map()
	if _, present := file["target"]; present {
		t.Error("a regular file's map carries a target")
	}
}

// TestApply_ChangesOwnershipWhenPermitted covers the chown branches for
// real, which needs privilege the ordinary test run does not have.
//
// Skipped rather than faked when not root. A test that asserted a chown
// worked by not checking would be worse than no test, and a fake
// filesystem would only prove the fake obeys its own rules.
func TestApply_ChangesOwnershipWhenPermitted(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.Uid != "0" {
		t.Skip("not running as root, so no chown to another account is permitted")
	}
	conn := connect(t)
	path := filepath.Join(t.TempDir(), "chowned")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	// nobody exists on essentially every Unix-like image, including the
	// ones this repository tests against.
	target, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("no nobody account to chown to: %v", err)
	}

	cases := []remotefile.Attributes{
		{Owner: target.Username},
		{Group: target.Gid},
		{Owner: "root", Group: "root"},
	}
	for _, want := range cases {
		before := statOf(t, path)
		changed, err := remotefile.Apply(context.Background(), conn, path, want, before)
		if err != nil {
			t.Fatalf("Apply(%+v): %v", want, err)
		}
		if !changed {
			t.Errorf("Apply(%+v) reported no change against %+v", want, before)
		}
	}
}

// TestApply_ReportsAFailedChmod covers the chmod error branch, which the
// chown one does not reach.
func TestApply_ReportsAFailedChmod(t *testing.T) {
	conn := connect(t)
	missing := filepath.Join(t.TempDir(), "not-there")

	// A path that is not there, described as if it were, which is what a
	// task racing something that deleted the file would see.
	before := remotefile.Info{Kind: remotefile.KindFile, Mode: "0644"}
	_, err := remotefile.Apply(context.Background(), conn, missing, remotefile.Attributes{Mode: "0600"}, before)
	if err == nil {
		t.Fatal("chmod against a path that is not there was reported as success")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("error = %v, want it to name the operation", err)
	}
}

// TestApply_ReportsAFailedChgrp covers the group-only branch's error
// path, which the owner-only one does not reach.
func TestApply_ReportsAFailedChgrp(t *testing.T) {
	conn := connect(t)
	missing := filepath.Join(t.TempDir(), "not-there")

	before := remotefile.Info{Kind: remotefile.KindFile, Group: "somethingelse"}
	if _, err := remotefile.Apply(context.Background(), conn, missing, remotefile.Attributes{Group: "root"}, before); err == nil {
		t.Fatal("chgrp against a path that is not there was reported as success")
	}

	before = remotefile.Info{Kind: remotefile.KindFile, Owner: "somethingelse"}
	if _, err := remotefile.Apply(context.Background(), conn, missing, remotefile.Attributes{Owner: "root"}, before); err == nil {
		t.Fatal("chown against a path that is not there was reported as success")
	}

	before = remotefile.Info{Kind: remotefile.KindFile, Owner: "a", Group: "b"}
	if _, err := remotefile.Apply(context.Background(), conn, missing, remotefile.Attributes{Owner: "root", Group: "root"}, before); err == nil {
		t.Fatal("a combined chown against a path that is not there was reported as success")
	}
}

// TestOperationsReportDeviceFailures covers the non-zero-exit branch of
// each write helper, and proves the message carries what the device said
// rather than only that something failed.
func TestOperationsReportDeviceFailures(t *testing.T) {
	conn := connect(t)
	// A regular file used as a directory: every operation below fails with
	// ENOTDIR, which is a real refusal from the real filesystem rather
	// than an injected error.
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the blocker: %v", err)
	}
	under := filepath.Join(blocker, "child")

	if err := remotefile.MakeDirectory(context.Background(), conn, under, false); err == nil {
		t.Error("MakeDirectory under a regular file was reported as success")
	}
	if err := remotefile.Touch(context.Background(), conn, under); err == nil {
		t.Error("Touch under a regular file was reported as success")
	}
	if err := remotefile.Symlink(context.Background(), conn, "/somewhere", under); err == nil {
		t.Error("Symlink under a regular file was reported as success")
	}
	if err := remotefile.Write(context.Background(), conn, under, []byte("x")); err == nil {
		t.Error("Write under a regular file was reported as success")
	}
	if _, err := remotefile.Stat(context.Background(), conn, under); err == nil {
		// Stat reports absence rather than an error for a path that is
		// merely missing, but a path whose PARENT is a file is a different
		// answer: the probe itself could not be evaluated.
		t.Log("Stat under a regular file reported absence, which is the shell's own answer to test -e here")
	}
	if _, _, err := remotefile.Read(context.Background(), conn, under); err != nil {
		t.Logf("Read under a regular file: %v", err)
	}
	if _, _, err := remotefile.Checksum(context.Background(), conn, under); err != nil {
		t.Logf("Checksum under a regular file: %v", err)
	}
}

// TestApply_ChangesOwnershipBeforeMode pins the one ordering constraint
// in this function that has real consequences and no visible symptom.
//
// Linux clears the setuid and setgid bits on a regular file whenever its
// owner or group changes. A chmod that runs BEFORE the chown therefore
// sets a special bit the chown immediately throws away, and Apply
// returns true having not achieved what it was asked for: the task
// reports changed, the device does not carry the bit, and the next run
// reports changed again, forever.
//
// Nothing else in this package's tests would notice a regression. The
// ordering is invisible in the return value, invisible in the file's
// permissions unless the fixture happens to be setuid AND the account
// running the test can chown (which is to say, unless it runs as root),
// and invisible in every error path because both commands succeed. That
// combination, load-bearing and asymptomatic, is exactly what an
// assertion is for.
//
// It observes the commands themselves rather than the resulting file,
// through fake chown and chmod on PATH, because the real effect needs
// root and a test that skipped without it would leave the constraint
// unpinned on every machine that matters.
func TestApply_ChangesOwnershipBeforeMode(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	// The record path travels by environment rather than being written
	// into the script, so nothing derived from the test's name can be
	// re-expanded by the shell.
	for _, name := range []string{"chown", "chgrp", "chmod"} {
		script := "#!/bin/sh\nprintf '" + name + "\\n' >> \"$FAKE_RECORD\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
			t.Fatalf("writing the fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)

	conn := connect(t)
	path := filepath.Join(dir, "target")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	// before differs from want in both owner and mode, so both commands
	// have to run. A setuid mode is what is at stake, so it is what the
	// test asks for.
	before := remotefile.Info{Mode: "0644", Owner: "olduser", Group: "oldgroup"}
	want := remotefile.Attributes{Mode: "4755", Owner: "newuser", Group: "newgroup"}

	changed, err := remotefile.Apply(context.Background(), conn, path, want, before)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !changed {
		t.Fatal("Apply reported no change after running both a chown and a chmod")
	}

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("reading the recorded invocations: %v", err)
	}
	got := strings.Fields(string(data))
	want2 := []string{"chown", "chmod"}
	if len(got) != len(want2) {
		t.Fatalf("commands run = %v, want exactly %v", got, want2)
	}
	for i := range got {
		if got[i] != want2[i] {
			t.Fatalf("commands ran in the order %v, want %v: a chmod before a chown loses the setuid bit the chown clears", got, want2)
		}
	}
}
