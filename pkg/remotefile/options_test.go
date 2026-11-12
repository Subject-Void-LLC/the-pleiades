// Tests that no word pkg/remotefile sends after a command's options can be
// read as an option: an owner or group chown or chgrp is given, the target
// ln is given, and a relative path starting with a dash. Each runs the real
// tool through a real shell over SSH (FAILURE_PATTERNS 1174).
package remotefile_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestApply_ReadsAnOwnerOrGroupStartingWithADashAsAName hands Apply an
// owner and a group that GNU chown and chgrp read as their --reference
// option when no "--" comes first: "give the file the owner and group of
// this other file", which succeeds. After "--" each is a name no account
// has, so the command fails and Apply reports it. (BSD's and BusyBox's
// chown refuse the option either way; the case that proves "--" is GNU's.)
func TestApply_ReadsAnOwnerOrGroupStartingWithADashAsAName(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	reference := filepath.Join(dir, "reference")
	target := filepath.Join(dir, "target")
	for _, p := range []string{reference, target} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	option := "--reference=" + reference
	for name, want := range map[string]remotefile.Attributes{
		"owner": {Owner: option},
		"group": {Group: option},
	} {
		t.Run(name, func(t *testing.T) {
			before := statOf(t, target)
			changed, err := remotefile.Apply(context.Background(), conn, target, want, before)
			if err == nil {
				t.Fatalf("Apply(%+v) = changed %v, no error: the name reached chown as its --reference option", want, changed)
			}
			if changed {
				t.Errorf("Apply reported a change beside its error: %v", err)
			}
			if after := statOf(t, target); after.Owner != before.Owner || after.Group != before.Group {
				t.Errorf("ownership moved from %s:%s to %s:%s", before.Owner, before.Group, after.Owner, after.Group)
			}
		})
	}
}

// TestSymlink_ReadsATargetStartingWithADashAsTheTarget is the case that
// mattered most. Without "--", GNU ln read the target -t<dir> as "make the
// links in <dir>" and, with -f, replaced <dir>/<base of the link path>
// with a link, whatever file was there, while file.symlink had checked
// only the link path it was given. Here the file in <dir> is untouched and
// the link is made where it was asked, holding the target as written.
func TestSymlink_ReadsATargetStartingWithADashAsTheTarget(t *testing.T) {
	conn := connect(t)
	victimDir, linkDir := t.TempDir(), t.TempDir()
	victim := filepath.Join(victimDir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "victim")
	target := "-t" + victimDir

	if err := remotefile.Symlink(context.Background(), conn, target, link); err != nil {
		t.Fatalf("Symlink(%q, %q): %v", target, link, err)
	}
	info, err := os.Lstat(victim)
	if err != nil {
		t.Fatalf("the file in %s is gone: %v", victimDir, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("the file in %s is now %v, not a regular file: ln read the target as -t", victimDir, info.Mode())
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep\n" { // #nosec G304 -- a path under this test's own directory
		t.Errorf("the file in %s now holds %q (%v)", victimDir, data, err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Errorf("the link at %s points at %q (%v), want %q", link, got, err, target)
	}
}

// TestEndsOptions_EveryCommandApplyAndTheWritersSend records the argument
// list each tool receives, through stand-ins on PATH, and holds every one
// to the same shape: "--" right after the command's own options, and the
// path last. A real tool's acceptance of "--" is proven by the tests
// around this one, which run the real tools.
func TestEndsOptions_EveryCommandApplyAndTheWritersSend(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	// Each stand-in writes its name and arguments one per line, then a
	// separator. The record path travels by environment, so nothing from
	// the test is re-expanded by the shell.
	for _, name := range []string{"chown", "chgrp", "chmod", "rm", "mkdir", "ln", "touch"} {
		script := "#!/bin/sh\n{ printf '%s\\n' " + name + "; for a in \"$@\"; do printf '%s\\n' \"$a\"; done; printf -- '---\\n'; } >> \"$FAKE_RECORD\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { // #nosec G306 -- a stand-in that must be executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	conn := connect(t)
	ctx := context.Background()
	path := "/srv/app/target"
	before := remotefile.Info{Kind: remotefile.KindFile, Mode: "0644", Owner: "olduser", Group: "oldgroup"}

	for _, want := range []remotefile.Attributes{
		{Owner: "newuser", Group: "newgroup", Mode: "0600"},
		{Owner: "newuser"},
		{Group: "newgroup"},
	} {
		if _, err := remotefile.Apply(ctx, conn, path, want, before); err != nil {
			t.Fatalf("Apply(%+v): %v", want, err)
		}
	}
	for _, call := range []func() error{
		func() error { return remotefile.Remove(ctx, conn, path, false) },
		func() error { return remotefile.Remove(ctx, conn, path, true) },
		func() error { return remotefile.MakeDirectory(ctx, conn, path, false) },
		func() error { return remotefile.MakeDirectory(ctx, conn, path, true) },
		func() error { return remotefile.Symlink(ctx, conn, "-target", path) },
		func() error { return remotefile.Touch(ctx, conn, path) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(record) // #nosec G304 -- a path under this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"chown -- newuser:newgroup " + path,
		"chmod -- 00600 " + path,
		"chown -- newuser " + path,
		"chgrp -- newgroup " + path,
		"rm -f -- " + path,
		"rm -rf -- " + path,
		"mkdir -- " + path,
		"mkdir -p -- " + path,
		"ln -sfn -- -target " + path,
		"touch -- " + path,
	}
	var got []string
	for _, call := range strings.Split(strings.TrimSuffix(string(data), "---\n"), "---\n") {
		got = append(got, strings.Join(strings.Fields(call), " "))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the tools received:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestReadersTakeARelativePathStartingWithADashAsAPath: Checksum, Read
// and Write name a file whose relative path starts with a dash as ./path,
// so sha256sum, shasum, cat and mktemp each read it as the file. A
// relative path is the login's, and the harness's shell starts in this
// process's working directory, so the test moves it to a directory of its
// own first (t.Chdir).
func TestReadersTakeARelativePathStartingWithADashAsAPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "-d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "-n"), []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	conn := connect(t)
	ctx := context.Background()

	if got, ok, err := remotefile.Read(ctx, conn, "-n"); err != nil || !ok || got != "held\n" {
		t.Errorf("Read(-n) = %q, %v, %v; want the file's own bytes, not cat -n of empty input", got, ok, err)
	}
	if sum, ok, err := remotefile.Checksum(ctx, conn, "-n"); err != nil || !ok || sum != remotefile.ChecksumOf([]byte("held\n")) {
		t.Errorf("Checksum(-n) = %q, %v, %v", sum, ok, err)
	}
	if err := remotefile.Write(ctx, conn, "-d/new", []byte("written\n")); err != nil {
		t.Fatalf("Write(-d/new): %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "-d", "new")); err != nil || string(data) != "written\n" { // #nosec G304 -- a path under this test's own directory
		t.Errorf("-d/new holds %q (%v)", data, err)
	}
}

// TestReplace_WritesBesideAFileUnderADirectoryStartingWithADash: Replace
// puts the new content beside the file with mktemp, whose template starts
// with the file's directory, so a relative directory starting with a dash
// is given as ./dir, as Write's is. Before, mktemp read "-d/.pleiades.XXXXXX"
// as its -d option and the replace failed.
func TestReplace_WritesBesideAFileUnderADirectoryStartingWithADash(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "-d"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "-d", "conf")
	if err := os.WriteFile(file, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	conn := connect(t)
	ctx := context.Background()

	before, err := remotefile.Stat(ctx, conn, "-d/conf")
	if err != nil {
		t.Fatalf("Stat(-d/conf): %v", err)
	}
	if _, err := remotefile.Replace(ctx, conn, "-d/conf", []byte("new\n"), before); err != nil {
		t.Fatalf("Replace(-d/conf): %v", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "new\n" { // #nosec G304 -- a path under this test's own directory
		t.Errorf("-d/conf holds %q (%v), want the new content", data, err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("-d/conf is %v (%v), want its mode 0640 kept", info.Mode(), err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "-d", ".pleiades.*"))
	if len(leftovers) != 0 {
		t.Errorf("a temporary was left beside the file: %v", leftovers)
	}
}
