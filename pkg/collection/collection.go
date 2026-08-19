package collection

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Method is a Collection method's real implementation: the function a
// runbook task actually invokes.
//
// device is the inventory item this invocation targets, and is nil only for
// a task with no target at all. A method needs it for the same reason
// Ansible modules need a host: a method that cannot tell which device it is
// acting on can only act on something hardcoded. It is pkg/inventory's
// InventoryItem specifically, so a Collection package still imports nothing
// outside pkg/, which is the constraint a third-party Collection will have
// to satisfy once Part X's OCI distribution exists.
//
// It returns Result rather than a bare error so a method can report whether
// it changed anything, which is the Ansible changed/ok distinction the
// engine's convergence principle depends on. Facts and stats flow through
// the RunbookContext instead, since a method may emit many of those and
// they are not the method's return value.
type Method func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (Result, error)

// Result is what a Method reports.
type Result struct {
	// Changed reports whether this invocation altered real state. A
	// read-only method (a facts gatherer) always reports false: reading is
	// not changing, and a method that claimed otherwise would make every
	// run look like it converged something.
	Changed bool
}

// Descriptor is one entry in the collection registry: a fully namespaced
// method name, the Manifest describing what it needs and whether it is
// implemented yet, and the function that runs it.
type Descriptor struct {
	Name     string
	Manifest Manifest

	// Invoke is the method's implementation, or nil for a declared stub
	// that has none yet.
	//
	// It lives on the descriptor rather than in a second parallel registry
	// because the alternative is two tables keyed by the same string that
	// can disagree: one saying a method exists and another saying how to
	// run it. Registration already has the function in scope (a generated
	// package's init sits in the same file as its method), so carrying it
	// here costs nothing and removes a whole class of "registered but not
	// dispatchable" drift.
	//
	// A nil Invoke on a manifest claiming StatusImplemented is a
	// contradiction, and Register rejects it rather than letting it surface
	// at run time as a nil-pointer panic three tasks into a runbook.
	Invoke Method
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
		return fmt.Errorf("collection: %q is not namespaced (requires <namespace>.<method>)", d.Name)
	}

	for _, name := range d.Manifest.RequiredCapabilities {
		if _, known := capability.Lookup(name); !known {
			return fmt.Errorf("collection: %q requires unknown capability %q", d.Name, name)
		}
	}

	// A method claiming to be implemented must carry an implementation.
	// Catching it here turns what would be a nil-pointer panic partway
	// through a runbook into a refusal at process start, which is the same
	// "type safety moves left" rule the rest of this registry follows.
	if d.Manifest.Status == StatusImplemented && d.Invoke == nil {
		return fmt.Errorf("collection: %q claims status %q but carries no implementation", d.Name, StatusImplemented)
	}

	if err := checkReversibility(d); err != nil {
		return err
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
