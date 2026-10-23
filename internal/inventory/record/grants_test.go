// Tests for the data capabilities a record is granted.
package record_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grantTypeName is a device type no rule mentions, so Declared reads only
// the grants.
const grantTypeName = "grants_test_type"

// widgetGrant is an asserted grant of acme.widget_api.
func widgetGrant(props map[string]any) inventory.Grant {
	return inventory.Grant{Capability: "acme.widget_api", Source: inventory.GrantAsserted, By: "ops", Reason: "lab", Properties: props, At: time.Unix(0, 0)}
}

// recordWith is a record whose granted property holds grants.
func recordWith(grants ...inventory.Grant) record.Record {
	return record.Record{Name: "w1", Type: grantTypeName, Properties: map[string]inventory.PropertyValue{
		"host":                    "10.0.0.1",
		inventory.GrantedProperty: inventory.GrantsProperty(grants),
	}}
}

// capsOf is what a device built on rec with baseline declares, as every
// device type builds its capabilities (record.NewBase).
func capsOf(rec record.Record, baseline []capability.Name) []capability.Name {
	return record.NewBase(rec, baseline).Capabilities()
}

// TestNewBase_AddsWhatARecordIsGranted: a grant of a capability this
// process does not define is kept by name (a Controller loads no program);
// one it defines counts only with properties that fit and the compiled
// capabilities it requires.
func TestNewBase_AddsWhatARecordIsGranted(t *testing.T) {
	baseline := []capability.Name{capability.NameSSHTransport}
	ok := map[string]any{"endpoint": "https://widgets.example"}

	if caps := capsOf(recordWith(widgetGrant(ok)), baseline); !slices.Contains(caps, "acme.widget_api") {
		t.Fatalf("undefined: caps = %v; want the grant kept by name", caps)
	}

	t.Cleanup(capability.SnapshotForTest())
	if err := capability.RegisterData(capability.DataDefinition{
		Name:       "acme.widget_api",
		Requires:   []capability.Name{capability.NameSSHTransport},
		Properties: []capability.DataProperty{{Name: "endpoint", Type: "string", Required: true}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		props    map[string]any
		baseline []capability.Name
		want     bool
	}{
		{"properties that fit", ok, baseline, true},
		{"a missing required property", map[string]any{}, baseline, false},
		{"an undeclared property", map[string]any{"endpoint": "x", "zone": "y"}, baseline, false},
		{"no compiled capability it requires", ok, nil, false},
	} {
		caps := capsOf(recordWith(widgetGrant(tc.props)), tc.baseline)
		if got := slices.Contains(caps, "acme.widget_api"); got != tc.want {
			t.Errorf("%s: granted %v, want %v (caps %v)", tc.name, got, tc.want, caps)
		}
	}
}

// TestNewBase_AMalformedGrantGrantsNothing: a grant of a name that is not
// a data capability's, or with an unknown source, was not written by
// RecordGrant, so the record grants nothing at all: failing closed.
func TestNewBase_AMalformedGrantGrantsNothing(t *testing.T) {
	for name, granted := range map[string]map[string]any{
		"a compiled name":   {string(capability.NameSSHTransport): map[string]any{"source": "assertion"}},
		"an unknown source": {"acme.widget_api": map[string]any{"source": "rumour"}},
	} {
		rec := record.Record{Name: "w1", Type: grantTypeName, Properties: map[string]inventory.PropertyValue{inventory.GrantedProperty: granted}}
		if caps := capsOf(rec, nil); len(caps) != 0 {
			t.Errorf("%s: a malformed grant declared %v", name, caps)
		}
	}
}

// TestBase_GrantsAreWrittenOnlyThroughTheirOwnMethods: AddInfo refuses the
// property, RecordGrant replaces a grant of the same capability, and
// RevokeGrant removes it, each with a revision.
func TestBase_GrantsAreWrittenOnlyThroughTheirOwnMethods(t *testing.T) {
	b := record.NewBase(record.Record{Name: "w1", Properties: map[string]inventory.PropertyValue{}}, nil)
	if err := b.AddInfo(inventory.GrantedProperty, map[string]any{}, true); err == nil || !strings.Contains(err.Error(), "grant-capability") {
		t.Errorf("AddInfo(granted) = %v, want refused naming grant-capability", err)
	}
	if err := b.RecordGrant(widgetGrant(map[string]any{"endpoint": "a"})); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordGrant(widgetGrant(map[string]any{"endpoint": "b"})); err != nil {
		t.Fatal(err)
	}
	grants, _, err := inventory.GrantsFrom(b.Properties())
	if err != nil || len(grants) != 1 || grants[0].Properties["endpoint"] != "b" || grants[0].By != "ops" {
		t.Fatalf("grants = %+v, %v; want the one grant, replaced", grants, err)
	}
	if removed, err := b.RevokeGrant("acme.widget_api"); err != nil || !removed {
		t.Fatalf("RevokeGrant = %v, %v", removed, err)
	}
	if _, present := b.Properties().Raw()[inventory.GrantedProperty]; present {
		t.Error("the last grant revoked left the property behind")
	}
	if removed, _ := b.RevokeGrant("acme.widget_api"); removed {
		t.Error("revoking a grant that is not there reported one")
	}
	if len(b.History()) != 3 {
		t.Errorf("history has %d revisions, want 3 (two grants and a revoke)", len(b.History()))
	}
}

// boundGrantType is a device type whose host its grants are bound to.
const boundGrantType = "grants_bound_test_type"

func init() {
	record.RegisterDiscoveryRules(boundGrantType, record.DiscoveryRules{Bound: []string{"host"}})
}

// TestRecordGrant_AnAssertionFollowsNoRepointedDevice: an assertion is
// bound to the properties its type binds, as a probe's grant is, so the
// device repointed holds nothing until someone vouches again.
func TestRecordGrant_AnAssertionFollowsNoRepointedDevice(t *testing.T) {
	rec := record.Record{Name: "w1", Type: boundGrantType, Properties: map[string]inventory.PropertyValue{"host": "10.0.0.1"}}
	base := record.NewBase(rec, nil)
	if err := base.RecordGrant(widgetGrant(map[string]any{"endpoint": "https://widgets.example"})); err != nil {
		t.Fatal(err)
	}
	props := base.Properties().Raw()
	if caps := capsOf(record.Record{Name: "w1", Type: boundGrantType, Properties: props}, nil); !slices.Contains(caps, "acme.widget_api") {
		t.Fatalf("the device as vouched for: caps = %v", caps)
	}
	moved := map[string]inventory.PropertyValue{}
	for k, v := range props {
		moved[k] = v
	}
	moved["host"] = "10.0.0.2"
	if caps := capsOf(record.Record{Name: "w1", Type: boundGrantType, Properties: moved}, nil); slices.Contains(caps, "acme.widget_api") {
		t.Errorf("the repointed device still holds the assertion: %v", caps)
	}
}

// TestBase_AGrantLeavesTheOthersAndAMalformedOneIsRefused: granting or
// revoking one capability keeps every other grant, and a granted property
// RecordGrant did not write is neither added to nor revoked from.
func TestBase_AGrantLeavesTheOthersAndAMalformedOneIsRefused(t *testing.T) {
	other := widgetGrant(nil)
	other.Capability = "acme.other"
	b := record.NewBase(recordWith(other), nil)
	if err := b.RecordGrant(widgetGrant(map[string]any{"endpoint": "a"})); err != nil {
		t.Fatal(err)
	}
	if removed, err := b.RevokeGrant("acme.widget_api"); err != nil || !removed {
		t.Fatalf("RevokeGrant = %v, %v", removed, err)
	}
	grants, _, err := inventory.GrantsFrom(b.Properties())
	if err != nil || len(grants) != 1 || grants[0].Capability != "acme.other" {
		t.Fatalf("grants = %+v, %v; want acme.other kept through a grant and a revoke", grants, err)
	}

	bad := record.NewBase(record.Record{Name: "w1", Type: grantTypeName, Properties: map[string]inventory.PropertyValue{inventory.GrantedProperty: "acme.widget_api"}}, nil)
	if err := bad.RecordGrant(widgetGrant(nil)); err == nil {
		t.Error("RecordGrant over a malformed granted property succeeded")
	}
	if _, err := bad.RevokeGrant("acme.widget_api"); err == nil {
		t.Error("RevokeGrant over a malformed granted property succeeded")
	}
}
