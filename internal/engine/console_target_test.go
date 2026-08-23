// This file is the RULE 0 companion to action_ssh_test.go's Target
// tests. Those build their device as a stub wrapping
// inventorytest.Stub, which deliberately matches a capability by name
// and skips the structural assertion a real device type performs. A stub
// therefore proves SerialTarget reads the accessors it is handed; it
// cannot prove any device the platform can actually build has them.
//
// That distinction is not academic. Phase 73 shipped SerialTarget,
// RawPassthroughTarget and TelnetTarget with a full stub-backed suite,
// all passing, while zero real device types implemented any of the three
// capabilities -- so every one of those functions returned false for
// every device a user could hydrate, and serial_exec, serialtcp_exec and
// telnet_exec were refused unconditionally. See FAILURE_PATTERNS.md.
//
// The tests below run the same functions against a real console_device
// hydrated through the real internal/inventory ItemFactory, which is the
// path cmd/pleiades run takes.
package engine_test

import (
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// hydrateConsole builds one console_device through the real
// ItemFactory, the same object cmd/pleiades hands its file-backed
// inventory repository.
func hydrateConsole(t *testing.T, props map[string]inventory.PropertyValue) inventory.InventoryItem {
	t.Helper()
	item, err := inv.NewItemFactory().Build(record.Record{
		ID:         "console-1",
		Name:       "console-1",
		Type:       "console_device",
		Properties: props,
	})
	if err != nil {
		t.Fatalf("ItemFactory.Build(console_device): %v", err)
	}
	return item
}

// TestSerialTarget_RealDevice proves SerialTarget resolves a real,
// factory-built device rather than only a stub, and that the capability
// engine.ActionCapability requires for serial_exec is one that device
// really has.
func TestSerialTarget_RealDevice(t *testing.T) {
	dev := hydrateConsole(t, map[string]inventory.PropertyValue{
		"serial_device":    "/dev/ttyUSB0",
		"serial_baud":      115200,
		"serial_parity":    "even",
		"serial_stop_bits": "2",
	})

	if want := engine.ActionCapability["serial_exec"]; !dev.HasCapability(want) {
		t.Fatalf("a real console_device does not have %s, so serial_exec is refused before any Target runs", want)
	}

	target, ok := engine.SerialTarget(dev)
	if !ok {
		t.Fatal("SerialTarget refused a real console_device")
	}
	endpoint, ok := target.Endpoint.(transport.SerialEndpoint)
	if !ok {
		t.Fatalf("SerialTarget produced a %T endpoint, want transport.SerialEndpoint", target.Endpoint)
	}
	if endpoint.Device != serialline.Device("/dev/ttyUSB0") {
		t.Errorf("endpoint.Device = %q, want /dev/ttyUSB0", endpoint.Device)
	}
	want := serialline.Config{BaudRate: 115200, DataBits: 8, Parity: serialline.ParityEven, StopBits: serialline.StopBitsTwo}
	if endpoint.Line != want {
		t.Errorf("endpoint.Line = %+v, want %+v", endpoint.Line, want)
	}
}

// TestRawPassthroughTarget_RealDevice is SerialTarget_RealDevice's
// counterpart for serialtcp_exec.
func TestRawPassthroughTarget_RealDevice(t *testing.T) {
	dev := hydrateConsole(t, map[string]inventory.PropertyValue{
		"raw_passthrough_host": "ts1.example.net",
		"raw_passthrough_port": 2003,
	})

	if want := engine.ActionCapability["serialtcp_exec"]; !dev.HasCapability(want) {
		t.Fatalf("a real console_device does not have %s, so serialtcp_exec is refused before any Target runs", want)
	}

	target, ok := engine.RawPassthroughTarget(dev)
	if !ok {
		t.Fatal("RawPassthroughTarget refused a real console_device")
	}
	endpoint, ok := target.Endpoint.(transport.NetworkEndpoint)
	if !ok {
		t.Fatalf("RawPassthroughTarget produced a %T endpoint, want transport.NetworkEndpoint", target.Endpoint)
	}
	if endpoint.Host != "ts1.example.net" || endpoint.Port != 2003 {
		t.Errorf("endpoint = %+v, want ts1.example.net:2003", endpoint)
	}
}

// TestTelnetTarget_RealDevice is SerialTarget_RealDevice's counterpart
// for telnet_exec, including the well-known port 23 default the device
// type supplies when the record names only a host.
func TestTelnetTarget_RealDevice(t *testing.T) {
	dev := hydrateConsole(t, map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40"})

	if want := engine.ActionCapability["telnet_exec"]; !dev.HasCapability(want) {
		t.Fatalf("a real console_device does not have %s, so telnet_exec is refused before any Target runs", want)
	}

	target, ok := engine.TelnetTarget(dev)
	if !ok {
		t.Fatal("TelnetTarget refused a real console_device")
	}
	endpoint, ok := target.Endpoint.(transport.NetworkEndpoint)
	if !ok {
		t.Fatalf("TelnetTarget produced a %T endpoint, want transport.NetworkEndpoint", target.Endpoint)
	}
	if endpoint.Host != "10.20.30.40" || endpoint.Port != 23 {
		t.Errorf("endpoint = %+v, want 10.20.30.40:23", endpoint)
	}
}

// TestConsoleTargets_RefuseAPathTheDeviceDoesNotHave is the other half
// of the proof, and the reason console_device declares its capabilities
// per record rather than all four at once: a device cabled only to
// Telnet must be refused for serial_exec at plan time, by the same
// HasCapability call validate.CapabilityRule makes, instead of reaching
// a transport that would dial an empty device name.
func TestConsoleTargets_RefuseAPathTheDeviceDoesNotHave(t *testing.T) {
	telnetOnly := hydrateConsole(t, map[string]inventory.PropertyValue{"telnet_host": "10.20.30.40"})

	for _, name := range []capability.Name{capability.NameSerial, capability.NameRawPassthrough, capability.NameRFC2217} {
		if telnetOnly.HasCapability(name) {
			t.Errorf("a Telnet-only console_device claims %s, so a runbook aimed at the wrong transport would validate", name)
		}
	}
}
