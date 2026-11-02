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

// tempDir is t.TempDir resolved to its physical path: a check keeps
// predictions only for a path that resolves where it is written, and macOS's
// temporary directory sits under a link.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// fullDirectory makes a directory holding a plain and a hidden file, which
// rmdir would refuse.
func fullDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(tempDir(t), "full")
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
	link := filepath.Join(tempDir(t), "link")
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

// TestCheckRemove_ADirectoryMadeFreshHoldsOnlyWhatWasMadeInIt: a directory
// an earlier task removed and made again is empty in the real run whatever the
// device holds now, unless something was made in it since.
func TestCheckRemove_ADirectoryMadeFreshHoldsOnlyWhatWasMadeInIt(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	loc := locateOn(t, server, dir)
	fresh := wire.PredictedPath{Path: loc.Path, State: wire.PredictedFresh, Dirs: loc.Dirs}
	if result, _, err := checkRemove(t, server, snapshotOf(fresh), dir); err != nil || !result.Changed {
		t.Errorf("a directory made fresh: %+v, %v; want it predicted removed", result, err)
	}
	if _, _, err := checkRemove(t, server, snapshotOf(fresh, made(locateOn(t, server, filepath.Join(dir, "new")))), dir); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a directory made fresh with a path made in it: %v, want it refused as not empty", err)
	}
}

// TestCheckRemove_AConvergedDirectoryIsReadAsTheDeviceHoldsIt: a directory an
// earlier task only made sure of (it was there) holds what the device holds,
// so its removal is refused as the real run refuses it, and a path missing
// under it is missing.
func TestCheckRemove_AConvergedDirectoryIsReadAsTheDeviceHoldsIt(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	snap := snapshotOf(made(locateOn(t, server, dir)))
	if _, _, err := checkRemove(t, server, snap, dir); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a converged directory with files: %v, want it refused as not empty", err)
	}
	if result, _, err := checkRemove(t, server, snap, filepath.Join(dir, "missing")); err != nil || result.Changed {
		t.Errorf("a missing path under a converged directory: %+v, %v; want nothing to remove", result, err)
	}
}

// TestCheckRemove_CannotCheckWhatThePredictionsCannotVouchFor: a directory an
// earlier task read under another identity (replaced, or another spelling),
// or one whose entries cannot all be read, is not what the device as it is
// shows, and the honest answer is that the call cannot be checked.
func TestCheckRemove_CannotCheckWhatThePredictionsCannotVouchFor(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	large := filepath.Join(tempDir(t), "large")
	if err := os.Mkdir(large, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 1025 {
		if err := os.WriteFile(filepath.Join(large, strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := snapshotOf(gone(locateOn(t, server, filepath.Join(dir, "plain"))))
	other.Dirs = append(other.Dirs[:0:0], other.Dirs...)
	for i := range other.Dirs {
		if other.Dirs[i].Path == dir {
			other.Dirs[i].ID = "9:9"
		}
	}
	for name, tc := range map[string]struct {
		path string
		snap *wire.Predictions
	}{
		"another identity":  {dir, other},
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

// TestCheckRemove_APathThroughALinkDropsThePredictions: where a link leads in
// the real run depends on what earlier tasks did to it, so a path that
// resolves anywhere but where it is written records the device's predictions
// unknown, whether the check would remove something or nothing.
func TestCheckRemove_APathThroughALinkDropsThePredictions(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	link := filepath.Join(tempDir(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(link, "plain"), filepath.Join(link, "missing")} {
		_, rc, err := checkRemove(t, server, snapshotOf(), target)
		if err != nil || rc.stats[sdk.StatPredictedPaths] != "unknown" {
			t.Errorf("%s: %v, recorded %#v; want the device unknown", target, err, rc.stats[sdk.StatPredictedPaths])
		}
	}
	if _, rc, err := checkRemove(t, server, snapshotOf(), filepath.Join(dir, "missing")); err != nil || rc.stats[sdk.StatPredictedPaths] == nil || rc.stats[sdk.StatPredictedPaths] == "unknown" {
		t.Errorf("a path with nothing to remove: %v, recorded %#v; want an empty record", err, rc.stats[sdk.StatPredictedPaths])
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
	if got := predictedRecord(t, rc); len(got) != 0 {
		t.Errorf("a check that changes nothing recorded %+v, want an empty record", got)
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
	target := filepath.Join(tempDir(t), "doomed")
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
// path records it present after its check, only in a check that keeps
// predictions; a path that resolves elsewhere than written (through a link)
// or cannot be located (not cleaned) records the device's predictions unknown
// instead. file.symlink does not declare it: a link it makes changes where
// later paths lead.
func TestChecks_RecordThePathTheyMakeWhereItResolves(t *testing.T) {
	server := startRemoveServer(t, -1)
	for fqcn, param := range map[string]string{
		"file.directory": "path",
		"file.touch":     "path",
		"file.copy":      "dest",
		"file.template":  "dest",
	} {
		t.Run(fqcn, func(t *testing.T) {
			real := tempDir(t)
			link := filepath.Join(tempDir(t), "link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			params := removeParams("", map[string]any{"content": "x"})
			delete(params, "path")
			if fqcn != "file.copy" && fqcn != "file.template" {
				delete(params, "content")
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

			rc := run(filepath.Join(real, "made"), snapshotOf())
			want := locateOn(t, server, filepath.Join(real, "made"))
			if got := predictedRecord(t, rc); len(got) != 1 || got[0].Path != want.Path || got[0].State != wire.PredictedPresent {
				t.Errorf("recorded %+v, want %s present", got, want.Path)
			}
			if rc := run(filepath.Join(link, "made"), snapshotOf()); rc.stats[sdk.StatPredictedPaths] != "unknown" {
				t.Errorf("a path through a link recorded %#v, want the device unknown", rc.stats[sdk.StatPredictedPaths])
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

func TestSymlink_DoesNotPredictPaths(t *testing.T) {
	if d, ok := collection.Lookup("file.symlink"); !ok || d.Manifest.PredictsPaths {
		t.Error("file.symlink declares PredictsPaths, though a link it makes changes where later paths lead")
	}
}
