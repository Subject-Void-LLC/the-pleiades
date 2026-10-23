// Tests of how a Cisco device's CLI is reached: SSH by default, Telnet when
// its transport says so, never both, and nothing else.
package cisco_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestTransport_TelnetReplacesSSH: a router or switch whose transport is
// telnet is reached over Telnet at its host and telnet_port (23 unless
// set), and is not claimed reachable over SSH; an unknown transport is
// refused when the record is built.
func TestTransport_TelnetReplacesSSH(t *testing.T) {
	type telnetDevice interface {
		inventory.InventoryItem
		capability.TelnetCapable
	}
	for name, build := range map[string]func(record.Record) (inventory.InventoryItem, error){"router": cisco.NewRouter, "switch": cisco.NewSwitch} {
		props := map[string]inventory.PropertyValue{"host": "10.10.20.171", "transport": "telnet"}
		item, err := build(record.Record{ID: "r1", Name: "r1", Properties: props})
		if err != nil {
			t.Fatalf("%s over telnet: %v", name, err)
		}
		if !item.HasCapability(capability.NameTelnet) || item.HasCapability(capability.NameSSHTransport) || !item.HasCapability(capability.NameCiscoIOS) {
			t.Errorf("%s over telnet declares %v, want Telnet and IOS and not SSH", name, item.Capabilities())
		}
		tel := item.(telnetDevice)
		if tel.TelnetHost() != "10.10.20.171" || tel.TelnetPort() != 23 {
			t.Errorf("%s's telnet address = %s:%d", name, tel.TelnetHost(), tel.TelnetPort())
		}
		props["telnet_port"] = 2323
		item, _ = build(record.Record{ID: "r1", Name: "r1", Properties: props})
		if p := item.(telnetDevice).TelnetPort(); p != 2323 {
			t.Errorf("%s's telnet_port = %d, want 2323", name, p)
		}

		item, err = build(record.Record{ID: "r1", Name: "r1", Properties: map[string]inventory.PropertyValue{"host": "10.10.20.171", "transport": "ssh"}})
		if err != nil || !item.HasCapability(capability.NameSSHTransport) || item.HasCapability(capability.NameTelnet) {
			t.Errorf("%s over ssh = %v, %v; want SSH and not Telnet", name, item, err)
		}
		if _, err := build(record.Record{ID: "r1", Name: "r1", Properties: map[string]inventory.PropertyValue{"transport": "serial"}}); err == nil {
			t.Errorf("%s accepted transport serial", name)
		}
	}
}
