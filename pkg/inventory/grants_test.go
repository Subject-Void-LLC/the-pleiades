// Tests for the granted property's encoding: a grant round-trips through
// the plain values every store keeps, and a malformed one is refused
// rather than read as "granted nothing".
package inventory_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func granted(v inventory.PropertyValue) inventory.Properties {
	return inventory.NewProperties(map[string]inventory.PropertyValue{inventory.GrantedProperty: v})
}

// TestGrants_RoundTrip encodes a probed and an asserted grant and reads
// them back sorted by capability, every field kept.
func TestGrants_RoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	grants := []inventory.Grant{
		{Capability: "acme.zeta", Source: inventory.GrantAsserted, By: "ops", Reason: "a lab box", At: at, Properties: map[string]any{}},
		{Capability: "acme.alpha", Source: inventory.GrantProbed, Program: "acme", Binding: "sha256:ab", At: at, Properties: map[string]any{"model": "x9"}},
	}
	got, ok, err := inventory.GrantsFrom(granted(inventory.GrantsProperty(grants)))
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	want := []inventory.Grant{grants[1], grants[0]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if !inventory.IsReservedProperty(inventory.GrantedProperty) {
		t.Error("granted is not reserved")
	}
}

// TestGrantsFrom_Shapes covers an absent property, an entry with no
// properties or time, and each malformed shape.
func TestGrantsFrom_Shapes(t *testing.T) {
	if _, ok, err := inventory.GrantsFrom(inventory.NewProperties(nil)); ok || err != nil {
		t.Errorf("absent: ok %v, err %v", ok, err)
	}
	got, _, err := inventory.GrantsFrom(granted(map[string]any{"acme.bare": map[string]any{"source": "assertion", "at": "not a time"}}))
	if err != nil || len(got) != 1 || len(got[0].Properties) != 0 || !got[0].At.IsZero() {
		t.Errorf("a bare entry = %+v, %v; want one grant with no properties and no time", got, err)
	}
	for name, v := range map[string]inventory.PropertyValue{
		"not a mapping":          []any{"acme.x"},
		"not a data capability":  map[string]any{"SSHTransport": map[string]any{"source": "probe"}},
		"an entry not a mapping": map[string]any{"acme.x": "probe"},
		"an unknown source":      map[string]any{"acme.x": map[string]any{"source": "rumour"}},
	} {
		if _, ok, err := inventory.GrantsFrom(granted(v)); !ok || err == nil {
			t.Errorf("%s: ok %v, err %v; want present and refused", name, ok, err)
		}
	}
}

// TestValidateGrantReason: an assertion always says why, briefly, in
// printable text.
func TestValidateGrantReason(t *testing.T) {
	if err := inventory.ValidateGrantReason("the vendor's API answers on 443"); err != nil {
		t.Errorf("a plain reason: %v", err)
	}
	for name, reason := range map[string]string{
		"empty":          "  ",
		"too long":       strings.Repeat("x", inventory.MaxGrantReason+1),
		"a control char": "line one\nline two",
	} {
		if err := inventory.ValidateGrantReason(reason); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestReservedPropertyWriter names each reserved property's own writer.
func TestReservedPropertyWriter(t *testing.T) {
	if got := inventory.ReservedPropertyWriter(inventory.GrantedProperty); !strings.Contains(got, "grant-capability") {
		t.Errorf("granted: %q", got)
	}
	if got := inventory.ReservedPropertyWriter(inventory.DiscoveredProperty); got != "onboarding" {
		t.Errorf("discovered: %q", got)
	}
}
