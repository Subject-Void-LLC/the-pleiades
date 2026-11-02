// Tests of what this package's checks predict about paths and read of what
// earlier tasks predicted (Phase 46a), against the real SSH harness and a real
// /bin/sh, as remove_test.go's are: every effect is read off the filesystem.
package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// predictingContext is removeContext holding a snapshot of what earlier tasks
// in the check predicted, as the engine's context does.
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

func absentAt(paths ...string) *wire.Predictions {
	out := &wire.Predictions{}
	for _, p := range paths {
		out.Paths = append(out.Paths, wire.PredictedPath{Path: p, State: wire.PredictedAbsent})
	}
	return out
}

// TestCheckRemove_ADirectoryWhoseEntriesAreAllPredictedGoneIsPredictedEmpty:
// the rollback shape (undo the copy inside, then the directory) predicts what
// the real run does, and the directory is recorded gone for later tasks.
func TestCheckRemove_ADirectoryWhoseEntriesAreAllPredictedGoneIsPredictedEmpty(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	rc := &predictingContext{removeContext: newRemoveContext(server), snap: absentAt(filepath.Join(dir, "plain"), filepath.Join(dir, ".hidden"))}

	result, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(dir, nil))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !result.Changed {
		t.Error("the check did not predict the directory removed")
	}
	if got := predictedRecord(t, rc.removeContext); len(got) != 1 || got[0] != (wire.PredictedPath{Path: dir, State: wire.PredictedAbsent}) {
		t.Errorf("recorded %+v, want %s absent", got, dir)
	}
	assertRemoveStillThere(t, filepath.Join(dir, "plain"))
}

// TestCheckRemove_ADirectoryIsNotEmptyWhileAnythingIsLeft: an entry nothing
// predicted gone, or a path an earlier task predicted made inside, keeps the
// refusal the real run would make.
func TestCheckRemove_ADirectoryIsNotEmptyWhileAnythingIsLeft(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		snap *wire.Predictions
	}{
		"an entry left":       {dir, absentAt(filepath.Join(dir, "plain"))},
		"no predictions":      {dir, nil},
		"unknown predictions": {dir, &wire.Predictions{Unknown: true, Paths: absentAt(filepath.Join(dir, "plain"), filepath.Join(dir, ".hidden")).Paths}},
		"a path made inside":  {empty, &wire.Predictions{Paths: []wire.PredictedPath{{Path: filepath.Join(empty, "new"), State: wire.PredictedPresent}}}},
	} {
		t.Run(name, func(t *testing.T) {
			rc := &predictingContext{removeContext: newRemoveContext(server), snap: tc.snap}
			_, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(tc.path, nil))
			if err == nil || !strings.Contains(err.Error(), "not empty") {
				t.Errorf("check = %v, want the directory refused as not empty", err)
			}
		})
	}
}

// TestCheckRemove_APathPredictedGoneHasNothingToRemove: an earlier task of
// the check removes it first, so this one changes nothing, as a real run
// finding it gone would.
func TestCheckRemove_APathPredictedGoneHasNothingToRemove(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := fullDirectory(t)
	target := filepath.Join(dir, "plain")
	rc := &predictingContext{removeContext: newRemoveContext(server), snap: absentAt(dir)}
	result, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(target, nil))
	if err != nil || result.Changed {
		t.Errorf("check = %+v, %v; want nothing to remove", result, err)
	}
	assertRemoveStillThere(t, target)
}

// TestChecks_RecordThePathTheyMake: each method that makes a path records it
// present after its check, and one written in a form the ledger cannot key
// (not cleaned) records the device's predictions unknown instead.
func TestChecks_RecordThePathTheyMake(t *testing.T) {
	server := startRemoveServer(t, -1)
	for fqcn, param := range map[string]string{
		"file.directory": "path",
		"file.touch":     "path",
		"file.copy":      "dest",
		"file.template":  "dest",
		"file.symlink":   "path",
	} {
		t.Run(fqcn, func(t *testing.T) {
			root := t.TempDir()
			params := removeParams("", map[string]any{"content": "x", "src": "/nowhere"})
			delete(params, "path")
			if fqcn != "file.copy" && fqcn != "file.template" {
				delete(params, "content")
			}
			if fqcn != "file.symlink" {
				delete(params, "src")
			}
			made := filepath.Join(root, "made")
			params[param] = made
			rc := newRemoveContext(server)
			if _, err := checkOf(t, fqcn)(context.Background(), rc, newRemoveDevice(server), params); err != nil {
				t.Fatalf("check: %v", err)
			}
			if got := predictedRecord(t, rc); len(got) != 1 || got[0] != (wire.PredictedPath{Path: made, State: wire.PredictedPresent}) {
				t.Errorf("recorded %+v, want %s present", got, made)
			}
			if _, err := os.Lstat(made); !os.IsNotExist(err) {
				t.Errorf("a check made %s", made)
			}

			params[param] = root + "/./unclean"
			rc = newRemoveContext(server)
			if _, err := checkOf(t, fqcn)(context.Background(), rc, newRemoveDevice(server), params); err != nil {
				t.Fatalf("check of an unclean path: %v", err)
			}
			if rc.stats[sdk.StatPredictedPaths] != "unknown" {
				t.Errorf("an unclean path recorded %#v, want the device unknown", rc.stats[sdk.StatPredictedPaths])
			}
		})
	}
}

// TestCheckRemove_ADirectoryTooLargeToListIsNotPredictedEmpty: past the
// entries a check lists, a directory keeps the refusal, even when one entry
// was predicted gone.
func TestCheckRemove_ADirectoryTooLargeToListIsNotPredictedEmpty(t *testing.T) {
	server := startRemoveServer(t, -1)
	dir := filepath.Join(t.TempDir(), "large")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 1025 {
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rc := &predictingContext{removeContext: newRemoveContext(server), snap: absentAt(filepath.Join(dir, "0"))}
	_, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(dir, nil))
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("check = %v, want the directory refused as not empty", err)
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
	rc := newRemoveContext(server)
	rc.statErr = errRemoveStat{}
	if _, err := checkOf(t, "file.remove")(context.Background(), rc, newRemoveDevice(server), removeParams(target, nil)); err == nil || !strings.Contains(err.Error(), "stat recording refused") {
		t.Errorf("check = %v, want the recording failure", err)
	}
	assertRemoveStillThere(t, target)
}
