// Package journal: the write path, and the one validation it performs.
//
// Record is the whole of engine.Journal. Everything here exists to
// satisfy an obligation that interface's doc comment places on an
// implementation, and each one is named at the place it is met.
package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// Record implements engine.Journal by appending every entry to the file
// named for its run.
//
// The context arrives already detached: the engine hands Record
// context.WithoutCancel of the run's own context plus its own timeout,
// so a graceful shutdown does not discard the journal of every level
// that had already completed. This method therefore never treats a done
// context as run cancellation, and never lengthens the deadline it was
// given. It checks the deadline between files, which is where the only
// meaningful amount of work sits.
//
// An error returned here never fails the run. The engine logs it,
// counts it, and returns the run's own outcome unchanged. That is the
// contract, and the reason is worth keeping in view even on this tier
// where nothing redelivers: a failed audit write must never turn into a
// repeated configuration change.
func (s *FileStore) Record(ctx context.Context, entries []engine.JournalEntry) error {
	if len(entries) == 0 {
		return nil
	}

	// Grouped by run rather than assumed to be one run. A single call
	// does carry one level of one run today, but the port explicitly
	// allows concurrent calls with different RunIDs, and a sink that
	// reads entries[0].RunID and writes the rest under it would put one
	// run's entries in another run's file the first time that changed.
	order, byRun := groupByRun(entries)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, runID := range order {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("failed to record the journal: %w", err)
		}
		if err := s.appendRun(runID, byRun[runID]); err != nil {
			return err
		}
	}
	return nil
}

// groupByRun splits entries by RunID, returning the run ids in the order
// they first appear alongside the grouping.
//
// The order is returned separately because ranging a map is randomized,
// and two runs of the same input writing their files in a different
// order would make a failure impossible to reproduce.
func groupByRun(entries []engine.JournalEntry) ([]string, map[string][]engine.JournalEntry) {
	order := make([]string, 0, 1)
	byRun := make(map[string][]engine.JournalEntry, 1)
	for _, entry := range entries {
		if _, seen := byRun[entry.RunID]; !seen {
			order = append(order, entry.RunID)
		}
		byRun[entry.RunID] = append(byRun[entry.RunID], entry)
	}
	return order, byRun
}

// appendRun writes one run's entries to that run's file.
//
// The file is opened fresh on every call rather than kept open. A run is
// a handful of level barriers, the cost is a handful of opens, and a
// long-lived handle would have to be closed by something, tracked per
// run, and reasoned about when a run ends badly.
func (s *FileStore) appendRun(runID string, entries []engine.JournalEntry) (err error) {
	name, err := fileNameFor(runID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)

	lines, err := encode(entries)
	if err != nil {
		return err
	}

	// #nosec G304 -- path is s.dir, fixed at construction from the CLI's
	// own project directory, joined with a name fileNameFor has already
	// restricted to a run id shaped like the uuid the engine mints. No
	// part of it is caller-influenced text.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("failed to open the journal file for run %s: %w", runID, err)
	}

	// Closed exactly once, here, with its error reported when nothing
	// worse already went wrong. A deferred Close beside an explicit one
	// closes the handle twice and discards whatever the second call says,
	// and Close is not a formality on every filesystem: it is where a
	// deferred write error can finally surface.
	defer func() {
		closeErr := f.Close()
		if closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close the journal file for run %s: %w", runID, closeErr)
		}
	}()

	if _, err := f.Write(lines); err != nil {
		return fmt.Errorf("failed to append to the journal file for run %s: %w", runID, err)
	}

	// Synced because the point of the file is to survive whatever ended
	// the run. An entry sitting in the page cache when the machine loses
	// power is an entry that was never recorded.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync the journal file for run %s: %w", runID, err)
	}
	return nil
}

// encode renders entries as JSON Lines: one object per line, newline
// terminated, no enclosing array.
//
// The whole batch is built in memory and written once, so a level's
// entries reach the file as a single append rather than as one write per
// entry that another process could interleave with.
func encode(entries []engine.JournalEntry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)

	// The default marshaler escapes <, > and & for safe embedding in
	// HTML, which this file is not. Left on, the sentinel the projection
	// stores for an unresolved FQCN is written as an escape sequence
	// instead of as the readable name it was chosen to be.
	enc.SetEscapeHTML(false)

	for _, entry := range entries {
		if err := enc.Encode(normalize(entry)); err != nil {
			return nil, fmt.Errorf("failed to encode a journal entry for run %s: %w", entry.RunID, err)
		}
	}
	return buf.Bytes(), nil
}

// normalize replaces a nil key vector with an empty one.
//
// encoding/json writes null for a nil slice and [] for an empty one, and
// the projection leaves a vector nil whenever it admitted no keys. Both
// mean "no keys", so the difference carries nothing and only makes every
// reader handle two spellings of one fact.
//
// Both sinks apply it, which is the point rather than a detail. It was
// applied on the file side alone at first, so the same run recorded []
// on the Crawl tier and null in the Walk tier's jsonb column, and there
// the difference is not cosmetic: SQLite reads json_array_length('null')
// as 0, while PostgreSQL refuses it with "cannot get array length of a
// scalar". An operator's query over stat_keys therefore failed on
// exactly the rows where a task recorded no keys, which is every failed
// task.
func normalize(entry engine.JournalEntry) engine.JournalEntry {
	if entry.StatKeys == nil {
		entry.StatKeys = []string{}
	}
	if entry.ParamKeys == nil {
		entry.ParamKeys = []string{}
	}
	if entry.InverseParamKeys == nil {
		entry.InverseParamKeys = []string{}
	}
	if entry.InverseParams == nil {
		entry.InverseParams = []engine.InverseParam{}
	}
	return entry
}

// fileNameFor turns a run id into the file it is recorded in, refusing
// any id that could name something other than a file in this directory.
//
// The engine mints RunID as a uuid, so nothing produces a bad one today.
// It is checked anyway, because "safe because of who usually produces
// it, not validated" is the exact trap FAILURE_PATTERNS.md #63 records:
// an unvalidated JobID once widened a NATS subject wildcard into every
// job's logs, and every producer of it at the time was a server
// generated uuid too.
func fileNameFor(runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("failed to name a journal file: the entry carries no run id")
	}
	if len(runID) > maxRunIDLen {
		return "", fmt.Errorf("failed to name a journal file: run id is %d characters, over the %d allowed", len(runID), maxRunIDLen)
	}
	if strings.ContainsFunc(runID, func(r rune) bool { return !isRunIDRune(r) }) {
		return "", fmt.Errorf("failed to name a journal file: run id %q holds a character outside [A-Za-z0-9-]", runID)
	}
	return runID + fileExtension, nil
}

// maxRunIDLen bounds the name so a pathological id cannot produce a file
// name the filesystem refuses. A uuid is 36 characters.
const maxRunIDLen = 64

// isRunIDRune reports whether r may appear in a run id.
//
// The set is exactly what a uuid spells. It excludes the path separator,
// the dot, and every other character that could make the joined path
// mean a different file, so the check is a whitelist rather than a list
// of things to escape.
func isRunIDRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-':
		return true
	default:
		return false
	}
}
