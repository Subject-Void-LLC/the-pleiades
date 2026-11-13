// Tests for the granted property's encoding: a grant round-trips through
// the plain values every store keeps, and a malformed one is refused
// rather than read as "granted nothing".
package inventory_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

func granted(v inventory.PropertyValue) inventory.Properties {
	return inventory.NewProperties(map[string]inventory.PropertyValue{inventory.GrantedProperty: v})
}

// TestGrants_RoundTrip encodes a probed and an asserted grant and reads
// them back sorted by capability, every field kept.
func TestGrants_RoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	definition, build := "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("b", 64)
	grants := []inventory.Grant{
		{Capability: "acme.zeta", Source: inventory.GrantAsserted, By: "ops", Reason: "a lab box", At: at, Properties: map[string]any{}, Definition: definition},
		{Capability: "acme.alpha", Source: inventory.GrantProbed, Program: "acme", Binding: "sha256:ab", At: at, Properties: map[string]any{"model": "x9"}, Definition: definition, Build: build},
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

// TestIsBuildDigest: a build's digest is "sha256:" and 64 lower case hex
// digits, and nothing else.
func TestIsBuildDigest(t *testing.T) {
	good := "sha256:" + strings.Repeat("0a", 32)
	if !inventory.IsBuildDigest(good) {
		t.Fatalf("%s refused", good)
	}
	for _, bad := range []string{"", strings.Repeat("0a", 32), strings.ToUpper(good), good[:70], good + "0", "md5:" + strings.Repeat("0a", 32)} {
		if inventory.IsBuildDigest(bad) {
			t.Errorf("IsBuildDigest(%q) = true", bad)
		}
	}
}

// TestLapsedGrant: the reason a device records for a lapsed grant of a
// capability, and nothing for a warning, another capability, or a device
// that keeps no notes.
func TestLapsedGrant(t *testing.T) {
	device := notedStub{Stub: &inventorytest.Stub{StubName: "w1"}, notes: []inventory.GrantNote{
		{Capability: "acme.alpha", Lapsed: true, Reason: "its definition changed"},
		{Capability: "acme.zeta", Reason: "a warning"},
	}}
	if got := inventory.LapsedGrant(device, "acme.alpha"); got != "its definition changed" {
		t.Errorf("a lapse: %q", got)
	}
	for _, name := range []string{"acme.zeta", "acme.other"} {
		if got := inventory.LapsedGrant(device, capability.Name(name)); got != "" {
			t.Errorf("%s: %q, want nothing", name, got)
		}
	}
	if got := inventory.LapsedGrant(&inventorytest.Stub{StubName: "w2"}, "acme.alpha"); got != "" {
		t.Errorf("a device keeping no notes: %q", got)
	}
}

// notedStub is a device that keeps grant notes.
type notedStub struct {
	*inventorytest.Stub
	notes []inventory.GrantNote
}

// GrantNotes implements inventory.GrantNoter.
func (s notedStub) GrantNotes() []inventory.GrantNote { return s.notes }

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
