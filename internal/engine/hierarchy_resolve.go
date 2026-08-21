package engine

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// ResolveHierarchy folds base and ancestry (inventory.Repository's own
// GroupAncestry result: every group and inventory a device is reachable
// through, ordered least specific first) through pkg/policy.Resolve using
// ModeOverride, the "more specific replaces the accumulated value
// outright" combinator (AGENTS.md's hierarchical policy principle).
//
// extract pulls this call's own T out of one layer's Properties bag
// (internal/ent/schema's Group.properties or Inventory.properties, an
// untyped map since more than one setting will eventually live there),
// reporting false when that layer has no opinion on it. A layer with no
// opinion contributes nothing rather than overriding with a zero value:
// internal/launch.Config's own doc comment states the identical rule for
// the same reason ("An absent field inherits what the layer beneath it
// decided; a field present with an empty value sets it to empty.
// Collapsing those two would mean a saved configuration that omits
// `limit` silently clearing the template's"). Without this, a group with
// real child groups but nothing configured for this particular setting
// would silently blank out whatever a less specific ancestor already
// set, every time.
//
// base is the System-level default: pkg/policy.Resolve's own base
// parameter, sourced from the caller (a composition root's config), not
// from any stored entity. A device's own override is deliberately not
// folded in here: the caller already holds the resolved device (it is
// what asked for this device's ancestry in the first place) and can
// apply its own Properties as the final, most specific layer with the
// identical extract function, one call to policy.Override, no second
// resolver needed.
func ResolveHierarchy[T any](base T, ancestry []inventory.HierarchyLayer, extract func(map[string]interface{}) (T, bool)) policy.Result[T] {
	layers := make([]policy.Layer[T], 0, len(ancestry))
	for _, a := range ancestry {
		if v, ok := extract(a.Properties); ok {
			layers = append(layers, policy.Layer[T]{Name: a.Name, Value: v})
		}
	}
	return policy.Resolve(policy.ModeOverride, base, layers, policy.Override)
}
