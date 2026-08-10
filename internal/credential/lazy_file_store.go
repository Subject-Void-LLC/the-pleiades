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
type lazyFileStore struct {
	dir string

	once  sync.Once
	store Store
	err   error
}

// NewLazyFileStore returns a Store rooted at dir whose master key
// resolution and file loading happen on first use.
func NewLazyFileStore(dir string) Store {
	return &lazyFileStore{dir: dir}
}

// Lookup implements Store, resolving and caching the underlying store on
// its first call.
func (l *lazyFileStore) Lookup(ctx context.Context, deviceName string) (Credential, error) {
	l.once.Do(func() {
		key, keyErr := ResolveMasterKey(l.dir)
		if keyErr != nil {
			l.err = fmt.Errorf("failed to resolve credential master key: %w", keyErr)
			return
		}
		l.store, l.err = NewFileStore(l.dir, key)
	})
	if l.err != nil {
		return Credential{}, l.err
	}
	return l.store.Lookup(ctx, deviceName)
}
