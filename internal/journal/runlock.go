// Package journal: the project's run lock, which keeps a rollback from
// running while a run is changing the same project's devices, or reading
// the journals a run is still writing.
//
// Runs take it shared, so any number run at once, as they always could. A
// rollback takes it exclusive: it reads every journal in the project to
// decide what to undo and what a later run has changed since, and a run
// writing alongside would make that answer stale the moment it was
// given. Neither waits: a run or rollback that cannot take the lock is
// refused at once, saying why, rather than sitting silent behind another
// that may take an hour.
package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// runLockName is the lock file, beside the journal directory.
const runLockName = "run.lock"

// ErrRunsBusy is returned when the lock is held in a way that excludes the
// caller: a rollback in progress, for a run; any run, for a rollback.
var ErrRunsBusy = errors.New("another run or rollback holds this project's run lock")

// LockRuns takes the run lock of the project at root, exclusive for a
// rollback and shared for a run, and returns the function that releases
// it. It never waits.
func LockRuns(root string, exclusive bool) (func() error, error) {
	dir := filepath.Join(root, pleiadesDirName)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", dir, err)
	}
	path := filepath.Join(dir, runLockName)
	// #nosec G304 -- path is the project directory the caller names joined
	// with this package's own fixed names.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("failed to open the run lock %s: %w", path, err)
	}
	if err := lockFile(f, exclusive); err != nil {
		_ = f.Close()
		if errors.Is(err, errLockHeld) {
			if exclusive {
				return nil, fmt.Errorf("%w: a run is in progress in this project; roll back when it ends", ErrRunsBusy)
			}
			return nil, fmt.Errorf("%w: a rollback is in progress in this project; run again when it ends", ErrRunsBusy)
		}
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}
	return func() error {
		unlockErr := unlockFile(f)
		closeErr := f.Close()
		return errors.Join(unlockErr, closeErr)
	}, nil
}

// errLockHeld is what lockFile returns when another holder excludes it.
var errLockHeld = errors.New("the lock is held")
