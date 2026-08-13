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

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

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

	// RunbookID names what to execute against DeviceHost: a runbook id for
	// the native kind, a playbook path for the legacy one. One field
	// rather than two, because the two never coexist on one payload and a
	// second field would be empty on every dispatch.
	RunbookID string `json:"runbook_id"`

	// Kind is the launch kind this dispatch is, which is what the Runner
	// routes on to choose an execution adapter.
	//
	// An ABSENT or empty Kind means the native runbook kind, and that rule
	// lives at exactly one place (internal/adapters/routing). It is not a
	// convenience: it is what makes this field additive. A dispatch
	// published before this field existed, or by a Controller that has not
	// been upgraded yet, still routes to the adapter it was always going
	// to reach.
	//
	// The reverse direction is the one this cannot protect on its own, and
	// it is stated here so nobody has to rediscover it: every decode on
	// the Runner path is a plain json.Unmarshal with no
	// DisallowUnknownFields, so an OLD Runner receiving a NEW payload
	// silently drops this key and runs the job natively. Runners must be
	// upgraded before a template of a non-native kind is created. There is
	// no in-band mechanism that makes that safe, which is why it is a
	// deployment ordering requirement rather than a comment about one.
	//
	// No omitempty, matching Interruptible three fields below and for the
	// same reason: this struct's wire form is asserted literally in
	// pkg/wire/dispatch_test.go, and a field that sometimes vanishes makes
	// that assertion a moving target.
	Kind string `json:"kind"`

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

	// Interruptible carries runbook.Runbook.Interruptible's own resolved
	// value (itself engine.Metadata.IsInterruptible()'s answer) across
	// the wire, so the Runner can decide whether to self-abort this
	// execution on lost lease heartbeat without a second lookup back to
	// the Controller (PLAN.md Section 16's Network Partitions
	// mitigation). No omitempty, matching every other field on this
	// struct: an absent key on the wire would decode to Go's own bool
	// zero value (false, "not interruptible"), silently inverting the
	// safe default engine.Metadata's own nil-means-true convention
	// establishes upstream of this struct.
	Interruptible bool `json:"interruptible"`

	// SSHPort is the port a capability.SSHTransportCapable device reports
	// via SSHPort(), carried across the wire because the Runner has no
	// inventory backend of its own to re-derive it from (Phase 16, Native
	// Go Execution Adapter). Zero when the device does not declare
	// SSHTransportCapable; a zero value is never dialed, since dispatch is
	// only ever routed to a transport whose required capability the device
	// actually has.
	SSHPort int `json:"ssh_port"`

	// Capabilities is the device's own Capabilities() result at the moment
	// the Controller admitted it for this job (internal/dispatch's
	// CapabilityAdmits already ran the real structural check against the
	// real inventory item before this payload was built). The Runner
	// trusts this list as a membership check rather than re-deriving it,
	// which is sound only because that structural check already happened
	// upstream of this payload ever existing.
	Capabilities []capability.Name `json:"capabilities"`

	// Secrets is the flattened credential for DeviceName, resolved by the
	// Controller at dispatch time (PLAN.md Section 17's Just-in-Time
	// delivery principle: attached directly to the payload, never
	// pre-distributed to the Runner). Empty when the device has no stored
	// credential, which is not itself a dispatch failure: only a task that
	// actually needs a secret fails downstream, the same place a missing
	// credential already fails at the Walk tier. Keys follow the
	// convention internal/credential.Flatten documents ("username",
	// "password", "private_key_pem", "passphrase"). omitempty keeps a
	// credential-less dispatch's wire form free of a bare "secrets":{}.
	Secrets map[string]string `json:"secrets,omitempty"`

	// Tags is the device's own pkg/inventory.InventoryItem.Tags() result at
	// dispatch time, carried as plain strings rather than
	// []inventory.Tag: this package must never import internal/, and
	// pkg/inventory.Tag would add an unwanted cross-package coupling this
	// wire type has otherwise deliberately avoided (see this file's own
	// doc comment on DeviceID/DeviceName). Added for
	// internal/adapters/legacy (Phase 17, Legacy Ansible Adapter), whose
	// generated inventory.json needs a device's group membership and had
	// no field to read it from: Tag already maps 1:1 onto an Ansible
	// inventory group in this codebase's own worked example
	// (examples/upgrade_ios/pleiades/inventory.yaml's "tags: [catalyst_lab]"
	// pairs with examples/upgrade_ios/ansible/inventory.ini's
	// "[catalyst_lab]" group header). omitempty keeps an untagged
	// dispatch's wire form free of a bare "tags":[].
	Tags []string `json:"tags,omitempty"`

	// Fields is the resolved launch.Resolved.Fields (AWX_PARITY_ROADMAP.md
	// Section 3b.1) this dispatch was launched with: forks, limit,
	// verbosity, timeout, and whichever kind-specific fields the launch's
	// kind declares (e.g. job_tags/skip_tags for the playbook kind). It is
	// carried as plain map[string]any rather than internal/launch.Fields:
	// this package must never import internal/ (see this file's own doc
	// comment on why DeviceID/DeviceName stay plain too), and the two
	// types share an identical underlying type, so
	// internal/adapters/legacy and internal/adapters/native convert back
	// with a bare type conversion (launch.Fields(payload.Fields)) to reuse
	// launch.Fields' own typed accessors rather than re-deriving them here.
	//
	// This is the second of the two wire hops the roadmap's own Section
	// 3b.1 names: internal/dispatch.Job already captured this value on the
	// job record (the first hop); this field is what finally lets it reach
	// a Runner. omitempty keeps a dispatch launched with no fields set free
	// of a bare "fields":{}, matching Secrets and Tags above; an absent key
	// decodes to a nil map, which every reader here treats identically to
	// an empty one.
	Fields map[string]any `json:"fields,omitempty"`

	// ExtraVars is the resolved launch.Resolved.ExtraVars this dispatch was
	// launched with: the template's defaults, a saved configuration, and
	// this launch's own overrides, already merged in that precedence
	// order. Same additive, omitempty, plain-map-not-launch.Fields
	// reasoning as Fields above, and it is genuinely a separate field
	// rather than Fields["extra_vars"]: launch.Resolved itself already
	// pulls it out for the identical convenience (internal/launch's own
	// resolve.go), and duplicating that split here means a Runner never
	// has to know "extra_vars" is the one Fields key that means something
	// different from the rest.
	ExtraVars map[string]any `json:"extra_vars,omitempty"`
}
