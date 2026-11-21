// Package factsreport is the document `controller facts inspect --json`
// prints: what an inspection of devices' stored fact histories found
// (internal/facts/entstore's inspect.go), published as
// docs/reference/schemas/facts-inspection.json (schema.go).
//
// It is a package of its own, holding only the model, so the store that
// fills it, tools/gendocs that publishes its schema, and the tests that hold
// the command's real output to that schema read one set of types. It names
// devices, rows, key identities, times, gather numbers and failure kinds,
// never a fact's name or value: a failing row's fact is named only on
// request, and only when its row opens.
package factsreport

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// Version is Report's schema_version.
const Version = 1

// Report is what one inspection found: the keys it read with, and each
// device it reports.
type Report struct {
	// SchemaVersion is this document's major version.
	SchemaVersion int `json:"schema_version"`
	// Keys are the identities of the facts keys the inspection read with,
	// never the keys themselves.
	Keys Keys `json:"keys"`
	// Scope is device when one device was asked for, fleet when every
	// device holding stored facts was read.
	Scope string `json:"scope"`
	// Scanned is how many devices were read.
	Scanned int `json:"scanned"`
	// NeedsRepair is how many of them hold a history no controller with
	// these keys can serve (State.NeedsRepair).
	NeedsRepair int `json:"needs_repair"`
	// Devices are the devices reported: the one asked for, or in a fleet
	// inspection every device whose state is not empty or healthy (all of
	// them with --all), in the order of their database IDs.
	Devices []Device `json:"devices"`
}

// The scopes a report can have.
const (
	// ScopeDevice is a report of the one device asked for.
	ScopeDevice = "device"
	// ScopeFleet is a report of every device holding stored facts.
	ScopeFleet = "fleet"
)

// Keys names the facts keys an inspection or a repair read with.
type Keys struct {
	// Current is the identity of PLEIADES_FACTS_DIGEST_KEY's keys, absent
	// when it was not set.
	Current string `json:"current,omitempty"`
	// Previous is the identity of PLEIADES_FACTS_DIGEST_KEY_PREVIOUS's keys,
	// absent when it was not set.
	Previous string `json:"previous,omitempty"`
}

// State is how a device's stored history stands against the keys an
// inspection was given.
type State string

// The states an inspection tells apart.
const (
	// StateEmpty: the device has no stored fact.
	StateEmpty State = "empty"
	// StateHealthy: every row is under the current key, opens as the
	// device's own, and the chain over its stable rows verifies.
	StateHealthy State = "healthy"
	// StateRekeyPending: every row is under the previous key and verifies
	// under it; a controller holding the new key with this one as previous
	// re-keys it at its next read or start.
	StateRekeyPending State = "rekey_pending"
	// StateRefused: every row is under the previous key and the history
	// does not verify under it, so a controller refuses to re-key it and
	// serves none of it.
	StateRefused State = "refused"
	// StateBroken: every row is under the current key and the history does
	// not verify: a row that does not open as the device's own, or a chain
	// that breaks.
	StateBroken State = "broken"
	// StateMixedKeys: some rows are under the current key and the rest
	// under the previous one, which no build of this release writes.
	StateMixedKeys State = "mixed_keys"
	// StateOtherKeys: some row is under a key the inspection was not given.
	StateOtherKeys State = "other_keys"
)

// KnownStates are every state this release reports, for the schema.
var KnownStates = []string{string(StateEmpty), string(StateHealthy), string(StateRekeyPending),
	string(StateRefused), string(StateBroken), string(StateMixedKeys), string(StateOtherKeys)}

// NeedsRepair reports whether no controller holding the inspection's keys
// can serve the device: every state but empty, healthy and rekey_pending.
// A state a newer release reports is read as needing repair.
func (s State) NeedsRepair() bool {
	return s != StateEmpty && s != StateHealthy && s != StateRekeyPending
}

// FailureKind is why one stored row does not verify.
type FailureKind string

// The failure kinds, in the order a row is checked: its payload, the key it
// holds, the value it holds, then (for a stable row) its chain link.
const (
	// FaultUnsealed: the payload is not a sealed envelope.
	FaultUnsealed FailureKind = "unsealed"
	// FaultDoesNotOpen: the payload is sealed but does not open bound to
	// the row's token: another row's payload, an edited one, or one sealed
	// under a MASTER_ENCRYPTION_KEY this controller does not hold.
	FaultDoesNotOpen FailureKind = "does_not_open"
	// FaultTokenMismatch: the payload opens, but under these keys the fact
	// it holds has another token for this device than the row carries: a
	// row moved from another device, or a token made under other keys.
	FaultTokenMismatch FailureKind = "token_mismatch"
	// FaultHashMismatch: the payload opens as the row's, and the value it
	// holds is not the one the row's keyed hash describes under these keys.
	FaultHashMismatch = FailureKind(facts.BreakValue)
	// FaultChainMissing: a stable row carries no chain link.
	FaultChainMissing = FailureKind(facts.BreakUnchained)
	// FaultChainLink: a stable row's link names another value than the one
	// stored before it: a gap, an insertion or a reordering.
	FaultChainLink = FailureKind(facts.BreakLink)
	// FaultChainHash: a stable row's chain hash does not match its device,
	// fact, time or provenance: one of those edited, or a link made under
	// other keys.
	FaultChainHash = FailureKind(facts.BreakChainHash)
)

// KnownFailureKinds are every failure kind this release reports, for the
// schema.
var KnownFailureKinds = []string{string(FaultUnsealed), string(FaultDoesNotOpen), string(FaultTokenMismatch),
	string(FaultHashMismatch), string(FaultChainMissing), string(FaultChainLink), string(FaultChainHash)}

// The roles a key identity can have for an inspection.
const (
	// RoleCurrent is PLEIADES_FACTS_DIGEST_KEY's keys.
	RoleCurrent = "current"
	// RolePrevious is PLEIADES_FACTS_DIGEST_KEY_PREVIOUS's keys.
	RolePrevious = "previous"
	// RoleOther is a key the inspection was not given.
	RoleOther = "other"
)

// The repairs, as a report names them.
const (
	// RepairDiscard removes a device's whole stored history.
	RepairDiscard = "discard"
	// RepairTruncate keeps the part before the first failure.
	RepairTruncate = "truncate"
)

// Device is what an inspection found of one device.
type Device struct {
	// Device is the device's inventory name.
	Device string `json:"device"`
	// ID is the device's ID, which the controller's log names.
	ID string `json:"id"`
	// State is how its stored history stands.
	State State `json:"state"`
	// Rows counts its stored facts.
	Rows Rows `json:"rows"`
	// Coverages and Observations count its other stored rows: the sets of
	// facts its gathers covered, and its gathers.
	Coverages    int `json:"coverages"`
	Observations int `json:"observations"`
	// KeyedBy is each key identity its rows name, with how many name it,
	// at most eight.
	KeyedBy []KeyedRows `json:"keyed_by"`
	// KeyedByMore counts the rows under key identities past the eight
	// named, which only an edited database holds.
	KeyedByMore int `json:"keyed_by_more,omitempty"`
	// Under is the history checked under each key the inspection was given,
	// current first.
	Under []KeyCheck `json:"under"`
	// TruncateFrom is the gather a truncate would cut the history at: the
	// one that wrote its first stable row that fails. Absent when no stable
	// row fails, or no truncate applies.
	TruncateFrom int64 `json:"truncate_from,omitempty"`
	// Repairs names the repairs that apply: discard, truncate. Empty when
	// there is nothing to repair.
	Repairs []string `json:"repairs"`
	// Advice says what the operator can do, in a sentence or two.
	Advice string `json:"advice"`
}

// Rows counts a device's stored facts.
type Rows struct {
	// Total is every row; Stable and Volatile split it by kind.
	Total    int `json:"total"`
	Stable   int `json:"stable"`
	Volatile int `json:"volatile"`
}

// KeyedRows is one key identity a device's rows name.
type KeyedRows struct {
	// KeyID is the identity, as the rows name it: never the key.
	KeyID string `json:"key_id"`
	// Role is current, previous or other.
	Role string `json:"role"`
	// Rows is how many rows name it.
	Rows int `json:"rows"`
}

// KeyCheck is a device's history checked under one set of keys.
type KeyCheck struct {
	// Role is current or previous.
	Role string `json:"role"`
	// KeyID is the identity of the keys.
	KeyID string `json:"key_id"`
	// Verifies is true when every row opens as the device's own under
	// these keys and the chain over its stable rows is whole.
	Verifies bool `json:"verifies"`
	// Checked is how many rows, in the order they were stored, passed
	// before the first that failed: all of them when none did.
	Checked int `json:"checked"`
	// Opened and Failed count the rows that open as the device's own under
	// these keys and the ones that do not, each row checked on its own.
	Opened int `json:"opened"`
	Failed int `json:"failed"`
	// Failure is the first row that fails, absent when none does.
	Failure *RowFailure `json:"failure,omitempty"`
}

// RowFailure is one stored row that fails a check.
type RowFailure struct {
	// Row is the row's ID in the facts table.
	Row int `json:"row"`
	// Position is its place among the device's rows, from 0, in the order
	// they were stored.
	Position int `json:"position"`
	// ChainIndex is its place among the device's stable rows, from 0, as
	// the fact chain route numbers a break; absent for a volatile row.
	ChainIndex *int `json:"chain_index,omitempty"`
	// Kind is why it fails.
	Kind FailureKind `json:"kind"`
	// Stable is true for a stable row, false for a volatile one.
	Stable bool `json:"stable"`
	// KeyID is the key identity the row names.
	KeyID string `json:"key_id"`
	// ObservedAt is when the value it holds was first observed.
	ObservedAt time.Time `json:"observed_at"`
	// Observation is the number of the device's gather that wrote it.
	Observation int64 `json:"observation"`
	// Fact is the fact the row holds, given only with --show-fact-names
	// and only when its payload opens.
	Fact string `json:"fact,omitempty"`
}
