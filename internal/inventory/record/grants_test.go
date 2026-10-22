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
	return record.Record{Name: "w1", Properties: map[string]inventory.PropertyValue{
		"host":                    "10.0.0.1",
		inventory.GrantedProperty: inventory.GrantsProperty(grants),
	}}
}

// TestDeclared_AddsWhatARecordIsGranted: a grant of a capability this
// process does not define is kept by name (a Controller loads no program);
// one it defines counts only with properties that fit and the compiled
// capabilities it requires.
func TestDeclared_AddsWhatARecordIsGranted(t *testing.T) {
	baseline := []capability.Name{capability.NameSSHTransport}
	ok := map[string]any{"endpoint": "https://widgets.example"}

	caps, _, err := record.Declared(grantTypeName, recordWith(widgetGrant(ok)), baseline)
	if err != nil || !slices.Contains(caps, "acme.widget_api") {
		t.Fatalf("undefined: Declared = %v, %v; want the grant kept by name", caps, err)
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
		caps, _, err := record.Declared(grantTypeName, recordWith(widgetGrant(tc.props)), tc.baseline)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := slices.Contains(caps, "acme.widget_api"); got != tc.want {
			t.Errorf("%s: granted %v, want %v (caps %v)", tc.name, got, tc.want, caps)
		}
	}
}

// TestDeclared_RefusesAMalformedGrant: a grant of a name that is not a data
// capability's, or with an unknown source, is a corrupted record and is
// refused rather than read as granting nothing.
func TestDeclared_RefusesAMalformedGrant(t *testing.T) {
	for name, granted := range map[string]map[string]any{
		"a compiled name":   {string(capability.NameSSHTransport): map[string]any{"source": "assertion"}},
		"an unknown source": {"acme.widget_api": map[string]any{"source": "rumour"}},
	} {
		rec := record.Record{Name: "w1", Properties: map[string]inventory.PropertyValue{inventory.GrantedProperty: granted}}
		if _, _, err := record.Declared(grantTypeName, rec, nil); err == nil {
			t.Errorf("%s: Declared accepted a malformed grant", name)
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
