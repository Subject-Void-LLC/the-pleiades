// Tests for generic_snmp: the records it refuses, what its accessors read,
// and that a Runner rebuilds it from its dispatch properties alone.
package generic_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/snmp"
)

// TestSNMP_RefusesUnusableRecords: a host is required and must be one name,
// and every SNMP setting pkg/snmp refuses is refused at load, the cleartext
// versions without their opt-in among them.
func TestSNMP_RefusesUnusableRecords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		ok    bool
	}{
		{"v3 by default", nil, true},
		{"no host", map[string]inventory.PropertyValue{generic.HostProperty: ""}, false},
		{"a host with a space", map[string]inventory.PropertyValue{generic.HostProperty: "10.0.0.3 evil"}, false},
		{"a host with a path", map[string]inventory.PropertyValue{generic.HostProperty: "10.0.0.3/24"}, false},
		{"v2c without its opt-in", map[string]inventory.PropertyValue{snmp.VersionProperty: "2c"}, false},
		{"v2c with its opt-in", map[string]inventory.PropertyValue{snmp.VersionProperty: "2c", snmp.AllowCleartextCommunityProperty: true}, true},
		{"the opt-in with v3", map[string]inventory.PropertyValue{snmp.AllowCleartextCommunityProperty: true}, false},
		{"a port out of range", map[string]inventory.PropertyValue{snmp.PortProperty: 70000}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildWith(t, generic.TypeSNMP, tc.props, nil); (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestSNMP_AccessorsAndItsDispatchRebuildIt: the accessors read the record's
// host and settings, and the device rebuilt from record.Dispatched's
// properties alone, as a Runner rebuilds it, reads the same.
func TestSNMP_AccessorsAndItsDispatchRebuildIt(t *testing.T) {
	item, err := buildWith(t, generic.TypeSNMP, map[string]inventory.PropertyValue{
		snmp.PortProperty: 1161, snmp.VersionProperty: "1", snmp.AllowCleartextCommunityProperty: true,
		snmp.TimeoutProperty: 3, snmp.MinIntervalProperty: 50, "notes": "kept home",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	agent := item.(interface {
		capability.SNMPCapable
		snmp.Configured
		capability.NetworkAddressableCapable
	})
	s := agent.SNMPSettings()
	if agent.SNMPHost() != "10.0.0.3" || agent.SNMPPort() != 1161 || agent.IPAddress() != "10.0.0.3" || s.Version != snmp.Version1 || s.Timeout != 3*time.Second || s.MinInterval != 50*time.Millisecond {
		t.Fatalf("accessors: %s:%d %+v", agent.SNMPHost(), agent.SNMPPort(), s)
	}
	typ, props := record.Dispatched(item)
	if typ != generic.TypeSNMP || props["notes"] != nil || props[snmp.VersionProperty] != "1" {
		t.Fatalf("dispatched %s %v", typ, props)
	}
	ctor, _ := record.LookupType(typ)
	rebuilt, err := ctor(record.Record{Name: "d1", Type: typ, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.(snmp.Configured).SNMPSettings() != s || rebuilt.(capability.SNMPCapable).SNMPPort() != 1161 {
		t.Error("the device rebuilt from its dispatch reads other settings")
	}
	if item.HasCapability(capability.NameSNMP) {
		t.Error("SNMPCapable was held before any discovery")
	}
	if b := item.(inventory.DiscoveryBinder).DiscoveryBinding(); b == "" || b != generic.Binding(generic.TypeSNMP, item.Properties()) {
		t.Error("generic_snmp's discovery binding is not its bound properties' digest")
	}
	if item.(inventory.StaleDiscoverer).StaleDiscovery() != "" {
		t.Error("a device with no discovery reads as holding a stale one")
	}
}
