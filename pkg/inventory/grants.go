// The data capabilities granted to a device, and the one property they are
// kept in.
package inventory

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// GrantedProperty holds the capabilities an external program defines as
// data (capability.DataDefinition) that a device has been granted, and how
// each was granted. Like DiscoveredProperty, no person and no sync plugin
// writes it as a property (IsReservedProperty): a grant is written by the
// program's probe during onboarding, or by an operator's explicit
// assertion through its own command and scope, and either way it records
// where it came from.
const GrantedProperty = "granted"

// GrantSource says how a grant was made.
type GrantSource string

const (
	// GrantProbed is a grant the defining program's probe made during
	// onboarding: the device answered its probe.
	GrantProbed GrantSource = "probe"

	// GrantAsserted is a grant an operator made by name, with a reason,
	// that nothing on the device vouched for.
	GrantAsserted GrantSource = "assertion"
)

// Grant is one data capability a device holds.
type Grant struct {
	// Capability is the data capability's name.
	Capability capability.Name

	// Source is how it was granted.
	Source GrantSource

	// Program is the program whose probe made a probed grant.
	Program string

	// Properties are the grant's own, held to the capability's definition.
	Properties map[string]any

	// By and Reason are who asserted an asserted grant, and why.
	By     string
	Reason string

	// At is when it was granted.
	At time.Time
}

// GrantsProperty encodes grants as the value GrantedProperty holds, keyed
// by capability, in plain types every inventory store round-trips.
func GrantsProperty(grants []Grant) map[string]any {
	out := make(map[string]any, len(grants))
	for _, g := range grants {
		props := map[string]any{}
		for k, v := range g.Properties {
			props[k] = v
		}
		entry := map[string]any{
			"source":     string(g.Source),
			"properties": props,
			"at":         g.At.UTC().Format(time.RFC3339),
		}
		if g.Program != "" {
			entry["program"] = g.Program
		}
		if g.By != "" {
			entry["by"] = g.By
		}
		if g.Reason != "" {
			entry["reason"] = g.Reason
		}
		out[string(g.Capability)] = entry
	}
	return out
}

// GrantsFrom decodes props' GrantedProperty, sorted by capability. It
// reports false when the property is absent, and an error when it is
// present but malformed (a name that is not a data capability's, a source
// it does not know), so a device refuses to hydrate from a corrupted
// record rather than read it as "granted nothing".
func GrantsFrom(props Properties) ([]Grant, bool, error) {
	raw, present := props.Raw()[GrantedProperty]
	if !present {
		return nil, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("property %s is not a mapping", GrantedProperty)
	}
	grants := make([]Grant, 0, len(m))
	for name, v := range m {
		if !capability.IsDataName(capability.Name(name)) {
			return nil, true, fmt.Errorf("property %s grants %q, which is not a data capability's name", GrantedProperty, name)
		}
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("property %s's grant of %s is not a mapping", GrantedProperty, name)
		}
		g := Grant{Capability: capability.Name(name), Properties: map[string]any{}}
		source, _ := entry["source"].(string)
		switch GrantSource(source) {
		case GrantProbed, GrantAsserted:
			g.Source = GrantSource(source)
		default:
			return nil, true, fmt.Errorf("property %s's grant of %s has the source %q, which is neither a probe nor an assertion", GrantedProperty, name, source)
		}
		if p, ok := entry["properties"].(map[string]any); ok {
			g.Properties = p
		}
		g.Program, _ = entry["program"].(string)
		g.By, _ = entry["by"].(string)
		g.Reason, _ = entry["reason"].(string)
		if at, ok := entry["at"].(string); ok {
			if t, err := time.Parse(time.RFC3339, at); err == nil {
				g.At = t
			}
		}
		grants = append(grants, g)
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].Capability < grants[j].Capability })
	return grants, true, nil
}

// MaxGrantReason bounds an asserted grant's reason.
const MaxGrantReason = 512

// ValidateGrantReason refuses an asserted grant's reason that is empty, too
// long, or holds a control character. An assertion is a claim nothing on
// the device vouched for, so it always says why.
func ValidateGrantReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("a reason is required: a granted capability is an assertion nothing on the device vouched for, so say why it holds")
	}
	if len(reason) > MaxGrantReason {
		return fmt.Errorf("the reason is longer than %d bytes", MaxGrantReason)
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return errors.New("the reason holds a control character")
		}
	}
	return nil
}
