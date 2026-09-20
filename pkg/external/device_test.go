// Tests for Device, the InventoryItem a child process rebuilds from the
// wire payload.
//
// These moved here with the type itself, from internal/adapters/native
// (where it was wireDevice). The Runner's own per-task child and every
// external Collection now hand a method this one adapter, so its behavior
// is pinned once, here, for both.
package external_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestDevice_IDAndName(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{DeviceID: "dev-1", DeviceName: "core-switch-1"})
	if got := string(d.ID()); got != "dev-1" {
		t.Errorf("ID() = %q, want %q", got, "dev-1")
	}
	if got := d.Name(); got != "core-switch-1" {
		t.Errorf("Name() = %q, want %q", got, "core-switch-1")
	}
}

func TestDevice_SSHTransportCapable(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{DeviceHost: "10.0.0.1", SSHPort: 2222})
	if got := d.SSHHost(); got != "10.0.0.1" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.1")
	}
	if got := d.SSHPort(); got != 2222 {
		t.Errorf("SSHPort() = %d, want %d", got, 2222)
	}

	// Confirm the interface-to-interface type assertion internal/engine's
	// SSHTarget performs actually succeeds against the concrete *Device
	// once it is held as an InventoryItem (which is how a method receives
	// it), not just that the two accessor methods exist.
	var item inventory.InventoryItem = d
	if _, ok := item.(capability.SSHTransportCapable); !ok {
		t.Error("*external.Device held as an InventoryItem does not satisfy capability.SSHTransportCapable")
	}
}

func TestDevice_HasCapability_TrustedRelay(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{Capabilities: []capability.Name{capability.NameSSHTransport}})

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

// TestDevice_PropertiesCarriesHostAndPort pins the two property keys the
// adapter promises: "host", every real device type's own key for its
// management address, and "port", for a caller that reads properties
// directly rather than through SSHTransportCapable.
func TestDevice_PropertiesCarriesHostAndPort(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{DeviceHost: "10.0.0.9", SSHPort: 22})
	props := d.Properties()

	host, ok := props.String("host")
	if !ok || host != "10.0.0.9" {
		t.Errorf("Properties()[\"host\"] = %q, %v, want %q, true", host, ok, "10.0.0.9")
	}
	port, ok := props.Int("port")
	if !ok || port != 22 {
		t.Errorf("Properties()[\"port\"] = %d, %v, want %d, true", port, ok, 22)
	}
}

func TestDevice_StateAlwaysActive(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{})
	if d.State() != inventory.StateActive {
		t.Errorf("State() = %v, want StateActive", d.State())
	}
}

// TestDevice_MutatorsRefuseRatherThanSilentlyDrop proves AddInfo and
// RemoveInfo report a real error. A child holds no inventory backend, so
// a silent no-op here would let a Collection method believe it had
// persisted a property change that nothing anywhere recorded.
func TestDevice_MutatorsRefuseRatherThanSilentlyDrop(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{DeviceName: "core-1"})

	cases := []struct {
		name string
		call func() error
		why  string
	}{
		{"AddInfo", func() error { return d.AddInfo("k", "v", true) }, "a silent no-op would look like a successful write"},
		{"RemoveInfo", func() error { return d.RemoveInfo("k") }, "a silent no-op would look like a successful delete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s() = nil, want an error: %s", tc.name, tc.why)
			}
			if !strings.Contains(err.Error(), "not supported") {
				t.Errorf("%s() error = %q, want it to say the operation is not supported here", tc.name, err)
			}
		})
	}
}

func TestDevice_VersionAndHistoryAreEmpty(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{})
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

// TestDevice_HasCapability_ResolvesHierarchy pins the property that makes
// the Walk tier agree with the Crawl tier.
//
// A real device type answers HasCapability through record.Base.Declares,
// which runs capability.Resolves, so a device declaring the concrete
// SystemdCapable satisfies a method requiring the broad
// ServiceManagerCapable. This adapter answered by exact set membership
// until engine.checkMethodCapabilities started enforcing manifests, at
// which point that difference stopped being academic: the same runbook,
// the same method and the same device succeeded through the CLI and were
// refused through the runner. A capability check that depends on which
// binary is running is worse than either answer on its own. An external
// Collection now receives this same adapter, so it inherits the same
// answer.
func TestDevice_HasCapability_ResolvesHierarchy(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{
		Capabilities: []capability.Name{capability.NameSystemd},
	})

	if !d.HasCapability(capability.NameSystemd) {
		t.Error("HasCapability(SystemdCapable) = false for a device that declares it")
	}
	if !d.HasCapability(capability.NameServiceManager) {
		t.Error("HasCapability(ServiceManagerCapable) = false: declaring the child must satisfy the parent")
	}
	// Resolution walks upward only. A sibling under the same parent, and
	// a child of the declared capability, are both still refusals: a
	// systemd host is not a Windows one, and it has not claimed firewalld.
	if d.HasCapability(capability.NameWindowsService) {
		t.Error("HasCapability(WindowsServiceCapable) = true for a systemd device")
	}
	if d.HasCapability(capability.NameFirewalld) {
		t.Error("HasCapability(FirewalldCapable) = true: resolution must not walk downward into a narrower claim")
	}
}

// TestDevice_ShowInfoMatchesProperties pins ShowInfo to Properties. The
// two are separate methods on inventory.InventoryItem and a future edit
// could easily make them disagree, which would show up as a device
// reporting different metadata depending on which accessor a caller
// happened to reach for.
func TestDevice_ShowInfoMatchesProperties(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{DeviceHost: "10.0.0.4", SSHPort: 2022})

	shown, ok := d.ShowInfo().String("host")
	if !ok || shown != "10.0.0.4" {
		t.Errorf("ShowInfo()[\"host\"] = %q, %v, want %q, true", shown, ok, "10.0.0.4")
	}
	props, _ := d.Properties().String("host")
	if shown != props {
		t.Errorf("ShowInfo() and Properties() disagree on host: %q vs %q", shown, props)
	}
}

// TestDevice_SourceIsZero documents that a child-side device carries no
// sync-plugin provenance: the wire payload has no field for it, and
// inventing one here would fabricate an authority that never synced this
// device.
func TestDevice_SourceIsZero(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{DeviceName: "core-1"})
	if src := d.Source(); src.Plugin != "" || !src.SyncedAt.IsZero() {
		t.Errorf("Source() = %+v, want the zero SourceAuthority", src)
	}
}

// TestDevice_CapabilitiesCopyIsDefensive proves a caller cannot reach
// back through the returned slice and change what the device reports it
// can do, which would let one task's mutation silently re-gate a later
// task's transport selection.
func TestDevice_CapabilitiesCopyIsDefensive(t *testing.T) {
	d := external.NewDevice(wire.DispatchPayload{Capabilities: []capability.Name{capability.NameSSHTransport}})

	caps := d.Capabilities()
	if len(caps) != 1 {
		t.Fatalf("Capabilities() returned %d entries, want 1", len(caps))
	}
	caps[0] = capability.NameCiscoIOS

	if !d.HasCapability(capability.NameSSHTransport) {
		t.Error("mutating the slice returned by Capabilities() changed the device's own capabilities")
	}
	if d.HasCapability(capability.NameCiscoIOS) {
		t.Error("mutating the slice returned by Capabilities() granted the device a capability it never declared")
	}
}

// TestDevice_PayloadReturnsWhatItWasBuiltFrom pins Payload, which the
// Runner uses to recover the dispatch (and its secrets) from the device a
// resolver handed the engine. It also pins the copy its doc comment
// promises: editing a scalar field of the returned struct cannot change
// what the device reports.
func TestDevice_PayloadReturnsWhatItWasBuiltFrom(t *testing.T) {
	want := wire.DispatchPayload{
		JobID:        "job-1",
		DeviceID:     "dev-1",
		DeviceName:   "core-1",
		DeviceHost:   "10.0.0.1",
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      map[string]string{"username": "admin"},
	}
	d := external.NewDevice(want)

	got := d.Payload()
	if got.JobID != want.JobID || got.DeviceID != want.DeviceID || got.DeviceName != want.DeviceName ||
		got.DeviceHost != want.DeviceHost || got.SSHPort != want.SSHPort {
		t.Errorf("Payload() = %+v, want %+v", got, want)
	}
	if got.Secrets["username"] != "admin" {
		t.Errorf("Payload().Secrets = %v, want username=admin", got.Secrets)
	}

	got.DeviceHost = "192.0.2.99"
	got.SSHPort = 1
	if d.SSHHost() != "10.0.0.1" || d.SSHPort() != 2222 {
		t.Errorf("editing Payload()'s copy changed the device: host %q port %d", d.SSHHost(), d.SSHPort())
	}
}
