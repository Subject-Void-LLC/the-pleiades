// Package engine: the Collection identity a journal entry records for the
// method a node ran (Phase 136b), JournalEntry.Collection.
package engine

import (
	"log/slog"
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
// Only an identity that passes wire.CollectionIdentity.Valid is recorded,
// so every identity a journal holds passes Shape, the part of Valid its
// readers hold it to, under this toolchain and every later one. A
// program's file name the loader admits can hold what Valid refuses (a
// format character) or bytes JSON cannot carry (invalid UTF-8, which
// encoding/json replaces), and the loader does not refuse one (an approved
// program loads as before); such a program's entries record no identity,
// keep its ProviderProgram and ProviderDigest, and are warned about once.
//
// A built-in's is computed once per method per run, since GroupDigest
// encodes every manifest of the group: a run of many nodes would otherwise
// pay for it on each. It is touched only by projectResult, which recordLevel
// calls, and by factProvenance, which recordFacts calls (Phase 136b's fact
// provenance names the same identity). Both run on the walk's own goroutine:
// at each level barrier after runConcurrently has returned, in undoBlock's
// steps, and in Run's deferred drain of uncollected jobs, never from inside
// a node, so the cache needs no lock (see run.sequence).
func (r *run) collectionIdentity(resolved resolvedFQCN, provider *collection.Provider) wire.CollectionIdentity {
	if resolved.Unresolved || resolved.engineAction {
		return wire.CollectionIdentity{}
	}
	if id, ok := r.identities[resolved.Name]; ok {
		return id
	}
	var id wire.CollectionIdentity
	var err error
	if provider != nil {
		id = provider.Identity
		err = id.Valid()
	} else {
		// internal/archtest's TestEveryBuiltinHasAnIdentity holds every
		// built-in to resolve, so an error here is a build out of step with
		// its own registry.
		id, err = collection.BuiltinIdentity(resolved.Name, coreVersion())
	}
	if err != nil {
		// Recorded as none, which a rollback cannot tell from an entry
		// written before the field existed: said here, once per method per
		// run, rather than left to be discovered from the journal.
		slog.Warn("the run journal records no Collection identity for this method", "run", r.runID, "fqcn", resolved.Name, "reason", err)
		id = wire.CollectionIdentity{}
	}
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
