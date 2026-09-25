package inventory

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
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
// error when there is no Classify path to walk.
//
// When h also has a Type (add-host always writes both), Type still wins,
// as it does for ResolveHostType, and the path contributes capabilities
// only when it resolves to that same type. A path that does not resolve,
// or resolves to a different type, contributes nothing and is not an
// error: TestHydrateHosts_TypeWinsOverClassify pins that a stale or
// unresolvable Classify beside a valid Type never breaks hydration.
//
// This used to return nil whenever Type was set, treating the path as
// provenance only. Since add-host writes both, every host classified at
// add time lost its classification's capabilities on load: a
// linux_server added with --classify linux_server,debian_family never
// declared AptCapable, which is half of why pkg.apt.* could never run
// (FAILURE_PATTERNS 344).
func ResolveHostCapabilities(h HostSpec, rs *classification.RuleSet) ([]capability.Name, error) {
	if len(h.Classify) == 0 {
		return nil, nil
	}

	result, err := rs.Classify(h.Classify)
	if h.Type != "" {
		if err != nil || result.Value.Type == nil || *result.Value.Type != h.Type {
			return nil, nil
		}
		return result.Value.Capabilities, nil
	}
	if err != nil {
		return nil, fmt.Errorf("host %q: %w", h.Name, err)
	}
	return result.Value.Capabilities, nil
}
