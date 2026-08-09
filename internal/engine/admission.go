// Package engine: device admission checks. LifecycleAdmits and
// CapabilityAdmits, extracted from executor.go's own previously inline
// lifecycle check, are the shared contract for whether a device is a
// valid target for real work. The Walk-tier engine.Executor calls them
// directly; the Crawl-tier dispatch.Worker (internal/dispatch/worker.go)
// calls them across the package boundary, so the two tiers never diverge
// on what makes a device admissible.
package engine

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// LifecycleAdmits reports whether dev's current lifecycle state permits a
// runbook to run real work against it, and, if not, why. This is the exact
// check runNode's device-resolution loop used to run inline before this
// function existed (the runtime half of the chain-audit lifecycle finding,
// IMPLEMENTATION.md Phase W5); pulling it out here gives the check a name
// and a location any future caller can reuse without re-deriving it, and
// without changing its behavior or its exact reason wording one bit, since
// executor_test.go already asserts on that wording.
//
// A true result always pairs with an empty reason string. A false result
// always pairs with a non-empty reason naming dev and its actual state, in
// the identical format runNode has always used for a lifecycle skip's
// NodeResult.SkipReason and published event message.
func LifecycleAdmits(dev inventory.InventoryItem) (bool, string) {
	if dev.State().CanExecute() {
		return true, ""
	}
	// Copied verbatim from the inline check this replaces: any drift here
	// would silently change every lifecycle skip's SkipReason text and the
	// event message runNode publishes alongside it.
	return false, fmt.Sprintf("device %q is %s, not active", dev.Name(), dev.State())
}

// CapabilityAdmits reports whether dev declares every capability in
// required, and, if not, why. It is the same fact
// validate.CapabilityRule checks at plan time (internal/validate/capability_rule.go),
// asked here at the granularity of one already-resolved device against an
// explicit capability list, so a caller that already has both in hand does
// not need to reconstruct a whole WorldView and DAG walk just to ask this
// one question.
//
// required is checked in order, but CapabilityAdmits does not report the
// first entry checked, it reports the first entry dev is actually missing:
// a satisfied requirement earlier in the list never masks an unsatisfied
// one later in it. An empty required list always admits, including a nil
// one: a task with no declared capability requirement has nothing to
// check.
func CapabilityAdmits(dev inventory.InventoryItem, required []capability.Name) (bool, string) {
	for _, name := range required {
		if !dev.HasCapability(name) {
			// Wording mirrors validate.CapabilityRule's own Finding.Message
			// ("device %q does not have capability %s, required by task
			// %s"), minus the task clause: CapabilityAdmits is handed a
			// device and a capability list, not a task to name.
			return false, fmt.Sprintf("device %q does not have capability %s", dev.Name(), name)
		}
	}
	return true, ""
}
