// Package journal: the project counter, the Crawl tier's order of runs.
//
// The rollback planner refuses undoing a run beneath a later run that
// changed the same device, and "later" used to be read off the journals'
// wall-clock times: one machine's clock, which can be stepped back between
// two runs and hide the later one. The counter is a number in
// .pleiades/clock that a run ticks as it begins and again as it ends, under a
// lock of its own, so two runs that overlap still get distinct ticks. Each of
// a run's entries carries its start tick (engine.JournalEntry.RunTick) and its
// seal carries its end tick, and the planner compares those.
//
// A counter that is missing, unreadable or behind the journals is rebuilt
// from them (the highest tick any journal, seal or run marker records), never
// restarted at zero: a restart would make every new run look older than the
// last. The journals are read for that outside the counter's lock, so a large
// project never holds other runs' ticks waiting; a tick taken meanwhile is in
// the counter itself, which is read under the lock.
//
// A run that dies before its seal records no end tick. clock_live.go is what
// bounds it anyway: every run holds a lock while it lives, and the next run
// to begin after it died records that it was gone by then.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The counter's files, beside the journal directory.
const (
	clockName     = "clock"
	clockLockName = "clock.lock"
)

// clockLockWait bounds how long a tick waits for another run's tick. A tick
// holds the lock for one small read and write, so anything near this is a
// stuck process rather than a busy one. A variable only so a test can
// shorten it.
var clockLockWait = 10 * time.Second

// clockScanLineBytes bounds how much of a journal's first line the rebuild
// reads to find its tick: the strict reader's own line bound.
const clockScanLineBytes = maxLineBytes

// maxSealBytes bounds a seal file, as the strict reader does.
const maxSealBytes = 4 << 10

// ErrClockExhausted is returned when the counter cannot go higher: a journal,
// seal or marker records the largest tick there is, which no run writes, so
// it was put there by hand.
var ErrClockExhausted = errors.New("the project's run counter is at its largest value")

// TickClock advances the project counter of the project at root and returns
// its new value: one more than the highest of the counter and every tick the
// project's journals, seals and run markers record.
func TickClock(root string) (uint64, error) {
	dir := filepath.Join(root, pleiadesDirName)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return 0, fmt.Errorf("failed to create %s: %w", dir, err)
	}
	// Read before the lock: only what the counter does not already hold
	// (a deleted or restored counter's past) can come from here, and any
	// tick taken while this reads is in the counter, read under the lock.
	recorded, err := highestRecordedTick(dir)
	if err != nil {
		return 0, err
	}
	unlock, err := lockClock(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unlock() }()

	current := readClock(filepath.Join(dir, clockName))
	floor := max(current, recorded)
	if floor == math.MaxUint64 {
		return 0, ErrClockExhausted
	}
	next := floor + 1
	if err := writeClock(dir, next); err != nil {
		return 0, err
	}
	return next, nil
}

// NextTick advances the project counter alone and returns its new value,
// for a run that already took its tick through TickClock (BeginRun), which
// floored the counter at every tick the project records: what a run needs
// to order its own events after its start, such as each task's facts (Phase
// 41b), without reading every journal again. A counter that is missing or
// unreadable starts from zero here, so a value from NextTick is only ever
// compared with another of the same counter's.
func NextTick(root string) (uint64, error) {
	dir := filepath.Join(root, pleiadesDirName)
	unlock, err := lockClock(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unlock() }()
	current := readClock(filepath.Join(dir, clockName))
	if current == math.MaxUint64 {
		return 0, ErrClockExhausted
	}
	if err := writeClock(dir, current+1); err != nil {
		return 0, err
	}
	return current + 1, nil
}

// lockClock takes the counter's own lock, waiting up to clockLockWait for
// another run's tick, and returns the function that releases it.
func lockClock(dir string) (func() error, error) {
	path := filepath.Join(dir, clockLockName)
	// #nosec G304 -- the project directory joined with this package's own
	// fixed names; noFollow refuses a link put at the name.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|noFollow, fileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open the counter's lock %s: %w", path, err)
	}
	deadline := time.Now().Add(clockLockWait)
	for {
		err := lockFile(f, true)
		if err == nil {
			break
		}
		if !errors.Is(err, errLockHeld) || time.Now().After(deadline) {
			_ = f.Close()
			if errors.Is(err, errLockHeld) {
				return nil, fmt.Errorf("the run counter's lock %s was held for over %s", path, clockLockWait)
			}
			return nil, fmt.Errorf("failed to lock %s: %w", path, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() error {
		return errors.Join(unlockFile(f), f.Close())
	}, nil
}

// readClock returns the counter's value, or zero when the file is missing or
// does not hold one, which the rebuild then answers.
func readClock(path string) uint64 {
	return readNumber(path)
}

// readPrivate reads the whole of a small file at path, opened as the strict
// reader opens one (no link, owner-only, no larger than limit), and reports
// false when it cannot.
func readPrivate(path string, limit int64) ([]byte, bool) {
	f, err := openPrivate(path, limit)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, limit))
	return raw, err == nil
}

// readNumber reads the one number a counter or marker file holds, zero when
// it holds none or cannot be read as the strict reader would.
func readNumber(path string) uint64 {
	raw, ok := readPrivate(path, maxMarkerBytes)
	if !ok {
		return 0
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// writeClock replaces the counter with value, synced, through a rename, so a
// crash leaves the old value or the new one and never half of either.
func writeClock(dir string, value uint64) error {
	tmp, err := os.CreateTemp(dir, ".clock-*")
	if err != nil {
		return fmt.Errorf("failed to write the run counter: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	// Written, synced and closed, every failure among the three reported:
	// a value that may not be on disk is never renamed into place.
	_, writeErr := tmp.WriteString(strconv.FormatUint(value, 10) + "\n")
	syncErr := tmp.Sync()
	if err := errors.Join(writeErr, syncErr, tmp.Close()); err != nil {
		return fmt.Errorf("failed to write the run counter: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dir, clockName)); err != nil {
		return fmt.Errorf("failed to replace the run counter: %w", err)
	}
	// The rename is durable only once the directory itself is synced.
	if d, err := os.Open(dir); err == nil { // #nosec G304 -- the project's own .pleiades directory
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// highestRecordedTick returns the highest tick the project under dir (its
// .pleiades) records: each journal's first entry's RunTick (every entry of a
// run holds the same), each seal's EndTick, and each run marker's tick
// (clock_live.go). Every file is read as the strict reader reads it (no
// link, owner-only, bounded); one it would refuse, or that does not decode,
// is passed over, since the counter only needs a floor.
func highestRecordedTick(dir string) (uint64, error) {
	var highest uint64
	journals := filepath.Join(dir, journalDirName)
	names, err := os.ReadDir(journals)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("failed to list %s: %w", journals, err)
	}
	for _, e := range names {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(journals, e.Name())
		switch {
		case strings.HasSuffix(e.Name(), fileExtension):
			highest = max(highest, firstEntryTick(path))
		case strings.HasSuffix(e.Name(), sealExtension):
			highest = max(highest, sealTick(path))
		}
	}
	marked, err := highestMarkerTick(filepath.Join(dir, liveDirName))
	if err != nil {
		return 0, err
	}
	return max(highest, marked), nil
}

// firstEntryTick returns the RunTick of the journal at path's first entry,
// zero when it has none or cannot be read as the strict reader would.
func firstEntryTick(path string) uint64 {
	f, err := openPrivate(path, maxJournalBytes)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(f, clockScanLineBytes+1))
	scanner.Buffer(make([]byte, 4<<10), clockScanLineBytes)
	if !scanner.Scan() {
		return 0
	}
	var first struct {
		RunTick uint64 `json:"run_tick"`
	}
	if json.Unmarshal(scanner.Bytes(), &first) != nil {
		return 0
	}
	return first.RunTick
}

// sealTick returns the EndTick of the seal at path, zero when it has none or
// cannot be read as the strict reader would.
func sealTick(path string) uint64 {
	raw, ok := readPrivate(path, maxSealBytes)
	if !ok {
		return 0
	}
	var seal Seal
	if json.Unmarshal(raw, &seal) != nil {
		return 0
	}
	return seal.EndTick
}
