// Tests of the credential file lock itself: a bounded wait that names the
// lock file, a holder that died leaving nothing behind, and readers that
// never wait for it.
package credential

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	unlock, err := lockCredentials(dir)
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
		began := time.Now()
		err := write()
		if !errors.Is(err, ErrCredentialsBusy) {
			t.Fatalf("%s while the lock is held: got %v, want ErrCredentialsBusy", name, err)
		}
		if !strings.Contains(err.Error(), credentialsLockPath(dir)) {
			t.Errorf("%s: the refusal does not name the lock file %s: %v", name, credentialsLockPath(dir), err)
		}
		if waited := time.Since(began); waited > 5*time.Second {
			t.Errorf("%s: waited %s for a 100ms bound", name, waited)
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

// lockHolderDir tells a test binary it is the lock holder process below.
const lockHolderDir = "PLEIADES_TEST_LOCK_HOLDER_DIR"

// TestHelperProcess_HoldsTheLock is the process
// TestSaveLock_AHolderThatDiesLeavesNoLockBehind starts: it takes the lock,
// says so, and holds it until it is killed. Run as an ordinary test it does
// nothing.
func TestHelperProcess_HoldsTheLock(t *testing.T) {
	dir := os.Getenv(lockHolderDir)
	if dir == "" {
		t.Skip("a helper process for TestSaveLock_AHolderThatDiesLeavesNoLockBehind, not a test of its own")
	}
	if _, err := lockCredentials(dir); err != nil {
		t.Fatal(err)
	}
	fmt.Println("holding the lock")
	// Never released: the parent kills this process while it holds the lock.
	time.Sleep(time.Hour)
}

// TestSaveLock_AHolderThatDiesLeavesNoLockBehind: another process holds the
// lock, so a save here is refused; that process is then killed without ever
// releasing it, and the next save goes through at once. The lock file is
// still there and holds nobody back, which a lock that was only a file's
// existence could not say.
func TestSaveLock_AHolderThatDiesLeavesNoLockBehind(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, masterKeyDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- the test binary itself, re-run as the lock holder.
	holder := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_HoldsTheLock$", "-test.count=1")
	holder.Env = append(os.Environ(), lockHolderDir+"="+dir)
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	holder.Stderr = &stderr
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	defer func() {
		if !killed {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
	}()
	// Wait until the holder says it has the lock.
	lines := bufio.NewScanner(out)
	holding := false
	for lines.Scan() {
		if strings.Contains(lines.Text(), "holding the lock") {
			holding = true
			break
		}
	}
	if !holding {
		t.Fatalf("the holder process never took the lock: %s", stderr.String())
	}

	save := func() error {
		return SaveFileStore(dir, lockTestKey, "web01", Credential{Username: "u", Password: "s3cret-pw"})
	}
	shortLockWait(t, 100*time.Millisecond)
	if err := save(); !errors.Is(err, ErrCredentialsBusy) {
		t.Fatalf("save while another process holds the lock: got %v, want ErrCredentialsBusy", err)
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	killed = true

	if _, err := os.Stat(credentialsLockPath(dir)); err != nil {
		t.Fatalf("the lock file should still be there after its holder died: %v", err)
	}
	if err := save(); err != nil {
		t.Fatalf("save after the holder was killed: %v", err)
	}
}
