package credential

import (
	"context"
	"fmt"
	"sync"
)

// lazyFileStore defers resolving the AES-256 master key and constructing
// the underlying file-backed Store until the first Lookup call, rather
// than doing either at composition-root startup. This preserves the "zero
// credential setup" promise NewFileStore's own doc comment makes for a
// runbook with no SSH-dependent tasks: without this wrapper, every
// composition root that builds a credential.Store would create
// .pleiades/master.key on disk (or fail on a malformed
// PLEIADES_MASTER_KEY) even for a run that never actually looks up a
// credential.
//
// This type originated in cmd/pleiades (lazy_credential_store.go) and was
// promoted here, unchanged, so cmd/controller (Phase 16, Native Go
// Execution Adapter: the Controller now resolves a device's credential at
// dispatch time to attach it to wire.DispatchPayload) has exactly one
// implementation to construct instead of a second, hand-kept-in-sync
// copy. cmd/pleiades keeps calling this constructor directly rather than
// through a package-local wrapper.
//
// Only an opened store is kept. A refusal (a key that is missing beside
// sealed material, or unusable) is not: the next lookup tries again, so a
// long-lived process (a Controller) whose key file is restored, or fixed with
// chmod 600, needs no restart.
type lazyFileStore struct {
	dir string

	mu    sync.Mutex
	store Store
}

// NewLazyFileStore returns a Store rooted at dir whose master key
// resolution and file loading happen on first use.
func NewLazyFileStore(dir string) Store {
	return &lazyFileStore{dir: dir}
}

// Lookup implements Store, resolving and caching the underlying store on
// its first call.
func (l *lazyFileStore) Lookup(ctx context.Context, deviceName string) (Credential, error) {
	if err := l.open(); err != nil {
		return Credential{}, err
	}
	return l.store.Lookup(ctx, deviceName)
}

// open resolves the master key and opens the store, keeping it once opened
// and trying again on the next call after a failure.
func (l *lazyFileStore) open() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store != nil {
		return nil
	}
	key, err := ResolveMasterKey(l.dir)
	if err != nil {
		return fmt.Errorf("failed to resolve credential master key: %w", err)
	}
	store, err := NewFileStore(l.dir, key)
	if err != nil {
		return err
	}
	l.store = store
	return nil
}
