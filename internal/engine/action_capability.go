package engine

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// ActionCapability is the single, shared fqcn-to-required-capability
// table. Before this table existed, validate.actionCapability
// (internal/validate/capability_rule.go) and cmd/pleiades/run.go's
// TransportBinding map were two independently hand-maintained copies of
// the same fact, and they had already drifted: "ios_backup" was declared
// in the validate-side copy and not the run-side one, so
// pleiades validate passed a runbook that always failed at execution with
// "no in-process implementation yet" (IMPLEMENTATION.md's Phase W3 chain
// audit finding). Both plan-time validation and every TransportBinding
// now key off this exact map, so that specific drift cannot recur.
//
// Phase 31's Collection registry is the eventual single source of truth
// for action metadata generally; until it exists, this table plus
// CheckActionCapabilityBindings is enough.
var ActionCapability = map[string]capability.Name{
	"ssh_exec":   capability.NameSSHTransport,
	"winrm_exec": capability.NameWinRM,
	"ios_backup": capability.NameCiscoIOS,
}

// CheckActionCapabilityBindings verifies that bindings, a composition
// root's map[string]TransportBinding, agrees with ActionCapability
// everywhere it has an opinion: every fqcn bindings can actually execute
// must also be in ActionCapability, with the identical capability.Name.
// Call this once, immediately after building bindings, and treat a
// non-nil error as fatal.
//
// The reverse gap, an ActionCapability entry with no binding ("ios_backup"
// today), is deliberately not an error here: a fqcn can be validated
// before it is executable. What this guards against is the direction that
// actually shipped a defect: a binding whose capability requirement
// disagrees with, or is entirely absent from, what plan-time validation
// checks, which would let a runbook pass pleiades validate and then fail,
// or worse dispatch unchecked, at execution.
func CheckActionCapabilityBindings(bindings map[string]TransportBinding) error {
	for fqcn, binding := range bindings {
		required, ok := ActionCapability[fqcn]
		if !ok {
			return fmt.Errorf("transport binding for fqcn %q has no matching entry in engine.ActionCapability, so plan-time validation would never check it", fqcn)
		}
		if required != binding.Capability {
			return fmt.Errorf("transport binding for fqcn %q requires capability %s but engine.ActionCapability says %s", fqcn, binding.Capability, required)
		}
	}
	return nil
}
