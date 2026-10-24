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

// TestTransport_ConsoleNeedsItsServerAndLine: a router or switch whose
// transport is console is reached through the console server it names, at
// the line it names, and claims neither SSH, Telnet nor an address of its
// own; one that names no server, a server that is not a name, or a line
// that is not a console UUID is refused when the record is built.
func TestTransport_ConsoleNeedsItsServerAndLine(t *testing.T) {
	const line = "0ba0f180-c5b2-4dcf-af64-bf9bb645a566"
	for name, build := range map[string]func(record.Record) (inventory.InventoryItem, error){"router": cisco.NewRouter, "switch": cisco.NewSwitch} {
		props := map[string]inventory.PropertyValue{"transport": "console", cisco.ConsoleServerProperty: "cml", cisco.ConsoleLineProperty: line}
		item, err := build(record.Record{ID: "p1", Name: "P1", Properties: props})
		if err != nil {
			t.Fatalf("%s over console: %v", name, err)
		}
		if !item.HasCapability(capability.NameConsoleServed) || !item.HasCapability(capability.NameCiscoIOS) ||
			item.HasCapability(capability.NameSSHTransport) || item.HasCapability(capability.NameTelnet) || item.HasCapability(capability.NameNetworkAddressable) {
			t.Errorf("%s over console declares %v", name, item.Capabilities())
		}
		served := item.(capability.ConsoleServedCapable)
		if served.ConsoleServer() != "cml" || served.ConsoleLine() != line {
			t.Errorf("%s's console = %s %s", name, served.ConsoleServer(), served.ConsoleLine())
		}
		for why, bad := range map[string]map[string]inventory.PropertyValue{
			"no server":             {"transport": "console", cisco.ConsoleLineProperty: line},
			"a server with a space": {"transport": "console", cisco.ConsoleServerProperty: "my cml", cisco.ConsoleLineProperty: line},
			"no line":               {"transport": "console", cisco.ConsoleServerProperty: "cml"},
			"a path for a line":     {"transport": "console", cisco.ConsoleServerProperty: "cml", cisco.ConsoleLineProperty: "/lab/P1/0"},
			"a line with a command": {"transport": "console", cisco.ConsoleServerProperty: "cml", cisco.ConsoleLineProperty: line + "\rexit"},
		} {
			if _, err := build(record.Record{ID: "p1", Name: "P1", Properties: bad}); err == nil {
				t.Errorf("%s accepted %s", name, why)
			}
		}
	}
}
