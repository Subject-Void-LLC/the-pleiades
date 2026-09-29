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

// nodeIDPattern is the graph ids the DAG builder synthesizes.
var nodeIDPattern = regexp.MustCompile(`^(pre|post)?tasks\[\d+\](\.(block|rescue|always|parallel)\[\d+\])*$`)

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
	sealed, err := readSeal(sealPath, runID)
	if err != nil {
		return RunJournal{}, err
	}
	return RunJournal{ID: runID, Entries: entries, Sealed: sealed}, nil
}

// ListRuns reads every run's journal in the project at root, oldest first
// by its first entry. A project with no journal directory has no runs.
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
	sort.SliceStable(runs, func(i, j int) bool { return firstStart(runs[i]).Before(firstStart(runs[j])) })
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
	var e engine.JournalEntry
	if err := dec.Decode(&e); err != nil {
		return e, fmt.Errorf("not a journal entry: %w", err)
	}
	if dec.More() {
		return e, errors.New("more than one value on the line")
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
	case !knownStage[e.FailureStage]:
		return e, fmt.Errorf("records failure stage %q", e.FailureStage)
	case !knownSkip[e.SkipKind]:
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
	knownStage = map[engine.FailureStage]bool{
		engine.FailureStageNone: true, engine.FailureStageWorkflowRead: true, engine.FailureStageConditionEval: true,
		engine.FailureStageSecretMask: true, engine.FailureStageResolveTarget: true, engine.FailureStageLockAll: true,
		engine.FailureStageLockDevice: true, engine.FailureStageAction: true, engine.FailureStageRegisterMask: true,
		engine.FailureStageRecord: true,
	}
	knownSkip = map[engine.SkipKind]bool{
		engine.SkipKindNone: true, engine.SkipKindWhen: true, engine.SkipKindWhenOr: true,
		engine.SkipKindWhenCEL: true, engine.SkipKindLifecycle: true,
	}
)

// readSeal reports whether runID's seal exists and names it.
func readSeal(path, runID string) (bool, error) {
	f, err := openPrivate(path, 4<<10)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	var seal Seal
	dec := json.NewDecoder(io.LimitReader(f, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&seal); err != nil || seal.RunID != runID {
		return false, fmt.Errorf("the seal of run %s is not one a run wrote", runID)
	}
	return true, nil
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
