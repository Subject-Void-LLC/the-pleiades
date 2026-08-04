package inventory

import (
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/classification"
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
