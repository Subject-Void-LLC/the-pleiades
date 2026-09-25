// Tests for the discovery property's encoding: it round-trips through the
// plain values every store keeps, and a malformed one is refused rather
// than read as "discovered nothing".
package inventory_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func discovered(v inventory.PropertyValue) inventory.Properties {
	return inventory.NewProperties(map[string]inventory.PropertyValue{inventory.DiscoveredProperty: v})
}

// TestDiscovery_RoundTrips encodes a discovery and reads it back, with its
// capabilities sorted and its time kept to the second.
func TestDiscovery_RoundTrips(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 30, 45, 0, time.UTC)
	d := inventory.Discovery{
		Protocol:     "netconf",
		Capabilities: []capability.Name{capability.NameNetconf, capability.NameHTTPAPI},
		Facts:        map[string]any{"framing": "chunked", "urns": []any{"a", "b"}},
		ProbedAt:     at,
	}
	got, ok, err := inventory.DiscoveryFrom(discovered(d.Property()))
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	want := d
	want.Capabilities = []capability.Name{capability.NameHTTPAPI, capability.NameNetconf}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if !inventory.IsReservedProperty(inventory.DiscoveredProperty) || inventory.IsReservedProperty("host") {
		t.Error("the reserved set is wrong")
	}
}

// TestDiscoveryFrom_Shapes covers an absent property, the []string form
// a Go caller may store, and each malformed shape.
func TestDiscoveryFrom_Shapes(t *testing.T) {
	if _, ok, err := inventory.DiscoveryFrom(inventory.NewProperties(nil)); ok || err != nil {
		t.Errorf("absent: ok %v, err %v", ok, err)
	}
	got, ok, err := inventory.DiscoveryFrom(discovered(map[string]any{"capabilities": []string{"GRPCCapable"}, "probed_at": "not a time"}))
	if err != nil || !ok || len(got.Capabilities) != 1 || !got.ProbedAt.IsZero() {
		t.Errorf("[]string form: %+v, %v, %v", got, ok, err)
	}
	for name, v := range map[string]inventory.PropertyValue{
		"not a mapping":         "yes",
		"capabilities a string": map[string]any{"capabilities": "GRPCCapable"},
		"a capability a number": map[string]any{"capabilities": []any{7}},
	} {
		if _, ok, err := inventory.DiscoveryFrom(discovered(v)); err == nil || !ok {
			t.Errorf("%s: ok %v, err %v, want a refusal", name, ok, err)
		}
	}
}
