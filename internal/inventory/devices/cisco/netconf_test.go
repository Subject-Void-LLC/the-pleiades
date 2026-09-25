package cisco_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// netconfConstructors runs a case against both Cisco device types.
// NetconfCapable's two halves were added to Router and Switch together,
// and a test covering only one would let the other silently diverge.
var netconfConstructors = map[string]func(record.Record) (inventory.InventoryItem, error){
	"cisco_router": cisco.NewRouter,
	"cisco_switch": cisco.NewSwitch,
}

// TestNetconfCapable_NeedsBothHalves is the regression proof for the
// gap this phase found: NetconfCapable is not an ancestor of
// CiscoIOSCapable (it has no parent at all), so declaring CiscoIOSCapable
// does not resolve to it. Before this, every Cisco device implemented
// SupportsNETCONF and none of them could satisfy NetconfCapable.
//
// It asserts BOTH directions on purpose. A device with NETCONF disabled
// must NOT claim the capability, or the property stops meaning anything
// and every Cisco router in an inventory becomes a NETCONF target
// whether or not it has the feature configured.
func TestNetconfCapable_NeedsBothHalves(t *testing.T) {
	for typeName, construct := range netconfConstructors {
		t.Run(typeName, func(t *testing.T) {
			t.Run("enabled", func(t *testing.T) {
				item, err := construct(record.Record{
					ID: "d1", Name: "d1", Type: typeName,
					Properties: map[string]inventory.PropertyValue{"netconf_enabled": true},
				})
				if err != nil {
					t.Fatalf("constructing %s: %v", typeName, err)
				}
				if !item.HasCapability(capability.NameNetconf) {
					t.Fatal("HasCapability(NetconfCapable) = false with netconf_enabled set, so net.netconf.config could never dispatch against a real device")
				}
			})

			t.Run("not enabled", func(t *testing.T) {
				item, err := construct(record.Record{ID: "d2", Name: "d2", Type: typeName})
				if err != nil {
					t.Fatalf("constructing %s: %v", typeName, err)
				}
				if item.HasCapability(capability.NameNetconf) {
					t.Fatal("HasCapability(NetconfCapable) = true with no netconf_enabled property: NETCONF is configuration on a Cisco device, not a property of the model")
				}
				// The structural half is still present. That is the
				// point of the pairing: the device could speak NETCONF,
				// and the data says nobody turned it on.
				if _, ok := item.(capability.NetconfCapable); !ok {
					t.Fatal("the type does not structurally implement capability.NetconfCapable, so enabling the property could never make it satisfiable either")
				}
			})
		})
	}
}

// TestNetconfPort_DefaultsTo830 pins a default that was measured rather
// than assumed. A real Cisco IOS XE 17.12 device accepts the "netconf"
// subsystem request on port 22 and then immediately ends the channel,
// serving NETCONF only on 830, so defaulting to the SSH port would
// produce a client that reports that device as working.
func TestNetconfPort_DefaultsTo830(t *testing.T) {
	for typeName, construct := range netconfConstructors {
		t.Run(typeName, func(t *testing.T) {
			item, err := construct(record.Record{
				ID: "d1", Name: "d1", Type: typeName,
				Properties: map[string]inventory.PropertyValue{"netconf_enabled": true},
			})
			if err != nil {
				t.Fatalf("constructing %s: %v", typeName, err)
			}
			dev, ok := item.(capability.NetconfCapable)
			if !ok {
				t.Fatal("the type does not implement capability.NetconfCapable")
			}
			if got := dev.NetconfPort(); got != 830 {
				t.Errorf("NetconfPort() = %d, want 830", got)
			}
		})
	}
}

func TestNetconfPort_HonorsTheProperty(t *testing.T) {
	for typeName, construct := range netconfConstructors {
		t.Run(typeName, func(t *testing.T) {
			item, err := construct(record.Record{
				ID: "d1", Name: "d1", Type: typeName,
				Properties: map[string]inventory.PropertyValue{
					"netconf_enabled": true,
					"netconf_port":    8300,
				},
			})
			if err != nil {
				t.Fatalf("constructing %s: %v", typeName, err)
			}
			if got := item.(capability.NetconfCapable).NetconfPort(); got != 8300 {
				t.Errorf("NetconfPort() = %d, want 8300", got)
			}
		})
	}
}

// TestNetconfBaseline_LeavesTheRestOfTheBaselineIntact guards against
// the shared helper accidentally replacing the vendor baseline rather
// than adding to it, which would silently strip SSH reachability from
// every NETCONF-enabled device.
func TestNetconfBaseline_LeavesTheRestOfTheBaselineIntact(t *testing.T) {
	for typeName, construct := range netconfConstructors {
		t.Run(typeName, func(t *testing.T) {
			item, err := construct(record.Record{
				ID: "d1", Name: "d1", Type: typeName,
				Properties: map[string]inventory.PropertyValue{"netconf_enabled": true},
			})
			if err != nil {
				t.Fatalf("constructing %s: %v", typeName, err)
			}
			for _, name := range []capability.Name{
				capability.NameSSHTransport,
				capability.NameCiscoIOS,
				capability.NameNetworkCLI,
			} {
				if !item.HasCapability(name) {
					t.Errorf("HasCapability(%s) = false on a NETCONF-enabled device: the baseline was replaced rather than extended", name)
				}
			}
		})
	}
}
