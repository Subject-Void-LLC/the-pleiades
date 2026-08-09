package syncplugin

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/classification"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Classification is what Classify resolves one discovered Record into: the
// device type to hydrate it as, the capabilities that type may advertise,
// and the lifecycle state it should land in.
//
// State is part of the classification rather than a separate decision
// because the two are the same judgment. A device that classified cleanly
// is onboardable; one that did not is quarantined; one whose source is
// read-only is simulate-locked. Splitting them would let a caller record a
// device as quarantined and active at once.
type Classification struct {
	// Type is the record.RegisterType key to hydrate this device as (e.g.
	// "cisco_switch"). It is empty only on a quarantined Classification.
	Type string

	// Capabilities is the capability set the classification rules granted.
	// It accumulates down the rule tree (Section 6d inherits and unions),
	// and a concrete vendor constructor may add its own baseline on top.
	Capabilities []capability.Name

	// State is the lifecycle state the device lands in.
	State inventory.LifecycleState

	// Reason explains a non-active State. It is required for a quarantined
	// classification and empty otherwise, so a device sitting in the
	// quarantine bucket can always answer "why" without a log dive.
	Reason string
}

// Quarantined reports whether this Classification sends the device to the
// Section 6g quarantine bucket for manual review.
func (c Classification) Quarantined() bool {
	return c.State == inventory.StateQuarantined
}

// Quarantine builds the Classification for a device that could not be
// placed. It carries no Type and no capabilities on purpose: guessing a
// type for an unclassifiable device is how a device ends up hydrated as
// something it is not, and Section 6g wants it visible and unassigned
// rather than plausibly wrong.
func Quarantine(reason string) Classification {
	return Classification{
		State:  inventory.StateQuarantined,
		Reason: reason,
	}
}

// ClassifyPath resolves a Section 6d classification path (e.g.
// ["network_device", "cisco", "ios"]) against rs into a Classification in
// state. Every plugin routes through this rather than switching on vendor
// strings itself, which is what keeps the rule tree the single place device
// classification is decided; a plugin's real job is deciding which path a
// raw upstream record maps to, not what that path means.
//
// A path that fails to resolve, or that resolves without assigning a type,
// comes back Quarantined rather than as an error. That is the Section 6g
// contract: the sync must continue and the device must stay visible.
func ClassifyPath(rs *classification.RuleSet, path []string, state inventory.LifecycleState) Classification {
	if len(path) == 0 {
		return Quarantine("no classification path could be derived from the upstream record")
	}

	result, err := rs.Classify(path)
	if err != nil {
		return Quarantine(fmt.Sprintf("classification path %v did not resolve: %v", path, err))
	}
	if result.Value.Type == nil {
		return Quarantine(fmt.Sprintf("classification path %v matched but assigned no device type", path))
	}

	return Classification{
		Type:         *result.Value.Type,
		Capabilities: result.Value.Capabilities,
		State:        state,
	}
}
