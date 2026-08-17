package block_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file/block"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestRemove_Registered proves the method registered itself as
// implemented and claims the reversibility that makes it unusual.
//
// Deleting text is normally the least reversible thing a method can do,
// so the Notes are pinned here for the same reason the flag is: the
// claim is only true because the markers say which lines belonged to the
// block, and a Notes field that lost that sentence would leave an
// operator with a promise and no reason to believe it.
func TestRemove_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.block.remove")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.block.remove")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, but this method captures the block it deletes and emits a real inverse")
	}
	if !strings.Contains(d.Manifest.Reversibility.Notes, "file.line") {
		t.Errorf("Reversibility.Notes = %q, want it to say why the markers make this stronger than file.line", d.Manifest.Reversibility.Notes)
	}
}

// TestRemove_RefusesAMissingPath proves the one required parameter is
// really required, and that the refusal happens before any connection.
func TestRemove_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"marker": "# {mark} PLEIADES"},
		{"path": ""},
		{"path": nil},
	} {
		_, err := block.Remove(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestRemove_RefusesAParameterThatIsNotText proves this method reads its
// parameters as strictly as its sibling does.
//
// It matters more here. A marker that arrived as something other than
// text and was treated as absent would send this method looking for the
// DEFAULT markers, and if it found them it would delete a block the task
// never named.
func TestRemove_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "path", params: map[string]any{"path": 42}},
		{name: "marker", params: map[string]any{"path": "/etc/hosts", "marker": 42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := block.Remove(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.name+" is int, not text") {
				t.Errorf("error = %q, want it to name %q and its type", err, tt.name)
			}
		})
	}
}

// TestRemove_RefusesAMarkerItCouldNotFind proves this method runs the
// same marker checks its sibling does.
//
// The check lives in one shared place and each method has to call it;
// a request reader that forgot to would send a marker with no
// placeholder to a device, where the two ends of the block are the same
// line and any deletion is a guess.
func TestRemove_RefusesAMarkerItCouldNotFind(t *testing.T) {
	params := map[string]any{"path": "/etc/hosts", "marker": "# PLEIADES BLOCK"}

	_, err := block.Remove(context.Background(), nil, nil, params)
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "{mark}") {
		t.Errorf("error = %q, want it to name the missing placeholder", err)
	}
}

// TestRemove_TakesTheBlockAndItsMarkersOut is this method's main path.
//
// The markers go with the block, which is the only sensible reading of a
// removal: a pair of marker lines with nothing between them is not a
// removed block, it is an empty one that the next file.block.set would
// find and fill. The lines above and below it have to survive exactly.
func TestRemove_TakesTheBlockAndItsMarkersOut(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha\n"+blockTestBegin+"\none\ntwo\n"+blockTestEnd+"\nomega\n")

	result, err := block.Remove(context.Background(), rc, newBlockTarget(server), blockParams(path, nil))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Error("a run that removed a block reported no change")
	}
	if got := blockContents(t, path); got != "alpha\nomega\n" {
		t.Errorf("the file holds %q, want %q", got, "alpha\nomega\n")
	}

	if got := rc.stats["present"]; got != false {
		t.Errorf("present stat = %v, want false once the block is gone", got)
	}
	if got := rc.stats["block"]; got != "" {
		t.Errorf("block stat = %v, want it empty once the block is gone", got)
	}
	if got := blockDiffHalf(t, rc, "before")["block"]; got != "one\ntwo" {
		t.Errorf("diff before block = %v, want the deleted text: this is the only record of it", got)
	}

	// The inverse is the whole reason this method reads before it writes:
	// once the file is written back, the deleted lines exist nowhere else.
	fqcn, params, description := blockInverse(t, rc)
	if fqcn != "file.block.set" {
		t.Errorf("inverse fqcn = %q, want file.block.set", fqcn)
	}
	if params["block"] != "one\ntwo" {
		t.Errorf("inverse block = %v, want the body this run deleted", params["block"])
	}
	if params["path"] != path {
		t.Errorf("inverse path = %v, want %q", params["path"], path)
	}
	// The block was in the middle of the file and file.block.set appends,
	// so the undo cannot put it back where it was. Saying so is the rule
	// for an inverse that restores part of what it took.
	if !strings.Contains(description, "lines 2 to 5") {
		t.Errorf("inverse description = %q, want it to name where the block was", description)
	}
	if !strings.Contains(description, "comes back at the end") {
		t.Errorf("inverse description = %q, want it to admit the undo does not restore the position", description)
	}
}

// TestRemove_ConvergedRunReportsNoChangeAndWritesNothing proves a file
// with no markers is left completely alone.
//
// The session budget is the assertion. A converged run needs exactly two
// sessions, the stat and the read; a run that went on to write would ask
// for a third and be refused, so a method that rewrote every file it was
// pointed at fails here rather than passing quietly while it changed
// modification times across a fleet.
func TestRemove_ConvergedRunReportsNoChangeAndWritesNothing(t *testing.T) {
	server := startBlockServerWithSessionBudget(t, 2)
	rc := newBlockContext(server)
	contents := "alpha\nomega\n"
	path := blockFile(t, contents)

	result, err := block.Remove(context.Background(), rc, newBlockTarget(server), blockParams(path, nil))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Error("a file with no managed block reported a change")
	}
	if got := blockContents(t, path); got != contents {
		t.Errorf("the file holds %q, want it untouched at %q", got, contents)
	}

	before := blockDiffHalf(t, rc, "before")
	after := blockDiffHalf(t, rc, "after")
	if before["present"] != false || after["present"] != false {
		t.Errorf("a converged run recorded before %v and after %v, want both saying no block was there", before, after)
	}
	blockNoInverse(t, rc)
}

// TestRemove_LeavesAnEmptyFileWhenTheBlockWasEverything proves a file
// that held nothing but the block ends up empty rather than holding a
// stray blank line.
func TestRemove_LeavesAnEmptyFileWhenTheBlockWasEverything(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, blockTestBegin+"\none\n"+blockTestEnd+"\n")

	if _, err := block.Remove(context.Background(), rc, newBlockTarget(server), blockParams(path, nil)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := blockContents(t, path); got != "" {
		t.Errorf("the file holds %q, want it empty", got)
	}

	// The block ran to the end of the file, so there is no position to
	// admit losing: the undo appends it back exactly where it was.
	if _, _, description := blockInverse(t, rc); strings.Contains(description, "comes back at the end rather than") {
		t.Errorf("inverse description = %q, want no position caveat for a block that was already at the end", description)
	}
}

// TestRemove_ClosesTheFileWhenTheBlockRanToTheEnd covers the awkward
// combination: a block at the end of a file whose last line had no
// newline.
//
// Every line before the block was terminated by a newline, because
// something followed it, so cutting the block leaves a file that DOES
// end with a newline where the original did not. That is a change to a
// line the task was not asked about, and the emitted inverse says so
// rather than leaving somebody comparing checksums after a rollback.
func TestRemove_ClosesTheFileWhenTheBlockRanToTheEnd(t *testing.T) {
	server := startBlockServer(t)
	rc := newBlockContext(server)
	path := blockFile(t, "alpha\n"+blockTestBegin+"\none\n"+blockTestEnd)

	if _, err := block.Remove(context.Background(), rc, newBlockTarget(server), blockParams(path, nil)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := blockContents(t, path); got != "alpha\n" {
		t.Errorf("the file holds %q, want %q", got, "alpha\n")
	}

	_, _, description := blockInverse(t, rc)
	if !strings.Contains(description, "did not end with a newline") {
		t.Errorf("inverse description = %q, want it to say the undo leaves the added newline behind", description)
	}
}

// TestRemove_PutsTheFilesModeBack proves the removal repairs the side
// effect of how a file is written here: a rewrite lands as mode 0600,
// whatever the file was before.
func TestRemove_PutsTheFilesModeBack(t *testing.T) {
	server := startBlockServer(t)
	path := blockFile(t, "alpha\n"+blockTestBegin+"\none\n"+blockTestEnd+"\n")

	if _, err := block.Remove(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, nil)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := blockMode(t, path); got != "0644" {
		t.Errorf("the file is %s on disk, want the 0644 it started as", got)
	}
}

// TestRemove_UsesTheMarkerItIsGiven proves the custom markers reach the
// search, and that a block written with them is the one removed.
//
// The file also holds a DEFAULT-marked block, which must survive. A
// method that ignored the marker parameters would delete that one
// instead and this test would catch it, where a file holding a single
// block could not.
func TestRemove_UsesTheMarkerItIsGiven(t *testing.T) {
	server := startBlockServer(t)
	path := blockFile(t, blockTestBegin+"\nkeep me\n"+blockTestEnd+"\n; OPEN PLEIADES PROXY\ndrop me\n; SHUT PLEIADES PROXY\n")

	if _, err := block.Remove(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, map[string]any{
		"marker":       "; {mark} PLEIADES PROXY",
		"marker_begin": "OPEN",
		"marker_end":   "SHUT",
	})); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	want := blockTestBegin + "\nkeep me\n" + blockTestEnd + "\n"
	if got := blockContents(t, path); got != want {
		t.Errorf("the file holds\n%q\nwant\n%q", got, want)
	}
}

// TestRemove_RefusesAnAbsentPath proves a path that is not there is a
// refusal rather than a quiet success.
//
// "There is no block in a file that does not exist" is true and useless.
// A path that is not there is far more often a typo, and a task that
// reported success for one would hide the typo in a green run.
func TestRemove_RefusesAnAbsentPath(t *testing.T) {
	server := startBlockServer(t)
	path := filepath.Join(t.TempDir(), "not-there")

	_, err := block.Remove(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, nil))
	if err == nil {
		t.Fatal("an absent path was reported as a file with no block")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to say the path is not there", err)
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Errorf("os.Lstat(%s) = %v, want the path still absent", path, statErr)
	}
}

// TestRemove_RefusesMarkersThatDoNotPairUp proves an ambiguous file is
// an error here too, and this is where it matters most: the guess this
// refusal replaces would be a deletion.
func TestRemove_RefusesMarkersThatDoNotPairUp(t *testing.T) {
	server := startBlockServer(t)
	contents := blockTestBegin + "\none\n" + blockTestEnd + "\ntwo\n" + blockTestEnd + "\n"
	path := blockFile(t, contents)

	_, err := block.Remove(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, nil))
	if err == nil {
		t.Fatal("a file with two end markers was accepted, so the method guessed which lines to delete")
	}
	if !strings.Contains(err.Error(), "found 2 copies of the end marker") {
		t.Errorf("error = %q, want it to say what it found", err)
	}
	if got := blockContents(t, path); got != contents {
		t.Errorf("the file holds\n%q\nwant it untouched at\n%q", got, contents)
	}
}

// TestRemove_RefusesAnUnreachableDevice covers the connect failure path.
func TestRemove_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := block.Remove(context.Background(), newBlockContext(blockServer{}), newBlockUnreachable(), map[string]any{
		"path": "/etc/hosts",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestRemove_ReportsAFailureAtEveryStep walks the session budget along
// the six commands a removing run sends.
//
// The read failure is the one worth naming. If a failed read were
// treated as a file with no markers, this method would report converged
// and leave a block in place that a runbook believes it deleted, which
// is a wrong answer about the device rather than a failure to reach it.
func TestRemove_ReportsAFailureAtEveryStep(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		wants  string
	}{
		{name: "the stat that reads what is at the path", budget: 0, wants: "stat "},
		{name: "the read of the file", budget: 1, wants: "read "},
		{name: "the write", budget: 2, wants: "write "},
		{name: "the stat that reads back what the write left", budget: 3, wants: "could not be given back its previous mode"},
		{name: "the chmod that puts the mode back", budget: 4, wants: "could not be given back its previous mode"},
		{name: "the read that observes the result", budget: 5, wants: "read "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startBlockServerWithSessionBudget(t, tt.budget)
			path := blockFile(t, "alpha\n"+blockTestBegin+"\none\n"+blockTestEnd+"\n")

			_, err := block.Remove(context.Background(), newBlockContext(server), newBlockTarget(server), blockParams(path, nil))
			if err == nil {
				t.Fatal("a failed command was reported as success")
			}
			if !strings.Contains(err.Error(), "open session") {
				t.Errorf("error = %q, want it to say the session could not be opened", err)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("error = %q, want it to name the step that failed (%q)", err, tt.wants)
			}
		})
	}
}

// TestRemove_RecordFailuresAreReported covers the three separate
// recording call sites, each of which an earlier one could hide.
func TestRemove_RecordFailuresAreReported(t *testing.T) {
	for _, key := range []string{"diff", "path", "inverse"} {
		t.Run(key, func(t *testing.T) {
			server := startBlockServer(t)
			rc := newBlockContext(server)
			rc.failOn = key

			path := blockFile(t, "alpha\n"+blockTestBegin+"\none\n"+blockTestEnd+"\n")
			_, err := block.Remove(context.Background(), rc, newBlockTarget(server), blockParams(path, nil))
			if err == nil {
				t.Fatalf("a failure to record %q was swallowed", key)
			}
			if !errors.Is(err, errBlockStat) {
				t.Errorf("error = %q, want it to carry what recording returned", err)
			}
		})
	}
}
