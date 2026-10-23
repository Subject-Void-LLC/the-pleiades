// What onboarding may grant each device type, and which of its properties a
// discovery holds for: one table for every type whose capabilities come from
// onboarding, generic or not, so a vendor type that also learns from its
// device (container_host's engine) uses the same rules rather than a copy.
package record

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// DiscoveryRules is what onboarding may do for one device type.
type DiscoveryRules struct {
	// Grants is every capability onboarding may grant the type: the ones
	// its protocol can carry and the type implements. A discovery naming
	// any other is refused when the record is built.
	Grants []capability.Name

	// Bound names the properties a discovery holds for: the ones that
	// decide where a probe went and what it trusted. A discovery records a
	// digest over them (inventory.Discovery.Binding), and the type grants
	// nothing from one whose digest no longer matches its record, however
	// the record changed: set-host, the Controller's PATCH, a sync, or a
	// hand-edited inventory.yaml (Phase 117a, finding S2). Empty means the
	// discovery is not bound, which is right only where something else
	// refuses a repointed device before it is trusted (an SSH host key).
	Bound []string
}

// discoveryRules holds every registered type's rules.
var discoveryRules = registry.New[DiscoveryRules]()

// RegisterDiscoveryRules records deviceType's rules. The type's package
// calls it from init(), beside RegisterType. It panics on a duplicate.
func RegisterDiscoveryRules(deviceType string, r DiscoveryRules) {
	discoveryRules.MustRegister(deviceType, DiscoveryRules{
		Grants: slices.Clone(r.Grants),
		Bound:  slices.Clone(r.Bound),
	})
}

// Discoverable returns the capabilities onboarding may grant a device of
// deviceType, or nil for a type onboarding grants nothing.
func Discoverable(deviceType string) []capability.Name {
	r, _ := discoveryRules.Get(deviceType)
	return slices.Clone(r.Grants)
}

// DiscoveryBinding is the digest a discovery of a deviceType device must
// carry for props to hold it (inventory.Discovery.Binding), or the empty
// string for a type whose discovery is not bound.
func DiscoveryBinding(deviceType string, props inventory.Properties) string {
	r, ok := discoveryRules.Get(deviceType)
	if !ok || len(r.Bound) == 0 {
		return ""
	}
	return inventory.BindingDigest(props, r.Bound)
}

// Declared returns baseline plus the capabilities rec's discovery records,
// refusing a discovery that is malformed or names a capability deviceType
// may not be granted. rec.Capabilities is deliberately not read: a
// classification rule is a claim nobody verified.
//
// A bound type's discovery whose binding does not match rec grants nothing,
// and Declared returns why as stale: the device keeps its baseline, loads,
// and says what to do, rather than failing to load.
//
// The data capabilities rec is granted are added by NewBase (withGrants),
// for every device type, not here.
func Declared(deviceType string, rec Record, baseline []capability.Name) (caps []capability.Name, stale string, err error) {
	return declaredByDiscovery(deviceType, rec, baseline)
}

// withGrants adds to caps the data capabilities rec is granted
// (inventory.GrantedProperty). NewBase calls it, so a grant counts for
// every device type whatever builds its capabilities. A record whose grants
// are malformed (a name that is not a data capability's, an unknown source)
// was not written by RecordGrant, and grants nothing: failing closed. Where
// this process holds a capability's definition (a loaded program defined
// it), a grant counts only when its properties fit the definition and caps
// already has every compiled capability it requires; where it does not (a
// Controller, which loads no program), the name is kept as recorded, and
// grants nothing there, since capability.Implements answers false for a
// name the vocabulary does not hold.
func withGrants(rec Record, caps []capability.Name) []capability.Name {
	grants, ok, err := inventory.GrantsFrom(inventory.NewProperties(rec.Properties))
	if err != nil || !ok {
		return caps
	}
	deviceType := rec.Type
	held := make(map[capability.Name]struct{}, len(caps))
	for _, c := range caps {
		held[c] = struct{}{}
	}
	binding := DiscoveryBinding(deviceType, inventory.NewProperties(rec.Properties))
	var granted []capability.Name
	for _, g := range grants {
		// A probed grant on a bound type holds only while the properties
		// the probe trusted are as they were (a repointed device is probed
		// again before a program sends its credential anywhere new).
		if g.Source == inventory.GrantProbed && binding != "" && g.Binding != binding {
			continue
		}
		if def, defined := capability.DataOf(g.Capability); defined {
			if capability.ValidateGrant(def, g.Properties) != nil || !requiresHeld(def, held) {
				continue
			}
		}
		granted = append(granted, g.Capability)
	}
	return policy.UnionSlices(caps, granted)
}

// requiresHeld reports whether held has every compiled capability def
// requires.
func requiresHeld(def capability.DataDefinition, held map[capability.Name]struct{}) bool {
	for _, req := range def.Requires {
		if !capability.Resolves(held, req) {
			return false
		}
	}
	return true
}

// declaredByDiscovery is Declared before grants: baseline plus what rec's
// discovery proved.
func declaredByDiscovery(deviceType string, rec Record, baseline []capability.Name) (caps []capability.Name, stale string, err error) {
	props := inventory.NewProperties(rec.Properties)
	d, ok, err := inventory.DiscoveryFrom(props)
	if err != nil {
		return nil, "", fmt.Errorf("%s %s: %w", deviceType, rec.Name, err)
	}
	if !ok {
		return baseline, "", nil
	}
	rules, _ := discoveryRules.Get(deviceType)
	for _, c := range d.Capabilities {
		if !slices.Contains(rules.Grants, c) {
			return nil, "", fmt.Errorf("%s %s: its discovery grants %s, which onboarding may not grant this type", deviceType, rec.Name, c)
		}
	}
	// Checked after the capabilities, so a corrupted record is still
	// refused rather than loaded with its baseline.
	if want := DiscoveryBinding(deviceType, props); want != "" && d.Binding != want {
		reason := fmt.Sprintf("device %s's discovery was made against other values of %s; run `pleiades onboard %s` again",
			rec.Name, strings.Join(rules.Bound, ", "), rec.Name)
		if d.Binding == "" {
			reason = fmt.Sprintf("device %s's discovery predates binding a discovery to the address it probed; run `pleiades onboard %s` again",
				rec.Name, rec.Name)
		}
		return baseline, reason, nil
	}
	return policy.UnionSlices(baseline, d.Capabilities), "", nil
}

// DiscoveredFact returns one text fact from props' recorded discovery, or
// the empty string. It is how a type's accessors read what onboarding
// learned, such as generic_ssh's kernel release or a container host's
// engine socket.
func DiscoveredFact(props inventory.Properties, key string) string {
	d, ok, err := inventory.DiscoveryFrom(props)
	if err != nil || !ok {
		return ""
	}
	s, _ := d.Facts[key].(string)
	return s
}

// DiscoveredList returns one list-of-text fact from props' recorded
// discovery, or nil. Onboarding stores a list as []any of strings, the shape
// every inventory store round-trips; anything else in it is dropped rather
// than guessed at.
func DiscoveredList(props inventory.Properties, key string) []string {
	d, ok, err := inventory.DiscoveryFrom(props)
	if err != nil || !ok {
		return nil
	}
	var out []string
	switch v := d.Facts[key].(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}
