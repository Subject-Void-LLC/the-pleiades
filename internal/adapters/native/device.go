package native

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// wireDevice adapts a wire.DispatchPayload back into the full
// inventory.InventoryItem port, so this package can hand it to
// internal/engine's already-built, already-tested execution stack
// (Executor, TransportActionExecutor, CollectionActionExecutor) unchanged.
//
// This is a deliberate Adapter, not an Interface Segregation narrowing of
// internal/engine's own signatures. pkg/collection.Method is pinned to the
// full, 14-method InventoryItem and lives in pkg/, so no internal/engine
// signature change removes the need for something to satisfy that full
// interface; a small adapter built from what the wire actually carries is
// simpler and touches zero existing internal/engine code.
//
// Two simplifications are load-bearing enough to name explicitly rather
// than leave implicit:
//
//   - HasCapability is a trusted-relay membership check against
//     payload.Capabilities, not a re-derivation of the structural
//     Declares(name) && capability.Implements(...) double-check a real
//     concrete device type performs. This is sound only because the
//     Controller already ran that exact structural check, against the
//     real device object, in the same function call that built this wire
//     payload (internal/dispatch/worker_devices.go's admitAndDispatchDevice,
//     via engine.CapabilityAdmits). A capability whose transport binding
//     needs more device-specific accessors than SSHTransportCapable's
//     host/port would need its own new wire field, the same way SSHPort
//     was added for this one; it is not a generic solution.
//   - State always reports StateActive, because engine.LifecycleAdmits
//     already ran Controller-side before this payload was ever published.
//     The Runner has no inventory backend of its own to re-derive the
//     device's true current state from, by design: it is a stateless
//     execution mesh (AGENTS.md's Architecture Principle 6).
//
// AddInfo, RemoveInfo, Version, History, and Source have no meaningful
// answer on the Runner side at all (there is no backing store to mutate or
// read audit history from), so they report a clear "not supported here"
// error or an empty value rather than silently pretending to succeed.
type wireDevice struct {
	payload wire.DispatchPayload
}

// newWireDevice builds the InventoryItem adapter for payload.
func newWireDevice(payload wire.DispatchPayload) *wireDevice {
	return &wireDevice{payload: payload}
}

var _ inventory.InventoryItem = (*wireDevice)(nil)
var _ capability.SSHTransportCapable = (*wireDevice)(nil)

// ID implements inventory.InventoryItem.
func (d *wireDevice) ID() inventory.DeviceID { return inventory.DeviceID(d.payload.DeviceID) }

// Name implements inventory.InventoryItem.
func (d *wireDevice) Name() string { return d.payload.DeviceName }

// Properties implements inventory.InventoryItem. "host" mirrors every real
// device type's own property key for its management address
// (pkg/wire.DispatchPayload's own doc comment records why "host", never
// "ip"); "port" is new here, carrying SSHPort for any caller that reads
// properties directly instead of going through SSHTransportCapable.
func (d *wireDevice) Properties() inventory.Properties {
	return inventory.NewProperties(map[string]inventory.PropertyValue{
		"host": d.payload.DeviceHost,
		"port": d.payload.SSHPort,
	})
}

// Tags implements inventory.InventoryItem. The wire payload carries no
// tags: nothing in this package's own dispatch logic needs them (unlike
// TargetResolver.Resolve's tag-matching, which singleDeviceResolver never
// performs; see resolver.go).
func (d *wireDevice) Tags() []inventory.Tag { return nil }

// HasCapability implements inventory.InventoryItem as a trusted-relay
// membership check; see this type's own doc comment for why that is sound
// here specifically.
func (d *wireDevice) HasCapability(name capability.Name) bool {
	for _, c := range d.payload.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// Capabilities implements inventory.InventoryItem.
func (d *wireDevice) Capabilities() []capability.Name { return d.payload.Capabilities }

// AddInfo implements inventory.InventoryItem. Unsupported: the Runner has
// no inventory backend to persist a mutation to.
func (d *wireDevice) AddInfo(_ string, _ inventory.PropertyValue, _ bool) error {
	return fmt.Errorf("wireDevice: AddInfo is not supported on the Runner, which holds no inventory backend")
}

// RemoveInfo implements inventory.InventoryItem. Unsupported, for the same
// reason as AddInfo.
func (d *wireDevice) RemoveInfo(_ string) error {
	return fmt.Errorf("wireDevice: RemoveInfo is not supported on the Runner, which holds no inventory backend")
}

// ShowInfo implements inventory.InventoryItem, returning the same view as
// Properties.
func (d *wireDevice) ShowInfo() inventory.Properties { return d.Properties() }

// Version implements inventory.InventoryItem. Always 0: the wire payload
// carries no revision history.
func (d *wireDevice) Version() uint64 { return 0 }

// History implements inventory.InventoryItem. Always empty, for the same
// reason as Version.
func (d *wireDevice) History() []inventory.Revision { return nil }

// State implements inventory.InventoryItem, always reporting StateActive;
// see this type's own doc comment for why that is sound here.
func (d *wireDevice) State() inventory.LifecycleState { return inventory.StateActive }

// Source implements inventory.InventoryItem. The wire payload carries no
// sync-plugin provenance, so this reports the zero value.
func (d *wireDevice) Source() inventory.SourceAuthority { return inventory.SourceAuthority{} }

// SSHHost implements capability.SSHTransportCapable.
func (d *wireDevice) SSHHost() string { return d.payload.DeviceHost }

// SSHPort implements capability.SSHTransportCapable.
func (d *wireDevice) SSHPort() int { return d.payload.SSHPort }
