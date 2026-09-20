// Package external: the device a child process hands its method, built from
// the request.
package external

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Device adapts a wire.DispatchPayload back into the full
// inventory.InventoryItem port, which is what a Collection method is
// handed. It is how a device crosses a process boundary: the parent sends
// the device's identity, address and capabilities as data, and the child
// rebuilds an InventoryItem from them.
//
// It lives in pkg/ rather than beside the Runner that first needed it, so
// an external Collection built outside this repository receives exactly
// the device type the Runner's own per-task child does. One adapter for
// both is what makes a method's view of its device the same whichever
// side of the boundary runs it.
//
// This is a deliberate Adapter, not an Interface Segregation narrowing of
// the method signature. pkg/collection.Method is pinned to the full,
// 14-method InventoryItem, so something has to satisfy that whole
// interface; a small adapter built from what the wire actually carries is
// simpler than changing every method.
//
// Two simplifications are load-bearing enough to name explicitly rather
// than leave implicit:
//
//   - HasCapability is a trusted-relay membership check against
//     payload.Capabilities, not a re-derivation of the structural
//     Declares(name) && capability.Implements(...) double-check a real
//     concrete device type performs. That is sound because the sender ran
//     that structural check against the real device object before it
//     sent the payload: the Controller does in the same call that builds a
//     dispatch (internal/dispatch/worker_devices.go's
//     admitAndDispatchDevice, via engine.CapabilityAdmits), and the engine
//     does before any Collection method is called at all. A capability
//     whose transport needs more device-specific accessors than
//     SSHTransportCapable's host and port would need its own new wire
//     field, the same way SSHPort was added for this one; this is not a
//     generic solution.
//   - State always reports StateActive, because the lifecycle check
//     already ran on the sending side before the payload existed. The
//     receiving side has no inventory backend of its own to re-derive the
//     device's true current state from, by design: execution is stateless
//     (AGENTS.md's Architecture Principle 6).
//
// AddInfo, RemoveInfo, Version, History, and Source have no meaningful
// answer here at all (there is no backing store to mutate or read audit
// history from), so they report a clear "not supported here" error or an
// empty value rather than silently pretending to succeed.
type Device struct {
	payload wire.DispatchPayload
}

// NewDevice builds the InventoryItem adapter for payload.
func NewDevice(payload wire.DispatchPayload) *Device {
	return &Device{payload: payload}
}

var _ inventory.InventoryItem = (*Device)(nil)
var _ capability.SSHTransportCapable = (*Device)(nil)

// Payload returns the dispatch payload this device was built from. It is
// a copy of the struct, so a caller cannot change what the device reports
// by editing it, though the slices and map inside are shared.
func (d *Device) Payload() wire.DispatchPayload { return d.payload }

// ID implements inventory.InventoryItem.
func (d *Device) ID() inventory.DeviceID { return inventory.DeviceID(d.payload.DeviceID) }

// Name implements inventory.InventoryItem.
func (d *Device) Name() string { return d.payload.DeviceName }

// Properties implements inventory.InventoryItem. "host" mirrors every real
// device type's own property key for its management address
// (pkg/wire.DispatchPayload's own doc comment records why "host", never
// "ip"); "port" carries SSHPort for any caller that reads properties
// directly instead of going through SSHTransportCapable.
func (d *Device) Properties() inventory.Properties {
	return inventory.NewProperties(map[string]inventory.PropertyValue{
		"host": d.payload.DeviceHost,
		"port": d.payload.SSHPort,
	})
}

// Tags implements inventory.InventoryItem. The wire payload carries no
// tags: target resolution, the only thing that reads them, has already
// happened on the sending side.
func (d *Device) Tags() []inventory.Tag { return nil }

// HasCapability implements inventory.InventoryItem as a trusted-relay
// check; see this type's own doc comment for why trusting the relayed set
// rather than re-deriving it structurally is sound here.
//
// It resolves the capability hierarchy rather than testing set
// membership, which is the one thing an exact-match loop got wrong. A
// real device type answers through record.Base.Declares, which runs
// capability.Resolves, so a device declaring the concrete SystemdCapable
// satisfies a method requiring the broad ServiceManagerCapable. An
// exact-match loop here answered false for that same pair, which meant
// the identical method against the identical device succeeded on the
// Crawl tier and was refused on the Walk tier. A capability check that
// disagrees with itself depending on which binary is running is worse
// than either answer, because the runbook that proves out on a laptop is
// the one that fails in the mesh.
func (d *Device) HasCapability(name capability.Name) bool {
	declared := make(map[capability.Name]struct{}, len(d.payload.Capabilities))
	for _, c := range d.payload.Capabilities {
		declared[c] = struct{}{}
	}
	return capability.Resolves(declared, name)
}

// Capabilities implements inventory.InventoryItem.
func (d *Device) Capabilities() []capability.Name {
	// A fresh slice per call, never the payload's own backing array: this
	// matches internal/inventory/record.Base.Capabilities, which builds
	// its result from a map and therefore cannot be aliased either. A
	// caller holding the live slice could otherwise rewrite what this
	// device reports it can do, and HasCapability is what gates transport
	// selection and dispatch admission, so that mutation would silently
	// re-gate a later task in the same run.
	out := make([]capability.Name, len(d.payload.Capabilities))
	copy(out, d.payload.Capabilities)
	return out
}

// AddInfo implements inventory.InventoryItem. Unsupported: there is no
// inventory backend on this side of the boundary to persist a mutation to.
func (d *Device) AddInfo(_ string, _ inventory.PropertyValue, _ bool) error {
	return fmt.Errorf("external.Device: AddInfo is not supported here, since this side of the process boundary holds no inventory backend")
}

// RemoveInfo implements inventory.InventoryItem. Unsupported, for the same
// reason as AddInfo.
func (d *Device) RemoveInfo(_ string) error {
	return fmt.Errorf("external.Device: RemoveInfo is not supported here, since this side of the process boundary holds no inventory backend")
}

// ShowInfo implements inventory.InventoryItem, returning the same view as
// Properties.
func (d *Device) ShowInfo() inventory.Properties { return d.Properties() }

// Version implements inventory.InventoryItem. Always 0: the wire payload
// carries no revision history.
func (d *Device) Version() uint64 { return 0 }

// History implements inventory.InventoryItem. Always empty, for the same
// reason as Version.
func (d *Device) History() []inventory.Revision { return nil }

// State implements inventory.InventoryItem, always reporting StateActive;
// see this type's own doc comment for why that is sound here.
func (d *Device) State() inventory.LifecycleState { return inventory.StateActive }

// Source implements inventory.InventoryItem. The wire payload carries no
// sync-plugin provenance, so this reports the zero value.
func (d *Device) Source() inventory.SourceAuthority { return inventory.SourceAuthority{} }

// SSHHost implements capability.SSHTransportCapable.
func (d *Device) SSHHost() string { return d.payload.DeviceHost }

// SSHPort implements capability.SSHTransportCapable.
func (d *Device) SSHPort() int { return d.payload.SSHPort }
