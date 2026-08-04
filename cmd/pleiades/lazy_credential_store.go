package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/SubjectVoidLLC/the-pleiades/internal/credential"
)

// lazyCredentialStore defers resolving the AES-256 master key and
// constructing the underlying file-backed credential.Store until the
// first Lookup call, rather than doing either at composition-root
// startup. This preserves the "zero credential setup" promise
// credential.NewFileStore's own doc comment makes for a runbook with no
// SSH-dependent tasks: without this wrapper, every `pleiades run`
// invocation would create .pleiades/master.key on disk (or fail on a
// malformed PLEIADES_MASTER_KEY) even for a runbook that never actually
// looks up a credential.
type lazyCredentialStore struct {
	dir string

	once  sync.Once
	store credential.Store
	err   error
}

// newLazyCredentialStore returns a credential.Store rooted at dir whose
// master key resolution and file loading happen on first use.
func newLazyCredentialStore(dir string) credential.Store {
	return &lazyCredentialStore{dir: dir}
}

// Lookup implements credential.Store, resolving and caching the
// underlying store on its first call.
func (l *lazyCredentialStore) Lookup(ctx context.Context, deviceName string) (credential.Credential, error) {
	l.once.Do(func() {
		key, keyErr := credential.ResolveMasterKey(l.dir)
		if keyErr != nil {
			l.err = fmt.Errorf("failed to resolve credential master key: %w", keyErr)
			return
		}
		l.store, l.err = credential.NewFileStore(l.dir, key)
	})
	if l.err != nil {
		return credential.Credential{}, l.err
	}
	return l.store.Lookup(ctx, deviceName)
}
