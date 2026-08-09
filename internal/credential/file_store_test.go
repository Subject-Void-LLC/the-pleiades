package credential_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// testKey builds a deterministic 32-byte AES-256 key filled with fill,
// so tests that need "a valid key" or "two different valid keys" can
// build them without depending on ResolveMasterKey.
func testKey(fill byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill
	}
	return key
}

func TestFileStore_RoundTripPassword(t *testing.T) {
	dir := t.TempDir()
	key := testKey('a')

	want := credential.Credential{Username: "admin", Password: "hunter2"}
	if err := credential.SaveFileStore(dir, key, "router1", want); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	got, err := store.Lookup(context.Background(), "router1")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got.Username != want.Username {
		t.Errorf("Username = %q, want %q", got.Username, want.Username)
	}
	if got.Password != want.Password {
		t.Errorf("Password = %q, want %q", got.Password, want.Password)
	}
}

func TestFileStore_RoundTripPrivateKeyAndPassphrase(t *testing.T) {
	dir := t.TempDir()
	key := testKey('b')

	want := credential.Credential{
		Username:      "deploy",
		PrivateKeyPEM: []byte("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"),
		Passphrase:    "keypass",
	}
	if err := credential.SaveFileStore(dir, key, "server1", want); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	got, err := store.Lookup(context.Background(), "server1")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if string(got.PrivateKeyPEM) != string(want.PrivateKeyPEM) {
		t.Errorf("PrivateKeyPEM = %q, want %q", got.PrivateKeyPEM, want.PrivateKeyPEM)
	}
	if got.Passphrase != want.Passphrase {
		t.Errorf("Passphrase = %q, want %q", got.Passphrase, want.Passphrase)
	}
}

func TestFileStore_MissingDeviceReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	key := testKey('c')

	if err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "admin", Password: "x"}); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	_, err = store.Lookup(context.Background(), "does-not-exist")
	if !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("Lookup() error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestFileStore_MissingFileReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir() // credentials.yaml is never created in this dir
	key := testKey('d')

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	_, err = store.Lookup(context.Background(), "router1")
	if !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("Lookup() error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestFileStore_MissingFileAndMissingDeviceProduceTheSameError(t *testing.T) {
	dirWithFile := t.TempDir()
	key := testKey('i')
	if err := credential.SaveFileStore(dirWithFile, key, "other-device", credential.Credential{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}
	storeWithFile, err := credential.NewFileStore(dirWithFile, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	_, errMissingDevice := storeWithFile.Lookup(context.Background(), "router1")

	dirNoFile := t.TempDir()
	storeNoFile, err := credential.NewFileStore(dirNoFile, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	_, errMissingFile := storeNoFile.Lookup(context.Background(), "router1")

	if !errors.Is(errMissingDevice, credential.ErrNotFound) || !errors.Is(errMissingFile, credential.ErrNotFound) {
		t.Fatalf("expected both cases to satisfy errors.Is(err, ErrNotFound): missing-device=%v missing-file=%v", errMissingDevice, errMissingFile)
	}
}

func TestNewFileStore_WrongLengthKeyIsExplicitError(t *testing.T) {
	dir := t.TempDir()

	_, err := credential.NewFileStore(dir, []byte("too-short"))
	if err == nil {
		t.Fatal("expected an explicit error for a wrong-length key, got nil")
	}
}

func TestNewFileStore_MissingFileIsNotAConstructionError(t *testing.T) {
	// NewFileStore must succeed even when nothing has ever been saved
	// under dir, so a runbook with no SSH-dependent tasks works with
	// zero credential setup.
	dir := t.TempDir()
	key := testKey('j')

	if _, err := credential.NewFileStore(dir, key); err != nil {
		t.Fatalf("NewFileStore() on a directory with no credentials file returned an error: %v", err)
	}
}

func TestFileStore_WrongKeyFailsToDecrypt(t *testing.T) {
	dir := t.TempDir()
	keyA := testKey('a')
	keyB := testKey('b')

	if err := credential.SaveFileStore(dir, keyA, "router1", credential.Credential{Username: "admin", Password: "hunter2"}); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}

	store, err := credential.NewFileStore(dir, keyB)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	got, err := store.Lookup(context.Background(), "router1")
	if err == nil {
		t.Fatalf("expected Lookup with the wrong key to fail, got a credential: %+v", got)
	}
	if errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("expected a decryption error, not ErrNotFound: %v", err)
	}
}

func TestSaveFileStore_OverwritesSameDevice(t *testing.T) {
	dir := t.TempDir()
	key := testKey('e')

	if err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "admin", Password: "old"}); err != nil {
		t.Fatalf("first SaveFileStore() error = %v", err)
	}
	if err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "admin", Password: "new"}); err != nil {
		t.Fatalf("second SaveFileStore() error = %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	got, err := store.Lookup(context.Background(), "router1")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got.Password != "new" {
		t.Fatalf("Password = %q, want %q (overwrite, not duplicate)", got.Password, "new")
	}
}

// TestSaveFileStore_TightensPreexistingDirPermissions is SaveFileStore's
// half of the regression test for FAILURE_PATTERNS.md #22 (see
// master_key_test.go's TestResolveMasterKey_TightensPreexistingDirPermissions
// for the master-key half of the same fix): a .pleiades directory that
// already existed with looser permissions must end up at 0o700 after a
// save, not just when SaveFileStore is the one creating it fresh.
func TestSaveFileStore_TightensPreexistingDirPermissions(t *testing.T) {
	dir := t.TempDir()
	pleiadesDir := filepath.Join(dir, ".pleiades")
	if err := os.MkdirAll(pleiadesDir, 0o777); err != nil {
		t.Fatalf("failed to pre-create %s: %v", pleiadesDir, err)
	}

	if err := credential.SaveFileStore(dir, testKey('f'), "router1", credential.Credential{Username: "admin", Password: "p"}); err != nil {
		t.Fatalf("SaveFileStore() error = %v", err)
	}

	info, err := os.Stat(pleiadesDir)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", pleiadesDir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf(".pleiades directory mode = %o, want 0700 even though it pre-existed with looser permissions", perm)
	}
}

func TestSaveFileStore_SecondDevicePreservesFirst(t *testing.T) {
	dir := t.TempDir()
	key := testKey('f')

	if err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "admin", Password: "r1"}); err != nil {
		t.Fatalf("SaveFileStore(router1) error = %v", err)
	}
	if err := credential.SaveFileStore(dir, key, "router2", credential.Credential{Username: "admin", Password: "r2"}); err != nil {
		t.Fatalf("SaveFileStore(router2) error = %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	got1, err := store.Lookup(context.Background(), "router1")
	if err != nil {
		t.Fatalf("Lookup(router1) error = %v", err)
	}
	if got1.Password != "r1" {
		t.Errorf("router1 Password = %q, want %q", got1.Password, "r1")
	}

	got2, err := store.Lookup(context.Background(), "router2")
	if err != nil {
		t.Fatalf("Lookup(router2) error = %v", err)
	}
	if got2.Password != "r2" {
		t.Errorf("router2 Password = %q, want %q", got2.Password, "r2")
	}
}

// TestSaveFileStore_ReplacesEntryRatherThanMerging documents and proves
// this package's chosen "your call" semantics for SaveFileStore: a save
// replaces a device's whole stored entry, it does not merge new fields
// onto old ones. Switching a device from password to key auth must not
// leave the old, no-longer-intended password sitting on disk next to it.
func TestSaveFileStore_ReplacesEntryRatherThanMerging(t *testing.T) {
	dir := t.TempDir()
	key := testKey('g')

	if err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "admin", Password: "hunter2"}); err != nil {
		t.Fatalf("first SaveFileStore() error = %v", err)
	}
	if err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "admin", PrivateKeyPEM: []byte("key-bytes")}); err != nil {
		t.Fatalf("second SaveFileStore() error = %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	got, err := store.Lookup(context.Background(), "router1")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got.Password != "" {
		t.Errorf("expected the stale password to be cleared after switching to key auth, got %q", got.Password)
	}
	if string(got.PrivateKeyPEM) != "key-bytes" {
		t.Errorf("PrivateKeyPEM = %q, want %q", got.PrivateKeyPEM, "key-bytes")
	}
}

// TestFileStore_LookupReadErrorWhenPleiadesDirIsAFile covers Lookup's
// "real read error, not a missing file" branch: readCredentialsDocument
// must distinguish a file that does not exist (ErrNotFound) from a path
// that cannot be traversed at all for some other reason (a real error).
// Creating a regular file where the .pleiades directory should be forces
// exactly that: opening dir/.pleiades/credentials.yaml fails with
// ENOTDIR, not ENOENT.
func TestFileStore_LookupReadErrorWhenPleiadesDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	key := testKey('k')

	if err := os.WriteFile(filepath.Join(dir, ".pleiades"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("failed to create test fixture: %v", err)
	}

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	_, err = store.Lookup(context.Background(), "router1")
	if err == nil {
		t.Fatal("expected a read error, got nil")
	}
	if errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("expected a real read error, not ErrNotFound: %v", err)
	}
}

// TestSaveFileStore_ReadErrorWhenPleiadesDirIsAFile covers SaveFileStore's
// propagation of a readCredentialsDocument failure that is not a missing
// file, using the same ENOTDIR fixture as the Lookup test above.
func TestSaveFileStore_ReadErrorWhenPleiadesDirIsAFile(t *testing.T) {
	dir := t.TempDir()
	key := testKey('l')

	if err := os.WriteFile(filepath.Join(dir, ".pleiades"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("failed to create test fixture: %v", err)
	}

	err := credential.SaveFileStore(dir, key, "router1", credential.Credential{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected SaveFileStore to fail when the credentials path cannot be read")
	}
}

func TestFileStore_ErrNotFoundMessageMentionsAddCredential(t *testing.T) {
	dir := t.TempDir()
	key := testKey('h')

	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	_, err = store.Lookup(context.Background(), "router1")
	if err == nil || !strings.Contains(err.Error(), "add-credential") {
		t.Fatalf("expected the error to reference add-credential, got %v", err)
	}
}
