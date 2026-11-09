// Package dispatch: the Collection identities a dispatch names (Phase 136b),
// wire.DispatchPayload.Collections.
//
// The Controller says, for each first-party group a job's methods belong to,
// which declarations it planned against: the group's identity as its own
// installed catalog gives it. A Runner of another build compares that with
// its own and warns (internal/adapters/native's collection check). Nothing
// here refuses: a Runner runs its own linked code under its own checks.
package dispatch

import (
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// coreVersion is this Controller's build version (buildinfo.Version), read
// once: it reads the binary's build information on every call, and it
// cannot change while the process runs.
var coreVersion = sync.OnceValue(buildinfo.Version)

// collectionIdentities returns the identity of each first-party group a
// method in methods belongs to, keyed by the group's name: what
// collection.BuiltinIdentity gives through this process's registry or, on a
// Controller, which registers no Collection, through the module catalog its
// composition root installed. A method with no identity here adds nothing:
// an external program's (only a Runner's loader knows it), an engine action
// (ssh_exec), or a name nothing declares. The Runner reads a group left out
// as not named. Nil when no method has one.
//
// Each group is digested once per call, however many of its methods the
// list names, since a digest encodes every manifest of the group.
func collectionIdentities(methods []string) map[string]wire.CollectionIdentity {
	var out map[string]wire.CollectionIdentity
	for _, fqcn := range methods {
		group, ok := collection.FirstPartyGroupOf(fqcn)
		if !ok {
			continue
		}
		if _, named := out[group.Name]; named {
			continue
		}
		id, err := collection.BuiltinIdentity(fqcn, coreVersion())
		if err != nil {
			// Not a built-in this Controller knows, which the Runner's own
			// preflight answers for.
			continue
		}
		if out == nil {
			out = map[string]wire.CollectionIdentity{}
		}
		out[id.Group] = id
	}
	return out
}

// rollbackCollections is collectionIdentities over the methods steps run: a
// rollback dispatch runs its undo steps, not the runbook's tasks, so those
// are the Collections it names.
func rollbackCollections(steps []wire.RollbackStep) map[string]wire.CollectionIdentity {
	methods := make([]string, 0, len(steps))
	for _, s := range steps {
		methods = append(methods, s.Method)
	}
	return collectionIdentities(methods)
}
