// What onboarding proved about a device, and the one property it is kept
// in.
package inventory

import (
	"fmt"
	"sort"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// DiscoveredProperty is the property onboarding writes. It holds what a
// device proved about itself over its own protocol, and a generic device
// type derives its capabilities from it.
//
// It is the one property no person and no sync plugin may write: the
// capabilities it grants are worth something only because the device
// vouched for them. Every inventory write path refuses it
// (IsReservedProperty); onboarding writes it through record.Base's own
// method, with a revision like any other change.
const DiscoveredProperty = "discovered"

// IsReservedProperty reports whether key is written only by the platform,
// never by a person or a sync plugin.
func IsReservedProperty(key string) bool {
	return key == DiscoveredProperty
}

// Discovery is what one onboarding proved: the protocol it spoke, the
// capabilities the device's own report supports, facts worth keeping (a
// NETCONF server's capability URNs, a gRPC server's services), and when.
type Discovery struct {
	Protocol     string
	Capabilities []capability.Name
	Facts        map[string]any
	ProbedAt     time.Time
}

// Property encodes d as the value DiscoveredProperty holds, in plain types
// every inventory store round-trips (YAML and JSON alike).
func (d Discovery) Property() map[string]any {
	caps := make([]any, 0, len(d.Capabilities))
	for _, c := range d.Capabilities {
		caps = append(caps, string(c))
	}
	facts := map[string]any{}
	for k, v := range d.Facts {
		facts[k] = v
	}
	return map[string]any{
		"protocol":     d.Protocol,
		"capabilities": caps,
		"facts":        facts,
		"probed_at":    d.ProbedAt.UTC().Format(time.RFC3339),
	}
}

// DiscoveryFrom decodes props' DiscoveredProperty. It reports false when
// the property is absent, and an error when it is present but malformed,
// so a generic type can refuse to hydrate from a corrupted record rather
// than read it as "discovered nothing".
func DiscoveryFrom(props Properties) (Discovery, bool, error) {
	raw, present := props.Raw()[DiscoveredProperty]
	if !present {
		return Discovery{}, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return Discovery{}, true, fmt.Errorf("property %s is not a mapping", DiscoveredProperty)
	}
	d := Discovery{Facts: map[string]any{}}
	if p, ok := m["protocol"].(string); ok {
		d.Protocol = p
	}
	switch caps := m["capabilities"].(type) {
	case nil:
	case []any:
		for _, c := range caps {
			s, ok := c.(string)
			if !ok {
				return Discovery{}, true, fmt.Errorf("property %s lists a capability that is not text", DiscoveredProperty)
			}
			d.Capabilities = append(d.Capabilities, capability.Name(s))
		}
	case []string:
		for _, s := range caps {
			d.Capabilities = append(d.Capabilities, capability.Name(s))
		}
	default:
		return Discovery{}, true, fmt.Errorf("property %s's capabilities is not a list", DiscoveredProperty)
	}
	if f, ok := m["facts"].(map[string]any); ok {
		d.Facts = f
	}
	if at, ok := m["probed_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			d.ProbedAt = t
		}
	}
	sort.Slice(d.Capabilities, func(i, j int) bool { return d.Capabilities[i] < d.Capabilities[j] })
	return d, true, nil
}
