package collection

import (
	"fmt"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/registry"
)

// Descriptor is one entry in the collection registry: a fully namespaced
// method name and the Manifest describing what it needs and whether it is
// implemented yet.
type Descriptor struct {
	Name     string
	Manifest Manifest
}

// collections holds every registered Collection method. It is built on
// pkg/registry.Registry (Phase 6's Section 25 "typed generic Registry"),
// so this package is that primitive's third consumer, not a second
// Registry implementation sitting beside it.
var collections = registry.New[Descriptor]()

// Register adds d under d.Name, rejecting a bare (non-namespaced) name, an
// empty namespace or method segment, an unknown required capability, or a
// duplicate name. It returns an error rather than panicking, for genuine
// runtime registration where a rejection is a data problem the caller must
// handle, not a process-ending programmer error.
//
// Decision, recorded per this phase's own checklist requirement: a
// duplicate name is always rejected, never resolved by first-write-wins or
// last-write-wins. Part X's Phase 44 records that a decentralized registry
// makes namespace collision a routine outcome, not a bug, because no
// registry can enforce ownership of a name it has never heard of.
// First-write-wins is a hijack primitive once a colliding name can arrive
// from a stranger; last-write-wins is worse. Neither wins here: the second
// registration simply fails, and the caller decides what that means for
// its own workflow.
func Register(d Descriptor) error {
	namespace, method, ok := strings.Cut(d.Name, ".")
	if !ok || namespace == "" || method == "" {
		return fmt.Errorf("collection: %q is not namespaced (PLAN.md Section 2 requires <namespace>.<method>)", d.Name)
	}

	for _, name := range d.Manifest.RequiredCapabilities {
		if _, known := capability.Lookup(name); !known {
			return fmt.Errorf("collection: %q requires unknown capability %q", d.Name, name)
		}
	}

	return collections.Register(d.Name, d)
}

// MustRegister calls Register and panics on error. This is for
// compile-time-known registrations made from a Collection package's own
// init(), mirroring pkg/capability's and pkg/registry's own convention: a
// malformed or duplicate built-in name is a programming error that must
// fail loudly at process start, never silently pick one registration over
// the other.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic("collection: " + err.Error())
	}
}

// Lookup returns the Descriptor registered under name, and whether it was
// found.
func Lookup(name string) (Descriptor, bool) {
	return collections.Get(name)
}
