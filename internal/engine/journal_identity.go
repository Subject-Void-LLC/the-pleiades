// Package engine: the Collection identity a journal entry records for the
// method a node ran (Phase 136b), JournalEntry.Collection.
package engine

import (
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// coreVersion is this build's version, as every binary reports it
// (buildinfo.Version), read once: it reads the binary's build information
// on every call, and it cannot change while the process runs.
var coreVersion = sync.OnceValue(buildinfo.Version)

// collectionIdentity is the identity JournalEntry.Collection records for a
// node that ran resolved's method: the identity the loader set on an
// external program's descriptor (provider), or the identity of the
// first-party group a built-in belongs to. Zero for an engine action and an
// unresolved name, which no Collection provides.
//
// A built-in's is computed once per method per run, since GroupDigest
// encodes every manifest of the group: a run of many nodes would otherwise
// pay for it on each. It is touched only by projectResult, which recordLevel
// calls at the level barrier, so the cache needs no lock (see run.sequence).
func (r *run) collectionIdentity(resolved resolvedFQCN, provider *collection.Provider) wire.CollectionIdentity {
	if provider != nil {
		return provider.Identity
	}
	if resolved.Unresolved || resolved.engineAction {
		return wire.CollectionIdentity{}
	}
	if id, ok := r.identities[resolved.Name]; ok {
		return id
	}
	// An error is a name no first-party group holds, which
	// internal/archtest's TestEveryBuiltinHasAnIdentity rules out for every
	// built-in; the entry then records no identity, which a rollback reads as
	// one recorded before the field existed and checks against this build's
	// declarations alone.
	id, _ := collection.BuiltinIdentity(resolved.Name, coreVersion())
	if r.identities == nil {
		r.identities = map[string]wire.CollectionIdentity{}
	}
	r.identities[resolved.Name] = id
	return id
}

// inverseSpecDigest is JournalEntry.InverseSpecDigest for an undo through
// inverse's method that spec declares: collection.UndoDigest over spec and
// the parameters that method declares, its fragments' included. It reads the
// method through collection.ManifestOf, as the Controller's rollback planner
// will to compute the same digest from its installed catalog. Empty when no
// manifest is known, which a declared, resolved undo never is.
func inverseSpecDigest(spec sdk.InverseSpec, inverse resolvedFQCN) string {
	target, ok := collection.ManifestOf(inverse.Name)
	if !ok {
		return ""
	}
	return collection.UndoDigest(spec, fragment.ByNameIn(target))
}
