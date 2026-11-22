// Tests of the credential file lock itself: a bounded wait that names the
// lock file, a holder that died leaving nothing behind, and readers that
// never wait for it.
package credential

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lockTestKey is a master key for these tests.
var lockTestKey = bytes.Repeat([]byte{'k'}, 32)

// shortLockWait makes a writer give up on the lock after wait for the rest of
// the test, so a test of the refusal does not take the real half minute.
func shortLockWait(t *testing.T, wait time.Duration) {
	t.Helper()
	old := credentialsLockWait
	credentialsLockWait = wait
	t.Cleanup(func() { credentialsLockWait = old })
}

// heldLock makes dir's .pleiades directory and takes its credential lock.
func heldLock(t *testing.T, dir string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, masterKeyDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockCredentials(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return unlock
}

// TestSaveLock_AWriterWaitsOnlySoLongAndNamesTheLockFile: while one holder
// has the lock, every kind of writer gives up after the wait, saying which
// file it waited for and writing nothing; once the holder lets go the same
// save goes through.
func TestSaveLock_AWriterWaitsOnlySoLongAndNamesTheLockFile(t *testing.T) {
	dir := t.TempDir()
	unlock := heldLock(t, dir)
	shortLockWait(t, 100*time.Millisecond)

	writers := map[string]func() error{
		"save a device": func() error {
			return SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"})
		},
		"remove a device": func() error { _, err := RemoveFileStore(dir, "web01"); return err },
		"bind a device":   func() error { _, err := BindFileStore(dir, "web01"); return err },
		"save a secret":   func() error { return SaveSecretField(dir, lockTestKey, "ike", "key", "a-shared-key-value") },
		"remove a secret": func() error { _, err := RemoveSecret(dir, "ike", ""); return err },
		"save a registry": func() error {
			return SaveRegistryCredential(dir, lockTestKey, "registry.example.com", "u", "s3cret-pw")
		},
		"remove a database": func() error {
			_, err := RemoveDatabaseConnection(dir, "appdb")
			return err
		},
	}
	for name, write := range writers {
		err := write()
		if !errors.Is(err, ErrCredentialsBusy) {
			t.Fatalf("%s while the lock is held: got %v, want ErrCredentialsBusy", name, err)
		}
		if !strings.Contains(err.Error(), credentialsLockPath(dir)) {
			t.Errorf("%s: the refusal does not name the lock file %s: %v", name, credentialsLockPath(dir), err)
		}
	}
	if _, err := os.Stat(credentialsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a refused writer left a credential file: %v", err)
	}

	unlock()
	if err := writers["save a device"](); err != nil {
		t.Fatalf("save after the holder let go: %v", err)
	}
}

// TestSaveLock_ReadersNeverWaitForIt: with the lock held, a lookup and each
// listing answer at once from the file as it stands.
func TestSaveLock_ReadersNeverWaitForIt(t *testing.T) {
	dir := t.TempDir()
	if err := SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"}); err != nil {
		t.Fatal(err)
	}
	defer heldLock(t, dir)()
	shortLockWait(t, 50*time.Millisecond)

	store, err := NewFileStore(dir, lockTestKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Lookup(context.Background(), "web01")
	if err != nil || got.Password != "s3cret-pw" {
		t.Fatalf("lookup while the lock is held: %v", err)
	}
	if listed, err := ListCredentials(dir); err != nil || len(listed) != 1 {
		t.Fatalf("listing while the lock is held: %d, %v", len(listed), err)
	}
	if _, err := ListSecrets(dir); err != nil {
		t.Fatalf("listing secrets while the lock is held: %v", err)
	}
	if _, err := SecretValues(dir); err != nil {
		t.Fatalf("reading secret values while the lock is held: %v", err)
	}
}

// TestSaveLock_ALockFileThatCannotBeOpenedIsNamed: a lock file that cannot be
// opened (a directory stands where it goes) fails the save with the path,
// before anything is read or written.
func TestSaveLock_ALockFileThatCannotBeOpenedIsNamed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(credentialsLockPath(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	err := SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"})
	if err == nil || !strings.Contains(err.Error(), credentialsLockPath(dir)) {
		t.Fatalf("got %v, want a failure naming %s", err, credentialsLockPath(dir))
	}
	if _, statErr := os.Stat(credentialsPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("a save that never had the lock wrote the credential file: %v", statErr)
	}
}

// TestSaveLock_ACancelledContextEndsTheWait: a writer waiting for a held lock
// stops when its context is cancelled, with the context's own error and the
// lock file's name, and a context cancelled before the wait takes no lock at
// all, held or free.
func TestSaveLock_ACancelledContextEndsTheWait(t *testing.T) {
	dir := t.TempDir()
	unlock := heldLock(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := BindFileStoreContext(ctx, dir, "web01")
		done <- err
	}()
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrCredentialsBusy) {
		t.Fatalf("a cancelled wait: got %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), credentialsLockPath(dir)) {
		t.Errorf("the refusal does not name the lock file: %v", err)
	}

	deadline, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if _, err := BindFileStoreContext(deadline, dir, "web01"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a wait past its deadline: got %v, want context.DeadlineExceeded", err)
	}

	unlock()
	if _, err := lockCredentials(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("a free lock asked for with a cancelled context: got %v, want context.Canceled", err)
	}
}

// TestSaveLock_ALookupDoesNotWaitOutASaveToClearAnUnboundMark: a run's first
// lookup of an unbound credential says so and clears the mark, which is a
// write. With the lock held by another command the lookup still returns the
// credential after its short bound, the notice is given, and the mark stays
// for a later run to clear; with the lock free the mark is cleared.
func TestSaveLock_ALookupDoesNotWaitOutASaveToClearAnUnboundMark(t *testing.T) {
	dir := t.TempDir()
	if err := SaveFileStoreWith(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"}, SaveOptions{Unbound: true}); err != nil {
		t.Fatal(err)
	}
	old := unboundBindWait
	unboundBindWait = 50 * time.Millisecond
	t.Cleanup(func() { unboundBindWait = old })
	isUnbound := func() bool {
		listed, err := ListCredentials(dir)
		if err != nil || len(listed) != 1 {
			t.Fatalf("listing: %v, %v", listed, err)
		}
		return listed[0].Unbound
	}
	lookup := func() int {
		inner, err := NewFileStore(dir, lockTestKey)
		if err != nil {
			t.Fatal(err)
		}
		notices := 0
		got, err := NoticeUnbound(inner, dir, func(string) { notices++ }).Lookup(context.Background(), "web01")
		if err != nil || got.Password != "s3cret-pw" {
			t.Fatalf("lookup: %v", err)
		}
		return notices
	}

	unlock := heldLock(t, dir)
	if notices := lookup(); notices != 1 || !isUnbound() {
		t.Fatalf("with the lock held: %d notices, unbound=%v; want the notice and the mark kept", notices, isUnbound())
	}
	unlock()
	if notices := lookup(); notices != 1 || isUnbound() {
		t.Fatalf("with the lock free: %d notices, unbound=%v; want the notice and the mark cleared", notices, isUnbound())
	}
}

// TestSaveLock_AFailedLockCallIsNotCalledBusy: a lock call that fails for a
// reason other than another holder ends the save at once with that reason and
// the lock file's name, not as a busy lock after the whole wait.
func TestSaveLock_AFailedLockCallIsNotCalledBusy(t *testing.T) {
	dir := t.TempDir()
	broken := errors.New("the lock call itself failed")
	old := tryLock
	tryLock = func(*os.File) error { return broken }
	t.Cleanup(func() { tryLock = old })
	err := SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"})
	if !errors.Is(err, broken) || errors.Is(err, ErrCredentialsBusy) || !strings.Contains(err.Error(), credentialsLockPath(dir)) {
		t.Fatalf("got %v, want the lock call's own failure naming %s", err, credentialsLockPath(dir))
	}
}

// TestSaveLock_ALinkWhereTheLockFileGoesIsNotFollowed: a symbolic link planted
// at the lock file's name, pointing outside the project, is refused on a Unix
// system (the open's O_NOFOLLOW): the save fails, naming the lock file, and
// the link's target is neither made nor changed. The error's number is not
// held to one value, since systems differ (Linux gives ELOOP, FreeBSD
// EMLINK).
//
// Where the open has no such flag (Windows, saveLockNoFollow 0) the link is
// followed: the lock is taken on the target, which is made empty when
// missing, and the save goes through. That is stated here, not asserted as a
// refusal; what holds everywhere is that an existing target's contents are
// never changed, since nothing is ever written to the lock file.
func TestSaveLock_ALinkWhereTheLockFileGoesIsNotFollowed(t *testing.T) {
	for _, existing := range []bool{false, true} {
		dir, outside := t.TempDir(), t.TempDir()
		target := filepath.Join(outside, "victim")
		if existing {
			if err := os.WriteFile(target, []byte("not a lock file"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(dir, masterKeyDirName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, credentialsLockPath(dir)); err != nil {
			t.Skipf("needs symbolic links: this system would not make one: %v", err)
		}
		err := SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"})
		if existing {
			if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "not a lock file" {
				t.Errorf("the link's target was changed: %q, %v", data, readErr)
			}
		}
		if saveLockNoFollow == 0 {
			t.Logf("this system's open follows a link at the lock file's name (existing target %v): save returned %v", existing, err)
			continue
		}
		if err == nil || !strings.Contains(err.Error(), credentialsLockPath(dir)) {
			t.Errorf("existing target %v: got %v, want a refusal naming %s", existing, err, credentialsLockPath(dir))
		}
		if _, statErr := os.Stat(credentialsPath(dir)); !os.IsNotExist(statErr) {
			t.Errorf("existing target %v: a save refused at the lock wrote the credential file: %v", existing, statErr)
		}
		if _, statErr := os.Lstat(target); !existing && !os.IsNotExist(statErr) {
			t.Errorf("the save made the link's target: %v", statErr)
		}
	}
}
