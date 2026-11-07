// Package journal: reading the Crawl tier's journal back, for `pleiades
// journal` and `pleiades rollback`.
//
// A rollback acts on what it reads here, so nothing here is trusted
// because this package wrote it. The file sits in a directory any process
// running as the operator can write, and a rollback replays the undo it
// finds. So the reader refuses anything the writer could not have made: a
// symbolic link, a file or directory others may write, a file too large or
// a line too long for any run, a key the entry type does not have, an
// entry naming another run, and a value outside a closed set or a known
// shape. What passes still goes to internal/rollback, which holds each
// recorded undo to the method's own declaration.
package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// maxJournalBytes bounds one run's journal file. A journal holds names and
// counts, a few hundred bytes a node; this is tens of thousands of nodes
// on thousands of devices, and a file past it is not one a run wrote.
const maxJournalBytes = 64 << 20

// maxLineBytes bounds one entry.
const maxLineBytes = 1 << 20

// nodeIDPattern is the graph ids the DAG builder synthesizes, a handler's
// copy after a section ("tasks.handlers[i]", internal/engine/chain.go; a
// handler is one task, never a block), and a step of a block's
// rollback_on_failure undoing one of them (.undo[k]).
var nodeIDPattern = regexp.MustCompile(`^(pre|post)?tasks(\[\d+\](\.(block|rescue|always|parallel)\[\d+\])*|\.handlers\[\d+\])(\.(item|attempt)\[\d+\]){0,2}(\.undo\[\d+\])?$`)

// digestPattern is DAG.Version's shape.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// RunJournal is one run's journal as read back.
type RunJournal struct {
	// ID is the run id.
	ID string
	// Entries are the run's entries, in file order.
	Entries []engine.JournalEntry
	// Sealed says the run returned and its journal is complete (seal.go).
	Sealed bool
	// EndTick is the run counter as the run ended, from its seal, or for a
	// run that died before its seal, the tick by which a later run found it
	// gone (clock_live.go); zero when neither is known.
	EndTick uint64
}

// ErrNoSuchRun is returned for a run id with no journal file.
var ErrNoSuchRun = errors.New("no journal for that run")

// ReadRun reads runID's journal from the project at root.
func ReadRun(root, runID string) (RunJournal, error) {
	dir := filepath.Join(root, pleiadesDirName, journalDirName)
	if err := checkJournalDir(dir); err != nil {
		return RunJournal{}, err
	}
	name, err := fileNameFor(runID)
	if err != nil {
		return RunJournal{}, err
	}
	entries, err := readJournalFile(filepath.Join(dir, name), runID)
	if err != nil {
		return RunJournal{}, err
	}
	sealPath, err := sealPathFor(dir, runID)
	if err != nil {
		return RunJournal{}, err
	}
	sealed, endTick, err := readSeal(sealPath, runID)
	if err != nil {
		return RunJournal{}, err
	}
	run := RunJournal{ID: runID, Entries: entries, Sealed: sealed, EndTick: endTick}
	// A run with no end tick may have died; a later run that found it gone
	// recorded by when (clock_live.go), which bounds it as well.
	if run.EndTick == 0 {
		if start := startTick(run); start != 0 {
			run.EndTick = endedTick(filepath.Join(root, pleiadesDirName), start)
		}
	}
	return run, nil
}

// ListRuns reads every run's journal in the project at root, oldest first:
// runs from before the run counter by their first entry's time, then the
// rest by start tick. A project with no journal directory has no runs.
func ListRuns(root string) ([]RunJournal, error) {
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
	var runs []RunJournal
	for _, e := range names {
		runID, ok := strings.CutSuffix(e.Name(), fileExtension)
		if !ok {
			continue
		}
		run, err := ReadRun(root, runID)
		if err != nil {
			return nil, fmt.Errorf("run %s: %w", runID, err)
		}
		runs = append(runs, run)
	}
	// Every run that carries no tick was written before the counter existed,
	// so before every run that does: those come first, by time, then the rest
	// by tick. One total order, whatever the mix.
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
	return runs, nil
}

// readJournalFile reads and checks every line of one journal file.
func readJournalFile(path, runID string) ([]engine.JournalEntry, error) {
	f, err := openPrivate(path, maxJournalBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []engine.JournalEntry
	scanner := bufio.NewScanner(io.LimitReader(f, maxJournalBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for line := 1; scanner.Scan(); line++ {
		entry, err := decodeEntry(scanner.Bytes(), runID)
		if err != nil {
			return nil, fmt.Errorf("the journal of run %s, line %d: %w", runID, line, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("the journal of run %s: %w", runID, err)
	}
	return entries, nil
}

// decodeEntry decodes one line, refusing anything the writer could not
// have produced.
func decodeEntry(line []byte, runID string) (engine.JournalEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	var l fileLine
	if err := dec.Decode(&l); err != nil {
		// A line from a newer build may hold a key this build does not
		// know, and the unknown key would be all the error said. Read the
		// format alone first, so such a line is refused for its version.
		if ferr := checkFormat(peekFormat(line)); ferr != nil {
			return l.JournalEntry, ferr
		}
		return l.JournalEntry, fmt.Errorf("not a journal entry: %w", err)
	}
	e := l.JournalEntry
	if dec.More() {
		return e, errors.New("more than one value on the line")
	}
	if err := checkFormat(l.Format); err != nil {
		return e, err
	}
	switch {
	case e.RunID != runID:
		return e, fmt.Errorf("names run %q", e.RunID)
	case !nodeIDPattern.MatchString(e.NodeID):
		return e, fmt.Errorf("names node %q, which is not a graph id", e.NodeID)
	case e.UndoesNode != "" && !nodeIDPattern.MatchString(e.UndoesNode):
		return e, fmt.Errorf("undoes node %q, which is not a graph id", e.UndoesNode)
	case e.DAGVersion != "" && !digestPattern.MatchString(e.DAGVersion):
		return e, fmt.Errorf("carries version %q, which is not a digest", e.DAGVersion)
	case !knownOutcome[e.Outcome]:
		return e, fmt.Errorf("records outcome %q", e.Outcome)
	// The stage and skip kind sets are the engine's own
	// (engine.IsRecordedFailureStage, IsRecordedSkipKind), so a kind the
	// engine learns to write is one this reader accepts: a second copy here
	// once lacked the handlers' not_notified, and every run with a handler
	// made journal list and rollback refuse the whole directory.
	case e.FailureStage != engine.FailureStageNone && !engine.IsRecordedFailureStage(e.FailureStage):
		return e, fmt.Errorf("records failure stage %q", e.FailureStage)
	case e.SkipKind != engine.SkipKindNone && !engine.IsRecordedSkipKind(e.SkipKind):
		return e, fmt.Errorf("records skip kind %q", e.SkipKind)
	case e.RollbackOf != "":
		if _, err := fileNameFor(e.RollbackOf); err != nil {
			return e, fmt.Errorf("undoes run %q, which is not a run id", e.RollbackOf)
		}
	}
	return e, nil
}

// The closed sets an entry's enums may hold.
var (
	knownOutcome = map[engine.Outcome]bool{
		engine.OutcomeRan: true, engine.OutcomeChanged: true, engine.OutcomeSkipped: true,
		engine.OutcomeFailed: true, engine.OutcomeNotReached: true,
	}
)

// readSeal reports whether runID's seal exists and names it, and the end
// tick it records.
func readSeal(path, runID string) (bool, uint64, error) {
	f, err := openPrivate(path, 4<<10)
	if errors.Is(err, fs.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = f.Close() }()
	var seal Seal
	dec := json.NewDecoder(io.LimitReader(f, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&seal); err != nil || seal.RunID != runID {
		return false, 0, fmt.Errorf("the seal of run %s is not one a run wrote", runID)
	}
	return true, seal.EndTick, nil
}

// startTick is the run counter as a run began, from its entries; zero for a
// run written before the counter existed.
func startTick(run RunJournal) uint64 {
	for _, e := range run.Entries {
		if e.RunTick != 0 {
			return e.RunTick
		}
	}
	return 0
}

// firstStart is when a run's earliest entry began.
func firstStart(run RunJournal) (first time.Time) {
	for _, e := range run.Entries {
		if !e.StartedAt.IsZero() && (first.IsZero() || e.StartedAt.Before(first)) {
			first = e.StartedAt
		}
	}
	return first
}
