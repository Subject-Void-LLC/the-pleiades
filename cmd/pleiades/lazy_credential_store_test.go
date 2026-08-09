package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestLazyCredentialStore_DoesNotTouchDiskUntilLookup confirms
// constructing a lazyCredentialStore has zero side effects, so a runbook
// with no SSH-dependent tasks never creates .pleiades/master.key just
// because run.go built one.
func TestLazyCredentialStore_DoesNotTouchDiskUntilLookup(t *testing.T) {
	dir := t.TempDir()
	_ = newLazyCredentialStore(dir)

	if _, err := os.Stat(filepath.Join(dir, ".pleiades")); err == nil {
		t.Error("expected constructing a lazyCredentialStore not to create .pleiades")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error checking .pleiades: %v", err)
	}
}

// TestLazyCredentialStore_LookupCreatesKeyAndReturnsNotFound confirms the
// first Lookup call is what actually resolves the master key (creating it
// if absent) and reads the (here, absent) credentials file, returning
// credential.ErrNotFound for a device nothing was ever stored for.
func TestLazyCredentialStore_LookupCreatesKeyAndReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	store := newLazyCredentialStore(dir)

	_, err := store.Lookup(context.Background(), "webserver1")
	if !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("expected credential.ErrNotFound, got: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".pleiades", "master.key")); statErr != nil {
		t.Errorf("expected the first Lookup to create master.key: %v", statErr)
	}
}

// TestLazyCredentialStore_FindsCredentialSavedBeforeLookup confirms the
// lazily-resolved store reads whatever credential.SaveFileStore already
// wrote to the same dir, the real path add-credential and run share.
func TestLazyCredentialStore_FindsCredentialSavedBeforeLookup(t *testing.T) {
	dir := t.TempDir()

	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatalf("failed to resolve master key: %v", err)
	}
	if err := credential.SaveFileStore(dir, key, "webserver1", credential.Credential{Username: "admin", Password: "s3cret"}); err != nil {
		t.Fatalf("failed to save fixture credential: %v", err)
	}

	store := newLazyCredentialStore(dir)
	cred, err := store.Lookup(context.Background(), "webserver1")
	if err != nil {
		t.Fatalf("expected the pre-saved credential to be found, got: %v", err)
	}
	if cred.Username != "admin" || cred.Password != "s3cret" {
		t.Errorf("expected the pre-saved credential's fields, got %+v", cred)
	}
}

// TestLazyCredentialStore_MalformedMasterKeyEnvIsActionable confirms a
// deliberately but incorrectly set PLEIADES_MASTER_KEY surfaces a clear
// error through the lazy path, on every call, rather than only the first
// or being silently swallowed by sync.Once caching a zero-value store.
func TestLazyCredentialStore_MalformedMasterKeyEnvIsActionable(t *testing.T) {
	t.Setenv("PLEIADES_MASTER_KEY", "not-valid-base64!!")

	dir := t.TempDir()
	store := newLazyCredentialStore(dir)

	for i := 0; i < 2; i++ {
		if _, err := store.Lookup(context.Background(), "webserver1"); err == nil {
			t.Fatalf("call %d: expected a malformed master key to be a hard error", i)
		}
	}
}
