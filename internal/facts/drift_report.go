// Package facts: the types of a drift report (Phase 41d), which `pleiades
// facts drift --json` prints and docs/reference/schemas/facts-drift.json
// describes from these comments.
package facts

import "time"

// DriftState is what a drift found for one device.
type DriftState string

const (
	// DriftInSync is a device with no confirmed difference from its
	// baseline and at least one key compared. Keys the latest gather of
	// their family did not confirm may still be listed in Unconfirmed.
	DriftInSync DriftState = "in_sync"
	// DriftDrifted is a device with at least one confirmed key added,
	// removed or changed.
	DriftDrifted DriftState = "drifted"
	// DriftUnknown is a device that could not be compared: never gathered,
	// its stored facts or baselines did not open, or none of its baseline's
	// keys could be compared. Reason says which.
	DriftUnknown DriftState = "unknown"
	// DriftNoBaseline is a device with no baseline to compare against.
	DriftNoBaseline DriftState = "no_baseline"
)

// Change is how one key differs from its baseline.
type Change string

const (
	// ChangeAdded is a key the device has that the baseline does not.
	ChangeAdded Change = "added"
	// ChangeRemoved is a key the baseline has that the device has never
	// reported. A store records no absence, so a key a host stopped
	// reporting keeps its last value and is unconfirmed, never removed:
	// removed is seen only against a golden baseline's keys.
	ChangeRemoved Change = "removed"
	// ChangeChanged is a key whose value differs from the baseline's.
	ChangeChanged Change = "changed"
	// ChangeMatching is a key whose value equals the baseline's. It is only
	// ever what an unconfirmed key would be (UnconfirmedKey.WouldBe), never
	// a change.
	ChangeMatching Change = "matching"
)

// DriftReport is one device's drift against one baseline.
type DriftReport struct {
	// Host is the device's inventory name, as the caller asked for it.
	Host string `json:"host"`
	// Device is the device's ID.
	Device string `json:"device"`
	// State is what was found.
	State DriftState `json:"state"`
	// Reason says why the state is unknown or no_baseline.
	Reason string `json:"reason,omitempty"`
	// Baseline is the baseline compared against, absent with no_baseline.
	Baseline *BaselineSummary `json:"baseline,omitempty"`
	// Compared is how many of the baseline's keys were compared: each one
	// matching, changed, removed or unconfirmed.
	Compared int `json:"compared"`
	// Matching is how many of those match, confirmed by the latest gather
	// of their family.
	Matching int `json:"matching"`
	// NotComparable are the baseline's keys that could not be compared: a
	// golden value carrying a digest keyed for its source device.
	NotComparable []string `json:"not_comparable"`
	// KindChanged are the keys stable on one side and volatile on the
	// other: held by the baseline and volatile now, or volatile when it was
	// taken and stable now. None is compared or counted as drift.
	KindChanged []string `json:"kind_changed"`
	// Changes are the confirmed keys that differ, in key order.
	Changes []KeyDrift `json:"changes"`
	// Unconfirmed are the keys whose current value the device's latest
	// gather of their family (the key up to its first dot) did not confirm,
	// in key order, each with what it would have been. A store keeps no
	// absence: a fact a gather stops reporting keeps its last value, so an
	// item removed from the device (a package uninstalled) shows here, not
	// as removed. None is drift, so a device whose only differences are
	// here is in sync.
	Unconfirmed []UnconfirmedKey `json:"unconfirmed"`
	// FactsLastSeenAt is when the device's facts were last confirmed, absent
	// when it has none.
	FactsLastSeenAt *time.Time `json:"facts_last_seen_at,omitempty"`
	// FactsAgeSeconds is how long before the report that was, in seconds.
	FactsAgeSeconds *int `json:"facts_age_seconds,omitempty"`
}

// BaselineSummary is a baseline as a drift names it: everything but its
// pairs, and how many there are.
type BaselineSummary struct {
	// Name is the baseline's name.
	Name string `json:"name"`
	// TakenAt is when it was taken.
	TakenAt time.Time `json:"taken_at"`
	// By is who took it.
	By string `json:"by"`
	// Reason is why.
	Reason string `json:"reason"`
	// From is the device it was taken from when golden, by its ID.
	From string `json:"from,omitempty"`
	// FromName is that device's inventory name when it was taken.
	FromName string `json:"from_name,omitempty"`
	// KeysID is the identity of the keys its hashes were keyed under.
	KeysID string `json:"keys_id"`
	// Keys are the key patterns it was taken over; empty is every stable key.
	Keys []string `json:"keys,omitempty"`
	// Pairs is how many keys it holds.
	Pairs int `json:"pairs"`
}

// KeyDrift is one key that differs from its baseline.
type KeyDrift struct {
	// Key is the fact's key.
	Key string `json:"key"`
	// Change is how it differs.
	Change Change `json:"change"`
	// BaselineHash is the baseline's keyed hash, absent for an added key.
	BaselineHash string `json:"baseline_hash,omitempty"`
	// CurrentHash is the current value's keyed hash, absent for a removed key.
	CurrentHash string `json:"current_hash,omitempty"`
	// Digest says a value holds a digest (a digest field at any depth, or a
	// key whose name ends in _digest or .digest), so neither value is
	// shown: the key and that it changed are.
	Digest bool `json:"digest"`
	// BaselineValue is the baseline's value, when there is one and it is not
	// a digest.
	BaselineValue any `json:"baseline_value,omitempty"`
	// CurrentValue is the current value, when there is one and it is not a
	// digest.
	CurrentValue any `json:"current_value,omitempty"`
	// ObservedAt is when the current value was first observed.
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	// LastSeenAt is when it was last confirmed.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// AgeSeconds is how long before the report that was, in seconds.
	AgeSeconds *int `json:"age_seconds,omitempty"`
}

// UnconfirmedKey is one key the latest gather of its family did not
// confirm.
type UnconfirmedKey struct {
	// Key is the fact's key.
	Key string `json:"key"`
	// WouldBe is what the key would be were its value confirmed: added,
	// changed or matching.
	WouldBe Change `json:"would_be"`
	// LastSeenAt is when its value was last confirmed.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// AgeSeconds is how long before the report that was, in seconds.
	AgeSeconds *int `json:"age_seconds,omitempty"`
}

// Summary is b without its pairs.
func (b Baseline) Summary() *BaselineSummary {
	return &BaselineSummary{Name: b.Name, TakenAt: b.TakenAt, By: b.By, Reason: b.Reason, From: b.From, FromName: b.FromName, KeysID: b.KeysID, Keys: append([]string(nil), b.Keys...), Pairs: len(b.Pairs)}
}
