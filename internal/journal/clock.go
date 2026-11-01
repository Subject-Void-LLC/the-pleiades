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
// from them (the highest tick any journal or seal records), never restarted
// at zero: a restart would make every new run look older than the last.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
// reads to find its tick.
const clockScanLineBytes = 1 << 20

// TickClock advances the project counter of the project at root and returns
// its new value: one more than the highest of the counter and every tick the
// project's journals and seals record.
func TickClock(root string) (uint64, error) {
	dir := filepath.Join(root, pleiadesDirName)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return 0, fmt.Errorf("failed to create %s: %w", dir, err)
	}
	unlock, err := lockClock(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unlock() }()

	current := readClock(filepath.Join(dir, clockName))
	recorded, err := highestRecordedTick(filepath.Join(dir, journalDirName))
	if err != nil {
		return 0, err
	}
	next := max(current, recorded) + 1
	if err := writeClock(dir, next); err != nil {
		return 0, err
	}
	return next, nil
}

// lockClock takes the counter's own lock, waiting up to clockLockWait for
// another run's tick, and returns the function that releases it.
func lockClock(dir string) (func() error, error) {
	path := filepath.Join(dir, clockLockName)
	// #nosec G304 -- the project directory joined with this package's own
	// fixed names.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
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
	f, err := openPrivate(path, 64)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 64))
	if err != nil {
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

// highestRecordedTick returns the highest tick the journals in dir record:
// each journal's first entry's RunTick (every entry of a run holds the same)
// and each seal's EndTick. Files that are not regular, or that do not decode,
// are passed over: the strict reader refuses them when it reads them, and the
// counter only needs a floor.
func highestRecordedTick(dir string) (uint64, error) {
	names, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to list %s: %w", dir, err)
	}
	var highest uint64
	for _, e := range names {
		if !e.Type().IsRegular() {
			continue
		}
		var tick uint64
		switch {
		case strings.HasSuffix(e.Name(), fileExtension):
			tick = firstEntryTick(filepath.Join(dir, e.Name()))
		case strings.HasSuffix(e.Name(), sealExtension):
			tick = sealTick(filepath.Join(dir, e.Name()))
		}
		highest = max(highest, tick)
	}
	return highest, nil
}

// firstEntryTick returns the RunTick of the journal at path's first entry,
// zero when it has none.
func firstEntryTick(path string) uint64 {
	f, err := os.Open(path) // #nosec G304 -- a regular file listed in the journal directory
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(f, clockScanLineBytes))
	scanner.Buffer(make([]byte, 64<<10), clockScanLineBytes)
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

// sealTick returns the EndTick of the seal at path, zero when it has none.
func sealTick(path string) uint64 {
	raw, err := os.ReadFile(path) // #nosec G304 -- a regular file listed in the journal directory
	if err != nil || len(raw) > 4<<10 {
		return 0
	}
	var seal Seal
	if json.Unmarshal(raw, &seal) != nil {
		return 0
	}
	return seal.EndTick
}
