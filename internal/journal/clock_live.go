// Package journal: run markers, which bound a run that died before its seal.
//
// A run's seal holds its end tick on the project counter (clock.go). A run
// that dies first (killed, or its machine lost) has none, and the rollback
// planner would have to count it as later than every run after it, forever.
// The markers bound it instead.
//
// While a run lives it holds a lock on .pleiades/clock.d/<start-tick>.live; an
// OS releases a dead process's lock on its own. Each run, as it begins, looks
// at the markers of runs that began before it: one whose lock nobody holds
// belongs to a run that is gone, and the beginning run records that with
// <start-tick>.ended, holding its own start tick. That run's changes all came
// before then, so the reader takes it as the dead run's end tick. A run that
// seals removes its own marker.
//
// Where a lock cannot tell a live process from a dead one (lockFile is a no-op
// there, activeLocks), no run is ever marked ended: a run that died keeps
// counting as later, which is the safe direction.
package journal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The marker directory, beside the journal, and its two file kinds.
const (
	liveDirName     = "clock.d"
	liveExtension   = ".live"
	endedExtension  = ".ended"
	maxMarkerBytes  = 64
	liveTempPattern = ".live-*"
)

// holdLive creates and locks the marker of the run whose start tick is tick
// in the .pleiades directory dir, returning the open, locked file. The lock is
// taken on a temporary file before it is renamed into place, so no other run
// can find the marker while it stands unlocked and mark a live run ended.
func holdLive(dir string, tick uint64) (*os.File, string, error) {
	liveDir := filepath.Join(dir, liveDirName)
	if err := os.MkdirAll(liveDir, dirMode); err != nil {
		return nil, "", fmt.Errorf("failed to create %s: %w", liveDir, err)
	}
	f, err := os.CreateTemp(liveDir, liveTempPattern)
	if err != nil {
		return nil, "", fmt.Errorf("failed to mark this run as live: %w", err)
	}
	// Locked first, then renamed into place: never found unlocked.
	path := filepath.Join(liveDir, strconv.FormatUint(tick, 10)+liveExtension)
	err = lockFile(f, true)
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = unlockFile(f)
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, "", fmt.Errorf("failed to mark this run as live: %w", err)
	}
	return f, path, nil
}

// releaseLive lets go of a run's marker once the run is sealed.
func releaseLive(f *os.File, path string) error {
	if f == nil {
		return nil
	}
	removeErr := os.Remove(path)
	if errors.Is(removeErr, fs.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(removeErr, unlockFile(f), f.Close())
}

// markEndedRuns records, for every run that began before the run whose start
// tick is tick and is gone (its marker's lock is held by nobody), that it was
// gone by tick. A marker already recorded is left as it is: the first run to
// see a run gone gives the tighter bound.
func markEndedRuns(dir string, tick uint64) error {
	if !activeLocks {
		return nil
	}
	// holdLive made the directory just before, so any failure to list it is
	// a real one.
	liveDir := filepath.Join(dir, liveDirName)
	names, err := os.ReadDir(liveDir)
	if err != nil {
		return fmt.Errorf("failed to list %s: %w", liveDir, err)
	}
	for _, e := range names {
		raw, ok := strings.CutSuffix(e.Name(), liveExtension)
		if !ok || !e.Type().IsRegular() {
			continue
		}
		start, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || start >= tick {
			continue
		}
		livePath := filepath.Join(liveDir, e.Name())
		if heldByLiveRun(livePath) {
			continue
		}
		endedPath := filepath.Join(liveDir, raw+endedExtension)
		// #nosec G304 -- the project's own marker directory joined with a
		// name parsed as a number.
		f, err := os.OpenFile(endedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, fileMode)
		if err == nil {
			_, writeErr := f.WriteString(strconv.FormatUint(tick, 10) + "\n")
			if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
				return fmt.Errorf("failed to record run %d as ended: %w", start, err)
			}
		} else if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("failed to record run %d as ended: %w", start, err)
		}
		_ = os.Remove(livePath)
	}
	return nil
}

// endedTick returns the tick by which the run whose start tick is start was
// recorded gone, zero when no run recorded it (it sealed, still lives, or no
// run has begun since it died).
func endedTick(dir string, start uint64) uint64 {
	path := filepath.Join(dir, liveDirName, strconv.FormatUint(start, 10)+endedExtension)
	return readMarker(path)
}

// readMarker returns the number an ended marker holds, read as the strict
// reader reads, zero when it holds none.
func readMarker(path string) uint64 {
	return readNumber(path)
}

// highestMarkerTick returns the highest tick the markers in liveDir name or
// hold: a run that ticked and died before it journaled anything still used
// its tick, which the counter must not hand out again.
func highestMarkerTick(liveDir string) (uint64, error) {
	names, err := os.ReadDir(liveDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to list %s: %w", liveDir, err)
	}
	var highest uint64
	for _, e := range names {
		name := e.Name()
		for _, ext := range []string{liveExtension, endedExtension} {
			raw, ok := strings.CutSuffix(name, ext)
			if !ok {
				continue
			}
			if start, err := strconv.ParseUint(raw, 10, 64); err == nil {
				highest = max(highest, start)
			}
			if ext == endedExtension && e.Type().IsRegular() {
				highest = max(highest, readMarker(filepath.Join(liveDir, name)))
			}
		}
	}
	return highest, nil
}
