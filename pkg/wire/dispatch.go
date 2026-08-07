// Package wire holds the DTOs that cross a process boundary on this
// platform's own wire, rather than living duplicated inside each process
// that happens to need one.
//
// This is PLAN.md Section 25's "wire contract package" primitive: the
// entry in that section's own table reads "every DTO crossing the bus or
// the API", names the Controller, the Runner, the Python callback bridge,
// and the UI as its consumers, and assigns the general package, everything
// this doc comment is not, to Phase 15. What this phase (14) owns is
// narrower: moving DispatchPayload specifically out of its two duplicated
// definitions (internal/api/dispatcher.go and internal/runner/agent.go)
// and into one place, ahead of the general package's own construction.
// internal/api/respond.go's Link type records the identical split for the
// identical reason: Section 25's rule is that a contract has exactly one
// implementation, and a type two packages both define separately, kept
// hand-synchronized by whoever edits either one, already violates that
// rule whether or not pkg/wire itself exists yet to hold the fix. Link
// stays in internal/api a little longer only because it also needs
// auth.LinkRel, which cannot follow it here (pkg/ may not import
// internal/, enforced by internal/archtest's TestPkgNeverImportsInternal);
// DispatchPayload carries no such dependency, so nothing blocks moving it
// now instead of waiting for Phase 15.
//
// DispatchPayload's shape changed in the move, in two ways, both fixes to
// real bugs in the two duplicates it replaces rather than a cosmetic
// rename:
//
//   - The old field was named DeviceIP. Every concrete device type in this
//     codebase (internal/inventory/devices/cisco's Router and Switch,
//     internal/inventory/devices/linux's Server) exposes its management
//     address through an SSHHost() method that reads a property named
//     "host", never "ip". A field called DeviceIP was naming a property
//     that does not exist on any device this platform actually dispatches
//     to. DeviceHost names the property a publisher will really have in
//     hand.
//   - The old dispatch code (internal/api/dispatcher.go, before this
//     phase wires this package in) populated its DeviceName field from
//     device.ID(), not device.Name(): a real bug, since
//     pkg/inventory.InventoryItem declares ID() and Name() as two
//     distinct methods with two distinct meanings. DispatchPayload carries
//     both DeviceID and DeviceName as separate fields so a later publisher
//     has a place to put each value correctly, rather than one field two
//     different call sites could each be tempted to fill from whichever
//     accessor happens to compile.
//
// This package must never import anything under internal/: pkg/ is this
// project's public SDK surface, and internal/archtest's
// TestPkgNeverImportsInternal enforces that boundary in CI on every
// build.
package wire

// DispatchPayload is the message body the Controller publishes to NATS
// and the Runner decodes back out, one per device, when a runbook is
// dispatched against an inventory group.
//
// It is wrapped inside an event.Event envelope on the actual wire (see
// internal/event), never published bare: the JSON tags below are what the
// envelope's own Data field decodes into on the Runner side, not the
// top-level shape of a raw NATS message.
type DispatchPayload struct {
	// JobID identifies the overall dispatch operation this payload
	// belongs to. Every device targeted by a single DispatchRunbook call
	// shares the same JobID, so the Runner and any downstream audit trail
	// can group per-device outcomes back into one logical job.
	JobID string `json:"job_id"`

	// RunbookID names the runbook to execute against DeviceHost.
	RunbookID string `json:"runbook_id"`

	// DeviceID is the inventory item's stable identifier
	// (pkg/inventory.InventoryItem.ID()), distinct from its
	// human-readable DeviceName. A later publisher needs both: the ID to
	// correlate this dispatch back to the exact inventory record, and the
	// name to log or display without a second inventory lookup.
	DeviceID string `json:"device_id"`

	// DeviceName is the inventory item's display name
	// (pkg/inventory.InventoryItem.Name()). It is deliberately a separate
	// field from DeviceID, not a fallback or an alias for it, because the
	// two duplicated predecessors of this type conflated them: populating
	// DeviceName from ID() was the bug this split exists to make
	// impossible to repeat.
	DeviceName string `json:"device_name"`

	// DeviceHost is the address the Runner connects to, the value every
	// concrete device type's SSHHost() method returns. It replaces the
	// old DeviceIP field, which named a property ("ip") no device type in
	// this codebase actually populates.
	DeviceHost string `json:"device_host"`
}
