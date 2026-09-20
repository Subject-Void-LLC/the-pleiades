// Package archive_test: tests of the archive.create and archive.extract
// checks, against the same real in-process SSH server and real tar the
// rest of this package's tests use.
package archive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// recordingHarness is a harness that keeps its server, so a test can read
// every command a check sent.
type recordingHarness struct {
	*harness
	srv *remoteexectest.Server
}

func newRecordingHarness(t *testing.T) *recordingHarness {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	return &recordingHarness{
		harness: &harness{
			rc: &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}},
			device: &target{
				Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NamePOSIXFileSystem}},
				host: srv.Host, port: srv.Port,
			},
		},
		srv: srv,
	}
}

// onlyReads fails t unless the check sent something and everything it
// sent was one of remotefile.Stat's reads.
func onlyReads(t *testing.T, commands []string) {
	t.Helper()
	if len(commands) == 0 {
		t.Fatal("the check sent nothing, so it read nothing")
	}
	for _, c := range commands {
		if !strings.HasPrefix(c, "if [ -e ") && !strings.HasPrefix(c, "readlink '") {
			t.Errorf("the check sent %q, which is not a read", c)
		}
	}
}

// halves returns a recorded diff's before and after halves.
func halves(t *testing.T, rc *ctxStub) (before, after map[string]any) {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	before, _ = diff[sdk.DiffBefore].(map[string]any)
	after, _ = diff[sdk.DiffAfter].(map[string]any)
	return before, after
}

// checkThenRun runs fqcn's registered check against params on a fresh
// recording harness, then the real run on another as the control, and
// returns both results and contexts.
func checkThenRun(t *testing.T, fqcn string, params map[string]any) (checked, ran collection.Result, crc, rrc *ctxStub, commands []string) {
	t.Helper()
	d := lookup(t, fqcn)
	if !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("%s does not declare a check", fqcn)
	}
	h := newRecordingHarness(t)
	checked, err := d.Check(context.Background(), h.rc, h.device, h.params(params))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	commands = h.srv.Commands()
	real := newHarness(t)
	ran, err = d.Invoke(context.Background(), real.rc, real.device, real.params(params))
	if err != nil {
		t.Fatalf("the real run: %v", err)
	}
	return checked, ran, h.rc, real.rc, commands
}

// predictsTheRealRun fails t unless the check predicted the real run's
// change and every key its after half states is one the real run left,
// and recorded no undo instruction.
func predictsTheRealRun(t *testing.T, checked, ran collection.Result, crc, rrc *ctxStub) {
	t.Helper()
	if checked.Changed != ran.Changed {
		t.Errorf("the check predicted Changed = %v, the real run reported %v", checked.Changed, ran.Changed)
	}
	if _, ok := crc.stats[sdk.StatInverse]; ok {
		t.Error("the check recorded an undo instruction for a change it never made")
	}
	_, predicted := halves(t, crc)
	_, actual := halves(t, rrc)
	for key, want := range predicted {
		if actual[key] != want {
			t.Errorf("predicted %s = %v, the real run left %v", key, want, actual[key])
		}
	}
}

// TestCheckCreate covers archive.create's check from each start: an
// absent archive is predicted written, reading only, and the archive does
// not exist afterwards; one already there is predicted left alone. The
// real run from the same start is each case's control.
func TestCheckCreate(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		writeFile(t, src, "archived")
		path := filepath.Join(dir, "out.tar.gz")

		d := lookup(t, "archive.create")
		h := newRecordingHarness(t)
		checked, err := d.Check(context.Background(), h.rc, h.device, h.params(map[string]any{"path": path, "src": []any{src}, "remove": true}))
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		onlyReads(t, h.srv.Commands())
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the check wrote the archive: %v", err)
		}
		if _, err := os.Stat(src); err != nil {
			t.Fatalf("the check removed src: %v", err)
		}
		if _, predicted := halves(t, h.rc); predicted["exists"] != true || predicted["kind"] != "file" || len(predicted) != 2 {
			t.Errorf("predicted after = %v, want only that a file would exist", predicted)
		}

		real := newHarness(t)
		ran, err := d.Invoke(context.Background(), real.rc, real.device, real.params(map[string]any{"path": path, "src": []any{src}, "remove": true}))
		if err != nil {
			t.Fatalf("the real run: %v", err)
		}
		predictsTheRealRun(t, checked, ran, h.rc, real.rc)
	})

	t.Run("present", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src.txt")
		writeFile(t, src, "archived")
		path := filepath.Join(dir, "out.tar.gz")
		writeFile(t, path, "an archive already")
		checked, ran, crc, rrc, commands := checkThenRun(t, "archive.create", map[string]any{"path": path, "src": []any{src}})
		onlyReads(t, commands)
		if checked.Changed {
			t.Error("the check predicted a write over an archive already there")
		}
		before, after := halves(t, crc)
		if before["size"] != after["size"] || after["exists"] != true {
			t.Errorf("an unchanged archive's diff = %v -> %v, want the same state on both sides", before, after)
		}
		predictsTheRealRun(t, checked, ran, crc, rrc)
	})
}

// TestCheckExtract covers archive.extract's check: without a creates
// guard a real run always extracts, so the check predicts it, reading
// only, and nothing is extracted; with a guard that is already there it
// predicts nothing. The real run is each case's control.
func TestCheckExtract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		destExists bool
		guarded    bool
		changes    bool
	}{
		{"a new dest", false, false, true},
		{"an existing dest", true, false, true},
		{"a guard already there", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "in.tar.gz")
			buildTarGz(t, src, "hello.txt", "extracted")
			dest := filepath.Join(dir, "out")
			if tc.destExists {
				if err := os.Mkdir(dest, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			params := map[string]any{"src": src, "dest": dest}
			if tc.guarded {
				marker := filepath.Join(dir, "done")
				writeFile(t, marker, "")
				params["creates"] = marker
			}
			checked, ran, crc, rrc, commands := checkThenRun(t, "archive.extract", params)
			// The control ran for real, so the check's own absence of an
			// effect is read from what it sent rather than from the disk.
			onlyReads(t, commands)
			if checked.Changed != tc.changes {
				t.Errorf("the check predicted Changed = %v, want %v", checked.Changed, tc.changes)
			}
			if _, after := halves(t, crc); tc.changes && (after["kind"] != "directory" || len(after) != 2) {
				t.Errorf("predicted after = %v, want only that dest would be a directory", after)
			}
			predictsTheRealRun(t, checked, ran, crc, rrc)
		})
	}
}

// TestChecks_WhatTarNeedsIsMissing covers the reads a check makes past
// the real run's: a src, or a directory to hold the archive, missing now,
// and a dest that is not a directory, each make the call unchecked
// (collection.CannotCheckError) rather than failed or predicted, naming
// the path; and nothing is written.
func TestChecks_WhatTarNeedsIsMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "hello.txt", "extracted")
	aFile := filepath.Join(dir, "a-file")
	writeFile(t, aFile, "not a directory")
	for _, tc := range []struct {
		name   string
		fqcn   string
		params map[string]any
		names  string
	}{
		{"a missing src to archive", "archive.create", map[string]any{"path": filepath.Join(dir, "out.tar"), "src": []any{src, filepath.Join(dir, "nope")}}, "src " + filepath.Join(dir, "nope") + " does not exist yet"},
		{"no directory to hold the archive", "archive.create", map[string]any{"path": filepath.Join(dir, "absent", "out.tar"), "src": []any{src}}, filepath.Join(dir, "absent") + " does not exist yet"},
		{"a missing archive to extract", "archive.extract", map[string]any{"src": filepath.Join(dir, "nope.tar"), "dest": filepath.Join(dir, "x")}, "src " + filepath.Join(dir, "nope.tar")},
		{"a dest that is a file", "archive.extract", map[string]any{"src": src, "dest": aFile}, "dest " + aFile + " is a file, not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecordingHarness(t)
			_, err := lookup(t, tc.fqcn).Check(context.Background(), h.rc, h.device, h.params(tc.params))
			var cannot *collection.CannotCheckError
			if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, tc.names) {
				t.Fatalf("check = %v, want a CannotCheckError naming %q", err, tc.names)
			}
			onlyReads(t, h.srv.Commands())
		})
	}
}

// TestChecks_FailWhenTheyCannotReadOrRecord covers a check that cannot
// finish its reads or record its answer. A read that fails is a failure,
// named as one, and never the "cannot check" answer a missing path gets:
// that answer means the device was read and the path was not there, which
// is not what happened. A stat or diff that cannot be recorded fails the
// check, as it fails the real run, rather than reporting a decision with
// nothing behind it.
func TestChecks_FailWhenTheyCannotReadOrRecord(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.tar.gz")
	buildTarGz(t, src, "hello.txt", "extracted")
	create := map[string]any{"path": filepath.Join(dir, "out.tar"), "src": []any{src}}
	extract := map[string]any{"src": src, "dest": filepath.Join(dir, "out")}

	for _, tc := range []struct {
		name    string
		fqcn    string
		params  map[string]any
		budget  int
		failKey string
		want    string
	}{
		// One session reads the archive's path; the directory that would
		// hold it is the second read, and the server refuses it.
		{"create, the holding directory cannot be read", "archive.create", create, 1, "", "archive.create"},
		{"create, the path cannot be recorded", "archive.create", existingArchive(src), -1, "path", `injected failure recording "path"`},
		{"create, the diff cannot be recorded", "archive.create", existingArchive(src), -1, sdk.StatDiff, "injected failure recording"},
		{"extract, the dest cannot be recorded", "archive.extract", extract, -1, "dest", `injected failure recording "dest"`},
		{"extract, the diff cannot be recorded", "archive.extract", extract, -1, sdk.StatDiff, "injected failure recording"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarnessBudgeted(t, tc.budget)
			h.rc.failOnKey = tc.failKey
			_, err := lookup(t, tc.fqcn).Check(context.Background(), h.rc, h.device, h.params(tc.params))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check = %v, want an error containing %q", err, tc.want)
			}
			var cannot *collection.CannotCheckError
			if errors.As(err, &cannot) {
				t.Errorf("a failed read or record was answered as a check that cannot happen: %v", err)
			}
		})
	}
}

// existingArchive is archive.create's params for an archive that already
// exists (the tarball src itself), so the check predicts no change and goes
// straight to recording its answer.
func existingArchive(existing string) map[string]any {
	return map[string]any{"path": existing, "src": []any{existing}}
}
