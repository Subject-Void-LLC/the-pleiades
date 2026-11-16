// Tests for the pin onboarding records on first use: only its own writers
// reach it, and a person's confirmation moves it into the pin property while
// keeping valid exactly the discovery and grants that held for the record as
// it was.
package record_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// firstUseType binds its discovery to its address and its TLS trust, as
// every generic TLS type does.
const firstUseType = "first_use_bound_test_type"

// firstUsePin is a certificate pin onboarding might record.
var firstUsePin = "sha256:" + strings.Repeat("ab", 32)

func init() {
	record.RegisterDiscoveryRules(firstUseType, record.DiscoveryRules{
		Grants: []capability.Name{capability.NameHTTPAPI},
		Bound:  []string{"host", "tls_fingerprint", inventory.TLSFirstUseProperty},
	})
}

// firstUseRecord is a device at 10.0.0.1 holding pin on first use, with a
// discovery and two grants: one bound to the record as it is, one to another
// address.
func firstUseRecord(t *testing.T, typ string) record.Record {
	t.Helper()
	props := map[string]inventory.PropertyValue{"host": "10.0.0.1", inventory.TLSFirstUseProperty: firstUsePin}
	binding := record.DiscoveryBinding(typ, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}, Binding: binding}.Property()
	current, stale := widgetGrant(nil), widgetGrant(nil)
	current.Binding = binding
	stale.Capability, stale.Binding = "acme.other", "sha256:"+strings.Repeat("0", 64)
	props[inventory.GrantedProperty] = inventory.GrantsProperty([]inventory.Grant{current, stale})
	return record.Record{Name: "fw1", Type: typ, Properties: props}
}

// TestFirstUsePin_OnlyItsOwnWritersReachIt: recording and removing it each
// make a revision, removing an absent one reports none, and AddInfo and
// RemoveInfo refuse it, as every write path a person reaches does.
func TestFirstUsePin_OnlyItsOwnWritersReachIt(t *testing.T) {
	b := record.NewBase(record.Record{Name: "fw1", Type: firstUseType, Properties: map[string]inventory.PropertyValue{"host": "10.0.0.1"}}, nil)
	if b.RemoveFirstUsePin() {
		t.Error("removing a first-use pin that is not there reported one")
	}
	b.RecordFirstUsePin(firstUsePin)
	if got, _ := b.Properties().String(inventory.TLSFirstUseProperty); got != firstUsePin {
		t.Fatalf("recorded %q", got)
	}
	if err := b.AddInfo(inventory.TLSFirstUseProperty, "sha256:"+strings.Repeat("cd", 32), true); err == nil {
		t.Error("AddInfo wrote the first-use pin")
	}
	if err := b.RemoveInfo(inventory.TLSFirstUseProperty); err == nil {
		t.Error("RemoveInfo removed the first-use pin")
	}
	if !b.RemoveFirstUsePin() {
		t.Error("removing the recorded pin reported none")
	}
	if _, present := b.Properties().Raw()[inventory.TLSFirstUseProperty]; present || len(b.History()) != 2 {
		t.Errorf("after removal: present %v, %d revisions (want 2)", present, len(b.History()))
	}
}

// TestConfirmFirstUsePin_MovesThePinAndKeepsWhatHeld: the confirmed pin is
// the device's tls_fingerprint and the first-use pin is gone; the discovery
// and the grant bound to the record as it was hold for it as it is, so the
// device keeps what it was granted; a grant bound to another address stays
// as stale as it was.
func TestConfirmFirstUsePin_MovesThePinAndKeepsWhatHeld(t *testing.T) {
	rec := firstUseRecord(t, firstUseType)
	b := record.NewBase(rec, nil)
	if err := b.ConfirmFirstUsePin(firstUsePin, "tls_fingerprint"); err != nil {
		t.Fatal(err)
	}
	props := b.Properties()
	if got, _ := props.String("tls_fingerprint"); got != firstUsePin {
		t.Errorf("tls_fingerprint is %q", got)
	}
	if _, present := props.Raw()[inventory.TLSFirstUseProperty]; present {
		t.Error("the first-use pin is still recorded")
	}
	after := record.DiscoveryBinding(firstUseType, props)
	d, _, err := inventory.DiscoveryFrom(props)
	if err != nil || d.Binding != after {
		t.Errorf("the discovery is bound to %q, want %q (%v)", d.Binding, after, err)
	}
	caps, stale, err := record.Declared(firstUseType, record.Record{Name: "fw1", Type: firstUseType, Properties: props.Raw()}, nil)
	if err != nil || stale != "" || len(caps) != 1 {
		t.Errorf("the confirmed device declares %v (stale %q, %v), want what onboarding proved", caps, stale, err)
	}
	grants, _, err := inventory.GrantsFrom(props)
	if err != nil || len(grants) != 2 {
		t.Fatalf("grants %v, %v", grants, err)
	}
	for _, g := range grants {
		switch g.Capability {
		case "acme.widget_api":
			if g.Binding != after {
				t.Errorf("the grant bound to the record as it was is bound to %q", g.Binding)
			}
		default:
			if g.Binding != "sha256:"+strings.Repeat("0", 64) {
				t.Errorf("the stale grant was rebound to %q", g.Binding)
			}
		}
	}
}

// TestConfirmFirstUsePin_Refusals: no first-use pin, another pin, and a
// device that already sets the pin property are each refused, changing
// nothing.
func TestConfirmFirstUsePin_Refusals(t *testing.T) {
	none := record.NewBase(record.Record{Name: "fw1", Type: firstUseType, Properties: map[string]inventory.PropertyValue{"host": "10.0.0.1"}}, nil)
	if err := none.ConfirmFirstUsePin(firstUsePin, "tls_fingerprint"); !errors.Is(err, record.ErrNoFirstUsePin) {
		t.Errorf("no first-use pin: %v", err)
	}
	other := record.NewBase(firstUseRecord(t, firstUseType), nil)
	if err := other.ConfirmFirstUsePin("sha256:"+strings.Repeat("cd", 32), "tls_fingerprint"); !errors.Is(err, record.ErrNoFirstUsePin) || !strings.Contains(err.Error(), firstUsePin) {
		t.Errorf("another pin: %v, want a refusal naming the recorded one", err)
	}
	rec := firstUseRecord(t, firstUseType)
	rec.Properties["tls_fingerprint"] = "sha256:" + strings.Repeat("ef", 32)
	set := record.NewBase(rec, nil)
	if err := set.ConfirmFirstUsePin(firstUsePin, "tls_fingerprint"); err == nil || !strings.Contains(err.Error(), "already sets tls_fingerprint") {
		t.Errorf("a pin already set: %v", err)
	}
	for _, b := range []*record.Base{none, other, set} {
		if len(b.History()) != 0 {
			t.Errorf("a refused confirmation recorded %d revisions", len(b.History()))
		}
	}
}

// TestConfirmFirstUsePin_LeavesWhatWasNotBoundAlone: on a type whose
// discovery is not bound the move is all there is, and on a bound type with
// a stale discovery, or none, and no grant bound to the record, nothing is
// rebound.
func TestConfirmFirstUsePin_LeavesWhatWasNotBoundAlone(t *testing.T) {
	unbound := record.NewBase(firstUseRecord(t, "first_use_unbound_test_type"), nil)
	if err := unbound.ConfirmFirstUsePin(firstUsePin, "tls_fingerprint"); err != nil || len(unbound.History()) != 2 {
		t.Errorf("unbound: %v, %d revisions (want the move's 2)", err, len(unbound.History()))
	}

	rec := firstUseRecord(t, firstUseType)
	rec.Properties[inventory.DiscoveredProperty] = inventory.Discovery{Protocol: "http", Binding: "sha256:" + strings.Repeat("1", 64)}.Property()
	delete(rec.Properties, inventory.GrantedProperty)
	staleDiscovery := record.NewBase(rec, nil)
	if err := staleDiscovery.ConfirmFirstUsePin(firstUsePin, "tls_fingerprint"); err != nil {
		t.Fatal(err)
	}
	d, _, _ := inventory.DiscoveryFrom(staleDiscovery.Properties())
	if d.Binding != "sha256:"+strings.Repeat("1", 64) || len(staleDiscovery.History()) != 2 {
		t.Errorf("a stale discovery was rebound to %q (%d revisions)", d.Binding, len(staleDiscovery.History()))
	}

	bare := firstUseRecord(t, firstUseType)
	delete(bare.Properties, inventory.DiscoveredProperty)
	only := widgetGrant(nil)
	only.Binding = "sha256:" + strings.Repeat("2", 64)
	bare.Properties[inventory.GrantedProperty] = inventory.GrantsProperty([]inventory.Grant{only})
	noneBound := record.NewBase(bare, nil)
	if err := noneBound.ConfirmFirstUsePin(firstUsePin, "tls_fingerprint"); err != nil || len(noneBound.History()) != 2 {
		t.Errorf("nothing bound to the record: %v, %d revisions (want the move's 2)", err, len(noneBound.History()))
	}
}
