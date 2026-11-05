// Tests of what file.remove's check predicts and reads of what earlier tasks
// predicted removed (Phase 46a), against the real SSH harness and a real
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
	return checkRemoveWith(t, server, snap, target, nil)
}

// checkRemoveWith is checkRemove with extra params.
func checkRemoveWith(t *testing.T, server *remoteexectest.Server, snap *wire.Predictions, target string, extra map[string]any) (collection.Result, *removeContext, error) {
	t.Helper()
	rc := &predictingContext{removeContext: newRemoveContext(server), snap: snap}
	result, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(target, extra))
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
// predicted gone keeps the refusal the real run would make, and so does an
// entry recorded gone under another identity for the directory (a second
// spelling of it), which the predictions do not vouch for.
func TestCheckRemove_ADirectoryIsNotEmptyWhileAnythingIsLeft(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	plain, hidden := locateOn(t, server, filepath.Join(dir, "plain")), locateOn(t, server, filepath.Join(dir, ".hidden"))
	other := snapshotOf(gone(plain), gone(hidden))
	for i := range other.Dirs {
		if other.Dirs[i].Path == dir {
			other.Dirs[i].ID = "9:9"
		}
	}
	for name, snap := range map[string]*wire.Predictions{
		"an entry left":    snapshotOf(gone(plain)),
		"no predictions":   snapshotOf(),
		"another identity": other,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := checkRemove(t, server, snap, dir)
			if err == nil || !strings.Contains(err.Error(), "not empty") {
				t.Errorf("check = %v, want the directory refused as not empty", err)
			}
		})
	}
}

// TestCheckRemove_CannotCheckADirectoryItCannotList: when earlier tasks removed
// entries of a directory too large to list, whether the real run finds it
// empty cannot be read, and the honest answer is that it cannot be checked.
func TestCheckRemove_CannotCheckADirectoryItCannotList(t *testing.T) {
	server := startRemoveServer(t, -1)
	large := filepath.Join(tempDir(t), "large")
	if err := os.Mkdir(large, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 1025 {
		if err := os.WriteFile(filepath.Join(large, strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := checkRemove(t, server, snapshotOf(gone(locateOn(t, server, filepath.Join(large, "0")))), large)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Errorf("check = %v, want CannotCheck", err)
	}
}

// TestCheckRemove_APathPredictedGoneHasNothingToRemove: a path an earlier task
// removes (itself or a directory holding it) has nothing left to remove, and
// the check records that it removes nothing.
func TestCheckRemove_APathPredictedGoneHasNothingToRemove(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	target := filepath.Join(dir, "plain")
	result, rc, err := checkRemove(t, server, snapshotOf(gone(locateOn(t, server, dir))), target)
	if err != nil || result.Changed {
		t.Errorf("a path removed earlier: %+v, %v; want nothing to remove", result, err)
	}
	if got := predictedRecord(t, rc); len(got) != 0 {
		t.Errorf("a check that removes nothing recorded %+v, want an empty record", got)
	}
	assertRemoveStillThere(t, target)
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

// TestCheckRemove_APathThroughALinkDropsThePredictions: where a link leads in
// the real run depends on what earlier tasks did to it, so a path that
// resolves anywhere but where it is written records the predictions unknown,
// whether the check would remove something or nothing; a path with nothing to
// remove records an empty list; and a check keeping no predictions records
// nothing at all.
func TestCheckRemove_APathThroughALinkDropsThePredictions(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	link := filepath.Join(tempDir(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(link, "plain"), filepath.Join(link, "missing"), dir + "/./plain"} {
		_, rc, err := checkRemove(t, server, snapshotOf(), target)
		if err != nil || rc.stats[sdk.StatPredictedPaths] != "unknown" {
			t.Errorf("%s: %v, recorded %#v; want the predictions unknown", target, err, rc.stats[sdk.StatPredictedPaths])
		}
	}
	if _, rc, err := checkRemove(t, server, snapshotOf(), filepath.Join(dir, "missing")); err != nil || rc.stats[sdk.StatPredictedPaths] == nil || rc.stats[sdk.StatPredictedPaths] == "unknown" {
		t.Errorf("a path with nothing to remove: %v, recorded %#v; want an empty record", err, rc.stats[sdk.StatPredictedPaths])
	}
	if _, rc, err := checkRemove(t, server, nil, filepath.Join(dir, "plain")); err != nil || rc.stats[sdk.StatPredictedPaths] != nil {
		t.Errorf("a check keeping no predictions: %v, recorded %#v", err, rc.stats[sdk.StatPredictedPaths])
	}
}

// TestMethodsThatMakePathsDoNotPredict: a claim that a path is gone is what
// making one makes false, so no method that makes a path declares it records
// what it changes, and a task by one drops the run's predictions.
func TestMethodsThatMakePathsDoNotPredict(t *testing.T) {
	for _, fqcn := range []string{"file.copy", "file.template", "file.directory", "file.touch", "file.symlink"} {
		if d, ok := collection.Lookup(fqcn); !ok || d.Manifest.PredictsPaths {
			t.Errorf("%s declares PredictsPaths (registered %v)", fqcn, ok)
		}
	}
}

// refusingPredictions is predictingContext refusing only the prediction stat,
// so the diff a check records before it succeeds.
type refusingPredictions struct{ *predictingContext }

// SetStat refuses sdk.StatPredictedPaths.
func (c refusingPredictions) SetStat(key string, value any) error {
	if key == sdk.StatPredictedPaths {
		return errRemoveStat{}
	}
	return c.predictingContext.SetStat(key, value)
}

// TestCheckRemove_FailsWhenRemovingNothingCannotBeRecorded: a check that
// removes nothing still records so, and fails when it cannot, rather than
// leave the engine to read silence as a declaration kept.
func TestCheckRemove_FailsWhenRemovingNothingCannotBeRecorded(t *testing.T) {
	server := startRemoveServer(t, -1)
	rc := refusingPredictions{&predictingContext{removeContext: newRemoveContext(server), snap: snapshotOf()}}
	_, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(filepath.Join(tempDir(t), "missing"), nil))
	if err == nil || !strings.Contains(err.Error(), "stat recording refused") {
		t.Errorf("check = %v, want the recording failure", err)
	}
}

// TestCheckRemove_RecordsOnlyARemovalTheRealRunCanMake: a check cannot see a
// refused removal coming, so a removal in a directory that does not allow it,
// or of a whole tree, records the predictions unknown rather than gone; and a
// directory found empty only through earlier predictions, in a parent that
// does not allow its removal, cannot be checked. Asked to warn
// (check_forbidden: warn), a removal the parent does not allow is warned
// about in the check's own result; by default it is refused
// (TestCheckRemove_AForbiddenRemovalIsRefusedByDefault).
func TestCheckRemove_RecordsOnlyARemovalTheRealRunCanMake(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes from any directory, so none refuses it here")
	}
	server := startRemoveServer(t, -1)
	sealed := filepath.Join(tempDir(t), "sealed")
	dir := filepath.Join(sealed, "full")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

	// A file in a directory that allows its removal is recorded gone.
	_, rc, err := checkRemove(t, server, snapshotOf(), filepath.Join(dir, "f"))
	if got := predictedRecord(t, rc); err != nil || len(got) != 1 {
		t.Errorf("a removable file: %v, recorded %+v", err, got)
	}
	if w, ok := rc.stats[sdk.StatWarnings]; ok {
		t.Errorf("a removable file was warned about: %#v", w)
	}
	// A directory in one that does not is not.
	empty := filepath.Join(sealed, "empty")
	if err := os.Chmod(sealed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	warn := map[string]any{"check_forbidden": "warn"}
	_, rc, err = checkRemoveWith(t, server, snapshotOf(), empty, warn)
	if err != nil || rc.stats[sdk.StatPredictedPaths] != "unknown" {
		t.Errorf("a directory its parent does not let go: %v, recorded %#v; want unknown", err, rc.stats[sdk.StatPredictedPaths])
	}
	// And the check says so itself: the real run will likely be refused.
	if warnings, _ := rc.stats[sdk.StatWarnings].([]string); len(warnings) != 1 || !strings.Contains(warnings[0], "may not remove "+empty) {
		t.Errorf("the check's warnings = %#v, want one saying the login may not remove %s", rc.stats[sdk.StatWarnings], empty)
	}
	// Nor is one found empty only through predictions: that cannot be checked.
	_, _, err = checkRemoveWith(t, server, snapshotOf(gone(locateOn(t, server, filepath.Join(dir, "f")))), dir, warn)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Errorf("a directory empty only through predictions, in a parent that does not let it go: %v, want CannotCheck", err)
	}
	// A whole tree is not recorded, wherever it is.
	tree := fullDirectory(t)
	params := removeParams(tree, map[string]any{"recurse": true})
	prc := &predictingContext{removeContext: newRemoveContext(server), snap: snapshotOf()}
	if _, err := checkOf(t, "file.remove")(context.Background(), prc, newRemoveDevice(server), params); err != nil || prc.stats[sdk.StatPredictedPaths] != "unknown" {
		t.Errorf("a whole tree: %v, recorded %#v; want unknown", err, prc.stats[sdk.StatPredictedPaths])
	}
}

// TestCheckRemove_AnUnansweredRemovalQuestionRecordsUnknown: when the device
// cannot be asked whether the removal is allowed (the session after the stat
// and the location is refused), the removal is not recorded gone.
func TestCheckRemove_AnUnansweredRemovalQuestionRecordsUnknown(t *testing.T) {
	server := startRemoveServer(t, 2)
	target := filepath.Join(tempDir(t), "doomed")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result, rc, err := checkRemove(t, server, snapshotOf(), target)
	if err != nil || !result.Changed || rc.stats[sdk.StatPredictedPaths] != "unknown" {
		t.Errorf("check = %+v, %v, recorded %#v; want a predicted removal recorded unknown", result, err, rc.stats[sdk.StatPredictedPaths])
	}
}

// TestCheckRemove_AForbiddenRemovalIsRefusedByDefault: a check of a removal
// the device's permissions forbid this login fails, naming the path and the
// way to ask for a warning instead (owner decision 2026-11-04), records no
// prediction, and a removal they allow is untouched by the rule. A value
// that is neither refuse nor warn is refused before the device is reached.
func TestCheckRemove_AForbiddenRemovalIsRefusedByDefault(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes from any directory, so none refuses it here")
	}
	server := startRemoveServer(t, -1)
	sealed := filepath.Join(tempDir(t), "sealed")
	empty := filepath.Join(sealed, "empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })

	for name, extra := range map[string]map[string]any{"by default": nil, "asked to refuse": {"check_forbidden": "refuse"}} {
		t.Run(name, func(t *testing.T) {
			_, rc, err := checkRemoveWith(t, server, snapshotOf(), empty, extra)
			if err == nil || !strings.Contains(err.Error(), "may not remove "+empty) || !strings.Contains(err.Error(), "check_forbidden: warn") {
				t.Fatalf("check = %v, want a refusal naming %s and check_forbidden: warn", err, empty)
			}
			if _, ok := rc.stats[sdk.StatPredictedPaths]; ok {
				t.Errorf("a refused check recorded predictions: %#v", rc.stats[sdk.StatPredictedPaths])
			}
		})
	}
	// The control: a file in a directory that allows its removal is checked
	// as before, under the default.
	allowed := filepath.Join(tempDir(t), "f")
	if err := os.WriteFile(allowed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := checkRemove(t, server, snapshotOf(), allowed); err != nil {
		t.Fatalf("a removable file under the default: %v", err)
	}
	if _, _, err := checkRemoveWith(t, server, snapshotOf(), allowed, map[string]any{"check_forbidden": "maybe"}); err == nil || !strings.Contains(err.Error(), "check_forbidden must be refuse or warn") {
		t.Fatalf("a value that is neither: %v, want it refused", err)
	}
}
