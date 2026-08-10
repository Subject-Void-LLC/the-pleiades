package native

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestWireDevice_IDAndName(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{DeviceID: "dev-1", DeviceName: "core-switch-1"})
	if got := string(d.ID()); got != "dev-1" {
		t.Errorf("ID() = %q, want %q", got, "dev-1")
	}
	if got := d.Name(); got != "core-switch-1" {
		t.Errorf("Name() = %q, want %q", got, "core-switch-1")
	}
}

func TestWireDevice_SSHTransportCapable(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{DeviceHost: "10.0.0.1", SSHPort: 2222})
	if got := d.SSHHost(); got != "10.0.0.1" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.1")
	}
	if got := d.SSHPort(); got != 2222 {
		t.Errorf("SSHPort() = %d, want %d", got, 2222)
	}

	// Confirm the interface-to-interface type assertion internal/engine's
	// SSHTarget performs actually succeeds against the concrete
	// *wireDevice, not just that the two accessor methods exist.
	if _, ok := interface{}(d).(capability.SSHTransportCapable); !ok {
		t.Error("*wireDevice does not satisfy capability.SSHTransportCapable")
	}
}

func TestWireDevice_HasCapability_TrustedRelay(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{Capabilities: []capability.Name{capability.NameSSHTransport}})

	if !d.HasCapability(capability.NameSSHTransport) {
		t.Error("HasCapability(NameSSHTransport) = false, want true")
	}
	if d.HasCapability(capability.NameCiscoIOS) {
		t.Error("HasCapability(NameCiscoIOS) = true, want false (not in payload.Capabilities)")
	}

	gotCaps := d.Capabilities()
	if len(gotCaps) != 1 || gotCaps[0] != capability.NameSSHTransport {
		t.Errorf("Capabilities() = %v, want [%v]", gotCaps, capability.NameSSHTransport)
	}
}

func TestWireDevice_PropertiesCarriesHostAndPort(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{DeviceHost: "10.0.0.9", SSHPort: 22})
	props := d.Properties()

	host, ok := props.String("host")
	if !ok || host != "10.0.0.9" {
		t.Errorf("Properties()[\"host\"] = %q, %v, want %q, true", host, ok, "10.0.0.9")
	}
}

func TestWireDevice_StateAlwaysActive(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{})
	if d.State() != inventory.StateActive {
		t.Errorf("State() = %v, want StateActive", d.State())
	}
}

func TestWireDevice_AddInfoAndRemoveInfoReturnErrors(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{})
	if err := d.AddInfo("key", "value", true); err == nil {
		t.Error("AddInfo() = nil error, want a non-nil \"not supported\" error")
	}
	if err := d.RemoveInfo("key"); err == nil {
		t.Error("RemoveInfo() = nil error, want a non-nil \"not supported\" error")
	}
}

func TestWireDevice_VersionAndHistoryAreEmpty(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{})
	if d.Version() != 0 {
		t.Errorf("Version() = %d, want 0", d.Version())
	}
	if len(d.History()) != 0 {
		t.Errorf("History() = %v, want empty", d.History())
	}
	if len(d.Tags()) != 0 {
		t.Errorf("Tags() = %v, want empty", d.Tags())
	}
}
