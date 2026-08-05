package inventory

import (
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// ResolveHostType returns the device type h should hydrate as: h.Type
// unchanged when set (Type always wins once present, whether written by
// add-host or by hand), or the result of classifying h.Classify against rs
// otherwise. It returns "" with a nil error when neither is set, matching
// this package's existing behavior of letting ItemFactory.Build raise its
// own "record has no device type" error rather than duplicating that
// message here.
//
// Both fileRepository.buildRecord and HydrateHosts call this rather than
// reading h.Type directly, so a hand-edited inventory.yaml entry carrying
// only "classify:" resolves identically through either hydration path.
// add-host itself resolves eagerly at write time and persists both Type
// and Classify (Classify surviving only as provenance): PLAN.md
// Architecture Principle 5 ("type safety moves left") means a bad
// classify path should fail loudly at add-host time, not silently three
// commands later at validate or run. For the future ent-backed create
// path, resolving before the first write is not just cleaner, it is the
// only structurally possible timing: internal/ent/schema/device.go's type
// column is Immutable, and the generated DeviceUpdate/DeviceUpdateOne
// builders carry no SetType method at all, so there is no update call
// this resolution could ever move to later even if a future change wanted
// it to.
func ResolveHostType(h HostSpec, rs *classification.RuleSet) (string, error) {
	if h.Type != "" {
		return h.Type, nil
	}
	if len(h.Classify) == 0 {
		return "", nil
	}

	result, err := rs.Classify(h.Classify)
	if err != nil {
		return "", fmt.Errorf("host %q: %w", h.Name, err)
	}
	if result.Value.Type == nil {
		return "", fmt.Errorf("host %q: classification path %v matched but assigned no type", h.Name, h.Classify)
	}
	return *result.Value.Type, nil
}

// ResolveHostCapabilities returns the capability set h's Classify path
// assigns (Phase 32's capability granularity decision), or nil with a nil
// error when there is no Classify path to walk. It short-circuits on
// h.Type exactly like ResolveHostType does, returning nil, nil without
// touching Classify at all: TestHydrateHosts_TypeWinsOverClassify already
// pins "Classify survives only as provenance and is never re-consulted
// once Type is present" as a hard invariant (an intentionally
// unresolvable Classify paired with a valid Type must not error), and
// that invariant has to hold for capabilities too, not just Type, or the
// two resolvers would disagree about what "provenance only" means for the
// same HostSpec. The practical consequence: a host add-host persisted with
// both Type and Classify does not gain classification-derived capabilities
// on later hydration (its vendor constructor's baseline is all it gets);
// only a HostSpec carrying Classify with no Type -- the hand-edited-YAML
// case ResolveHostType's own doc comment already calls out -- exercises
// this path. Callers that need a non-empty capability set regardless (a
// vendor constructor's own baseline) are responsible for that, the same
// way they are responsible for a resolved Type today.
func ResolveHostCapabilities(h HostSpec, rs *classification.RuleSet) ([]capability.Name, error) {
	if h.Type != "" {
		return nil, nil
	}
	if len(h.Classify) == 0 {
		return nil, nil
	}

	result, err := rs.Classify(h.Classify)
	if err != nil {
		return nil, fmt.Errorf("host %q: %w", h.Name, err)
	}
	return result.Value.Capabilities, nil
}
