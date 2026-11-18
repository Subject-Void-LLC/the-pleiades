// Package journal: the runs a rollback plans against, which leave out a run
// still live beside it.
//
// A rollback holds the project's run lock exclusive (runlock.go), and every
// run that takes the lock takes it shared, so none of those can be live
// while the rollback plans. A run that takes no lock can: a read-only adhoc
// call (cmd/pleiades), which an adhoc poller runs over and over while a
// change is rolled back under it. Such a run changes nothing a rollback
// undoes, and its journal may be half written as it is read, so a rollback
// passes over it rather than fail reading it or count it as a later run.
package journal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ListRunsForRollback is ListRuns less every run still live: its run marker
// (clock_live.go) held by the process running it. Call it holding the run
// lock exclusive, which is what makes a live run one that takes no lock.
// Where a lock cannot tell a live process from a dead one (activeLocks), it
// is ListRuns.
func ListRunsForRollback(root string) ([]RunJournal, error) {
	all, err := listRunIDs(root)
	if err != nil {
		return nil, err
	}
	var runs []RunJournal
	for _, runID := range all {
		if RunLive(root, runID) {
			continue
		}
		run, err := ReadRun(root, runID)
		if err != nil {
			return nil, fmt.Errorf("run %s: %w", runID, err)
		}
		runs = append(runs, run)
	}
	sortRuns(runs)
	return runs, nil
}

// RunLive reports whether runID is still running: its journal's first entry
// names a start tick whose run marker its process holds. A run with no
// journal yet, no tick, or no marker held is not live by this answer.
func RunLive(root, runID string) bool {
	if !activeLocks {
		return false
	}
	name, err := fileNameFor(runID)
	if err != nil {
		return false
	}
	dir := filepath.Join(root, pleiadesDirName)
	tick := firstEntryTick(filepath.Join(dir, journalDirName, name))
	if tick == 0 {
		return false
	}
	return heldByLiveRun(filepath.Join(dir, liveDirName, strconv.FormatUint(tick, 10)+liveExtension))
}

// listRunIDs returns the run id of every journal in the project at root, as
// ListRuns finds them.
func listRunIDs(root string) ([]string, error) {
	dir := filepath.Join(root, pleiadesDirName, journalDirName)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err := checkJournalDir(dir); err != nil {
		return nil, err
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", dir, err)
	}
	var ids []string
	for _, e := range names {
		if runID, ok := strings.CutSuffix(e.Name(), fileExtension); ok {
			ids = append(ids, runID)
		}
	}
	return ids, nil
}

// sortRuns orders runs as ListRuns does: runs from before the run counter
// by their first entry's time, then the rest by start tick.
func sortRuns(runs []RunJournal) {
	sort.SliceStable(runs, func(i, j int) bool {
		a, b := startTick(runs[i]), startTick(runs[j])
		switch {
		case a == 0 && b == 0:
			return firstStart(runs[i]).Before(firstStart(runs[j]))
		case a == 0 || b == 0:
			return a == 0
		}
		return a < b
	})
}
