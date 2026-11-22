// Package credential: the lock every writer of the project's credential file
// holds from before it reads the file until its new file is in place.
//
// A writer reads the file, changes one entry and renames a new file over the
// old one. Two writers that read the same file each wrote it back without the
// other's entry, and the second rename won: parallel add-credential commands
// lost each other's devices. The lock makes those steps one turn.
//
// It is an advisory lock the operating system holds on an open file (flock on
// a Unix system, LockFileEx on Windows; the same two calls the run lock in
// internal/journal and the fact files in internal/facts use), on a file of
// its own beside the credential file. A lock on the credential file itself
// would be lost with the file each rename replaces. The system releases it
// when the holder's file closes, and a process that dies closes its files, so
// a crash never leaves a lock behind; the lock file itself stays, empty, and
// means nothing by being there.
//
// Readers take no lock and need none: the rename shows them the whole old
// file or the whole new one.
//
// The lock has run on Linux only so far. The Windows, macOS and FreeBSD
// builds compile and vet; none has run these tests yet. On Windows the open
// has no flag that refuses a symbolic link at the lock file's name, so a link
// there is followed and the lock is taken on its target (created empty when
// missing, never written to).
//
// What it does not cover, said plainly: a writer that does not take it (a
// pleiades binary older than this lock, or an editor) still races with one
// that does; a network filesystem may not keep such a lock between machines
// (NFS without a lock manager, some SMB mounts), so a project shared that way
// is as it was before; and a platform with neither call takes no lock at all
// (save_lock_other.go).
package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// credentialsLockName is the lock file, beside the credential file.
const credentialsLockName = "credentials.lock"

// credentialsLockPoll is how long a writer waits between tries for the lock.
// A save holds the lock for a few milliseconds, so a short pause keeps a line
// of writers moving.
const credentialsLockPoll = 5 * time.Millisecond

// credentialsLockWait is how long a writer waits for the lock before it
// gives up. A save takes milliseconds, so a wait this long means a holder
// that is stuck, and the writer says so rather than wait with it. A variable
// so a test can shorten it.
var credentialsLockWait = 30 * time.Second

// errSaveLockHeld is what tryLockFile returns when another writer holds the
// lock.
var errSaveLockHeld = errors.New("the lock is held")

// ErrCredentialsBusy is returned by a save that waited credentialsLockWait
// for the credential file's lock and never got it.
var ErrCredentialsBusy = errors.New("another command holds this project's credential file lock")

// credentialsLockPath is the lock file's path under dir.
func credentialsLockPath(dir string) string {
	return filepath.Join(dir, masterKeyDirName, credentialsLockName)
}

// tryLock is tryLockFile, the platform's one try for the lock. A variable so
// a test can make the system call fail, which no real file does on demand.
var tryLock = tryLockFile

// lockCredentials takes the credential file lock of the project at dir and
// returns the function that releases it. The project's .pleiades directory
// must exist.
//
// It waits for a holder to let go, and the wait ends one of two ways, each
// with an error naming the lock file: ctx is cancelled or passes its
// deadline, and the error wraps ctx's own; or credentialsLockWait passes, and
// the error is ErrCredentialsBusy. A ctx already cancelled takes no lock.
func lockCredentials(ctx context.Context, dir string) (func(), error) {
	path := credentialsLockPath(dir)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("did not take the credential file lock %s: %w", path, err)
	}
	// #nosec G304 -- path is the project directory the caller names joined
	// with this package's own fixed names; saveLockNoFollow refuses a link.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|saveLockNoFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open the credential file lock %s: %w", path, err)
	}
	deadline := time.Now().Add(credentialsLockWait)
	pause := time.NewTimer(credentialsLockPoll)
	defer pause.Stop()
	for {
		err := tryLock(f)
		if err == nil {
			break
		}
		if !errors.Is(err, errSaveLockHeld) {
			_ = f.Close()
			return nil, fmt.Errorf("failed to lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w: waited %s for %s; run the command again when the other one ends", ErrCredentialsBusy, credentialsLockWait, path)
		}
		pause.Reset(credentialsLockPoll)
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("gave up waiting for the credential file lock %s: %w", path, ctx.Err())
		case <-pause.C:
		}
	}
	return func() {
		// Closing the file releases the lock whether or not the unlock call
		// itself succeeds, so neither error has anything to add to a save
		// that has already written its file.
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}
