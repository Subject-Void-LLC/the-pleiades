// What onboarding proved about a device, and the one property it is kept
// in.
package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

	// Binding is BindingDigest over the properties that decide where the
	// probe went and what it trusted (a base URL, its TLS settings), as
	// they were when it ran, for a device type that declares such
	// properties (DiscoveryBinder); empty otherwise. A device type grants
	// nothing from a discovery whose binding no longer matches its record,
	// so a repointed device is onboarded again before its stored
	// credential goes anywhere new (Phase 117a, finding S2).
	Binding string
}

// DiscoveryBinder is a device type whose discovery holds only for the
// properties it was made against. DiscoveryBinding is BindingDigest over
// them as the record holds them now; onboarding stores it in the discovery
// it records.
type DiscoveryBinder interface {
	DiscoveryBinding() string
}

// StaleDiscoverer is a device type that can say why it holds a discovery
// that no longer grants anything: which of its bound properties changed
// since it was onboarded. It answers the empty string when its discovery
// is current or absent.
type StaleDiscoverer interface {
	StaleDiscovery() string
}

// BindingDigest returns a SHA-256 digest, "sha256:<hex>", over keys'
// values in props. Keys are taken in sorted order, each value in the JSON
// form Go writes (map keys sorted), and an absent key is distinct from any
// value, so adding, removing or changing any one of them changes the
// digest. It holds no value in the clear, which matters for a PEM or a
// flag no less than for anything secret-looking.
func BindingDigest(props Properties, keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	raw := props.Raw()
	var b strings.Builder
	for _, k := range sorted {
		b.WriteString(k)
		v, present := raw[k]
		if !present {
			// A NUL cannot appear in JSON text, so an absent key never
			// collides with any present value.
			b.WriteString("\x00absent\n")
			continue
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			// A value JSON cannot encode is still bound, by its Go form, so
			// changing it still changes the digest.
			encoded = []byte(fmt.Sprintf("%#v", v))
		}
		b.WriteString("=")
		b.Write(encoded)
		b.WriteString("\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
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
	out := map[string]any{
		"protocol":     d.Protocol,
		"capabilities": caps,
		"facts":        facts,
		"probed_at":    d.ProbedAt.UTC().Format(time.RFC3339),
	}
	if d.Binding != "" {
		out["binding"] = d.Binding
	}
	return out
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
	if b, ok := m["binding"].(string); ok {
		d.Binding = b
	}
	if at, ok := m["probed_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			d.ProbedAt = t
		}
	}
	sort.Slice(d.Capabilities, func(i, j int) bool { return d.Capabilities[i] < d.Capabilities[j] })
	return d, true, nil
}
