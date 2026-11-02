// Tests of what this package's checks predict about paths and read of what
// earlier tasks predicted (Phase 46a), against the real SSH harness and a real
// /bin/sh, as remove_test.go's are: every effect is read off the filesystem,
// and every location is the one the device resolves (remotefile.Locate).
package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// predictingContext is removeContext holding a snapshot of what earlier tasks
// in the check predicted, as the engine's context does in a check.
type predictingContext struct {
	*removeContext
	snap *wire.Predictions
}

// Predictions returns the snapshot.
func (c *predictingContext) Predictions() *wire.Predictions { return c.snap }

// checkOf returns fqcn's registered check, the one the engine calls.
func checkOf(t *testing.T, fqcn string) collection.Method {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok || d.Check == nil || !d.Manifest.PredictsPaths {
		t.Fatalf("%s: registered %v, a check %v, predicting paths %v", fqcn, ok, d.Check != nil, d.Manifest.PredictsPaths)
	}
	return d.Check
}

// locateOn reads where target resolves through the harness, as a method does.
func locateOn(t *testing.T, server *remoteexectest.Server, target string) wire.Location {
	t.Helper()
	conn, err := sdk.Connect(context.Background(), newRemoveContext(server), newRemoveDevice(server), removeParams(target, nil), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	loc, err := remotefile.Locate(context.Background(), conn, target)
	if err != nil {
		t.Fatalf("Locate(%s): %v", target, err)
	}
	return loc
}

// snapshotOf is the snapshot the engine would hand a method after paths were
// recorded in order: each path's state, and every identity read along them.
func snapshotOf(paths ...wire.PredictedPath) *wire.Predictions {
	out := &wire.Predictions{}
	seen := map[string]bool{}
	for _, p := range paths {
		out.Paths = append(out.Paths, wire.PredictedPath{Path: p.Path, State: p.State})
		for _, d := range p.Dirs {
			if !seen[d.Path] {
				seen[d.Path] = true
				out.Dirs = append(out.Dirs, d)
			}
		}
	}
	return out
}

func gone(l wire.Location) wire.PredictedPath {
	return wire.PredictedPath{Path: l.Path, State: wire.PredictedAbsent, Dirs: l.Dirs}
}

func made(l wire.Location) wire.PredictedPath {
	return wire.PredictedPath{Path: l.Path, State: wire.PredictedPresent, Dirs: l.Dirs}
}

// predictedRecord reads back the paths a check recorded.
func predictedRecord(t *testing.T, rc *removeContext) []wire.PredictedPath {
	t.Helper()
	paths, err := sdk.ParsePredicted(rc.stats[sdk.StatPredictedPaths])
	if err != nil {
		t.Fatalf("the check recorded %#v: %v", rc.stats[sdk.StatPredictedPaths], err)
	}
	return paths
}

// fullDirectory makes a directory holding a plain and a hidden file, which
// rmdir would refuse.
func fullDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "full")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plain", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// checkRemove runs file.remove's check of target with snap.
func checkRemove(t *testing.T, server *remoteexectest.Server, snap *wire.Predictions, target string) (collection.Result, *removeContext, error) {
	t.Helper()
	rc := &predictingContext{removeContext: newRemoveContext(server), snap: snap}
	result, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(target, nil))
	return result, rc.removeContext, err
}

// TestCheckRemove_ADirectoryWhoseEntriesAreAllPredictedGoneIsPredictedEmpty:
// the rollback shape (undo the copy inside, then the directory) predicts what
// the real run does, and the directory is recorded gone where it resolves.
func TestCheckRemove_ADirectoryWhoseEntriesAreAllPredictedGoneIsPredictedEmpty(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	snap := snapshotOf(gone(locateOn(t, server, filepath.Join(dir, "plain"))), gone(locateOn(t, server, filepath.Join(dir, ".hidden"))))

	result, rc, err := checkRemove(t, server, snap, dir)
	if err != nil || !result.Changed {
		t.Fatalf("check = %+v, %v; want the directory predicted removed", result, err)
	}
	want := locateOn(t, server, dir)
	if got := predictedRecord(t, rc); len(got) != 1 || got[0].Path != want.Path || got[0].State != wire.PredictedAbsent || len(got[0].Dirs) != len(want.Dirs) {
		t.Errorf("recorded %+v, want %s absent with its identities", got, want.Path)
	}
	assertRemoveStillThere(t, filepath.Join(dir, "plain"))
}

// TestCheckRemove_ADirectoryIsNotEmptyWhileAnythingIsLeft: an entry nothing
// predicted gone, or a path an earlier task predicted made inside (here
// through a link to the directory, which a key as written would miss), keeps
// the refusal the real run would make.
func TestCheckRemove_ADirectoryIsNotEmptyWhileAnythingIsLeft(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	plain, hidden := locateOn(t, server, filepath.Join(dir, "plain")), locateOn(t, server, filepath.Join(dir, ".hidden"))
	for name, snap := range map[string]*wire.Predictions{
		"an entry left":                  snapshotOf(gone(plain)),
		"no predictions":                 snapshotOf(),
		"a path made inside, via a link": snapshotOf(gone(plain), gone(hidden), made(locateOn(t, server, filepath.Join(link, "new")))),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := checkRemove(t, server, snap, dir)
			if err == nil || !strings.Contains(err.Error(), "not empty") {
				t.Errorf("check = %v, want the directory refused as not empty", err)
			}
		})
	}
}

// TestCheckRemove_CannotCheckWhatEarlierTasksChangedUnreadably: where earlier
// tasks made or removed the directory, or its entries cannot all be read,
// the device as it is now is not what the real run meets, and the honest
// answer is that the call cannot be checked.
func TestCheckRemove_CannotCheckWhatEarlierTasksChangedUnreadably(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	large := filepath.Join(t.TempDir(), "large")
	if err := os.Mkdir(large, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 1025 {
		if err := os.WriteFile(filepath.Join(large, strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		path string
		snap *wire.Predictions
	}{
		"made again":        {dir, snapshotOf(gone(locateOn(t, server, dir)), made(locateOn(t, server, dir)))},
		"too large to list": {large, snapshotOf(gone(locateOn(t, server, filepath.Join(large, "0"))))},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := checkRemove(t, server, tc.snap, tc.path)
			var cannot *collection.CannotCheckError
			if !errors.As(err, &cannot) {
				t.Errorf("check = %v, want CannotCheck", err)
			}
		})
	}
}

// TestCheckRemove_PredictedStateOfThePathItself: a path an earlier task
// removes has nothing left to remove; one an earlier task makes cannot be
// read yet.
func TestCheckRemove_PredictedStateOfThePathItself(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	target := filepath.Join(dir, "plain")
	result, rc, err := checkRemove(t, server, snapshotOf(gone(locateOn(t, server, dir))), target)
	if err != nil || result.Changed {
		t.Errorf("a path removed earlier: %+v, %v; want nothing to remove", result, err)
	}
	if _, ok := rc.stats[sdk.StatPredictedPaths]; ok {
		t.Error("a check that changes nothing recorded a path")
	}
	assertRemoveStillThere(t, target)

	missing := filepath.Join(dir, "later")
	_, _, err = checkRemove(t, server, snapshotOf(made(locateOn(t, server, missing))), missing)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Errorf("a path made earlier: %v, want CannotCheck", err)
	}
}

// TestCheckRemove_FailsWhenThePredictionCannotBeRecorded: a check whose
// prediction is lost would let a later task read the device wrongly, so it
// fails rather than succeed without it.
func TestCheckRemove_FailsWhenThePredictionCannotBeRecorded(t *testing.T) {
	server := startRemoveServer(t, -1)
	target := filepath.Join(t.TempDir(), "doomed")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rc := &predictingContext{removeContext: newRemoveContext(server), snap: snapshotOf()}
	rc.statErr = errRemoveStat{}
	if _, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(target, nil)); err == nil || !strings.Contains(err.Error(), "stat recording refused") {
		t.Errorf("check = %v, want the recording failure", err)
	}
	assertRemoveStillThere(t, target)
}

// TestChecks_RecordThePathTheyMakeWhereItResolves: each method that makes a
// path records it present after its check, at its physical path (here
// written through a link), only in a check that keeps predictions; a path
// written in a form that cannot be located (not cleaned) records the
// device's predictions unknown instead.
func TestChecks_RecordThePathTheyMakeWhereItResolves(t *testing.T) {
	server := startRemoveServer(t, -1)
	for fqcn, param := range map[string]string{
		"file.directory": "path",
		"file.touch":     "path",
		"file.copy":      "dest",
		"file.template":  "dest",
		"file.symlink":   "path",
	} {
		t.Run(fqcn, func(t *testing.T) {
			real := t.TempDir()
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			params := removeParams("", map[string]any{"content": "x", "src": "/nowhere"})
			delete(params, "path")
			if fqcn != "file.copy" && fqcn != "file.template" {
				delete(params, "content")
			}
			if fqcn != "file.symlink" {
				delete(params, "src")
			}
			run := func(target string, snap *wire.Predictions) *removeContext {
				t.Helper()
				params[param] = target
				rc := &predictingContext{removeContext: newRemoveContext(server), snap: snap}
				if _, err := checkOf(t, fqcn)(context.Background(), rc, newRemoveDevice(server), params); err != nil {
					t.Fatalf("check of %s: %v", target, err)
				}
				return rc.removeContext
			}

			rc := run(filepath.Join(link, "made"), snapshotOf())
			want := locateOn(t, server, filepath.Join(real, "made"))
			if got := predictedRecord(t, rc); len(got) != 1 || got[0].Path != want.Path || got[0].State != wire.PredictedPresent {
				t.Errorf("recorded %+v, want %s present", got, want.Path)
			}
			if _, err := os.Lstat(filepath.Join(real, "made")); !os.IsNotExist(err) {
				t.Error("a check made the path")
			}
			if rc := run(real+"/./unclean", snapshotOf()); rc.stats[sdk.StatPredictedPaths] != "unknown" {
				t.Errorf("an unclean path recorded %#v, want the device unknown", rc.stats[sdk.StatPredictedPaths])
			}
			if rc := run(filepath.Join(real, "made"), nil); rc.stats[sdk.StatPredictedPaths] != nil {
				t.Errorf("a check keeping no predictions recorded %#v", rc.stats[sdk.StatPredictedPaths])
			}
		})
	}
}
