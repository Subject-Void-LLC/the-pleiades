// Package facts: drift, a device's current stable facts against one of its
// baselines (Phase 41d), computed on read by the pure Drift.
package facts

import (
	"sort"
	"strings"
	"time"
)

// DriftState is what a drift found for one device.
type DriftState string

const (
	// DriftInSync is a device whose every compared key matches its baseline.
	DriftInSync DriftState = "in_sync"
	// DriftDrifted is a device with at least one key added, removed or
	// changed.
	DriftDrifted DriftState = "drifted"
	// DriftUnknown is a device that could not be compared: never gathered,
	// or a baseline taken under other keys.
	DriftUnknown DriftState = "unknown"
	// DriftNoBaseline is a device with no baseline to compare against.
	DriftNoBaseline DriftState = "no_baseline"
)

// Change is how one key differs from its baseline.
type Change string

const (
	// ChangeAdded is a key the device has that the baseline does not.
	ChangeAdded Change = "added"
	// ChangeRemoved is a key the baseline has that the device no longer does.
	ChangeRemoved Change = "removed"
	// ChangeChanged is a key whose value differs from the baseline's.
	ChangeChanged Change = "changed"
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
	// Compared is how many of the baseline's keys were compared.
	Compared int `json:"compared"`
	// Matching is how many of those match.
	Matching int `json:"matching"`
	// NotComparable are the baseline's keys that could not be compared: a
	// golden value carrying a digest keyed for its source device.
	NotComparable []string `json:"not_comparable"`
	// Changes are the keys that differ, in key order.
	Changes []KeyDrift `json:"changes"`
	// Unconfirmed are the baseline's keys that match it but whose value the
	// device's latest gather of their family (the key up to its first dot)
	// did not confirm. A store keeps no absence: a fact a gather stops
	// reporting keeps its last value, so an item removed from the device (a
	// package uninstalled) is listed here rather than as removed. It is not
	// drift.
	Unconfirmed []string `json:"unconfirmed"`
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
	// Digest says a value holds a digest, so neither value is shown: the key
	// and that it changed are.
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

// Summary is b without its pairs.
func (b Baseline) Summary() *BaselineSummary {
	return &BaselineSummary{Name: b.Name, TakenAt: b.TakenAt, By: b.By, Reason: b.Reason, From: b.From, FromName: b.FromName, KeysID: b.KeysID, Keys: append([]string(nil), b.Keys...), Pairs: len(b.Pairs)}
}

// NoBaseline is the report of a device with no baseline to compare, saying
// why.
func NoBaseline(device, reason string) DriftReport {
	return DriftReport{Device: device, State: DriftNoBaseline, Reason: reason, NotComparable: []string{}, Changes: []KeyDrift{}, Unconfirmed: []string{}}
}

// family is the family a key belongs to for Unconfirmed: the key up to and
// including its first dot, or the whole key when it has none.
func family(key string) string {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		return key[:i+1]
	}
	return key
}

// Drift compares current, a device's current entries as a store read them
// under the keys identified by keysID, with its baseline b, at time at. Only
// stable keys the baseline's selector covers are compared, each hash in
// constant time (SameMAC); a volatile key is never compared, whichever side
// holds it. A device with no entries at all was never gathered and is
// unknown, as is a baseline under other keys.
func Drift(b Baseline, keysID string, current []Entry, at time.Time) DriftReport {
	rep := DriftReport{Device: b.Device, Baseline: b.Summary(), NotComparable: []string{}, Changes: []KeyDrift{}, Unconfirmed: []string{}}
	switch {
	case keysID == "" || b.KeysID != keysID:
		rep.State, rep.Reason = DriftUnknown, "the baseline was taken under other keys, so its hashes say nothing under these"
		return rep
	case len(current) == 0:
		rep.State, rep.Reason = DriftUnknown, "the device's facts were never gathered"
		return rep
	}
	stable, volatile := map[string]Entry{}, map[string]bool{}
	// latest is each family's newest confirmation, by the clock.
	latest := map[string]time.Time{}
	var last time.Time
	for _, e := range current {
		if e.LastSeenAt.After(last) {
			last = e.LastSeenAt
		}
		if e.Kind == KindVolatile {
			volatile[e.Key] = true
			continue
		}
		if _, dup := stable[e.Key]; !dup {
			stable[e.Key] = e
		}
		if f := family(e.Key); e.LastSeenAt.After(latest[f]) {
			latest[f] = e.LastSeenAt
		}
	}
	if !last.IsZero() {
		rep.FactsLastSeenAt, rep.FactsAgeSeconds = &last, ageOf(at, last)
	}
	inBaseline := map[string]bool{}
	for _, p := range b.Pairs {
		if inBaseline[p.Key] || volatile[p.Key] {
			// A key listed twice is read once, and one now volatile is never
			// compared.
			inBaseline[p.Key] = true
			continue
		}
		inBaseline[p.Key] = true
		if p.NotComparable {
			rep.NotComparable = append(rep.NotComparable, p.Key)
			continue
		}
		rep.Compared++
		cur, ok := stable[p.Key]
		switch {
		case !ok:
			rep.Changes = append(rep.Changes, keyDrift(p.Key, ChangeRemoved, &p, nil, at))
		case !SameMAC(p.Hash, cur.Hash):
			rep.Changes = append(rep.Changes, keyDrift(p.Key, ChangeChanged, &p, &cur, at))
		default:
			rep.Matching++
			if cur.LastSeenAt.Before(latest[family(p.Key)]) {
				rep.Unconfirmed = append(rep.Unconfirmed, p.Key)
			}
		}
	}
	for key, e := range stable {
		if !inBaseline[key] && b.Keys.Matches(key) {
			rep.Changes = append(rep.Changes, keyDrift(key, ChangeAdded, nil, &e, at))
		}
	}
	sort.Slice(rep.Changes, func(i, j int) bool { return rep.Changes[i].Key < rep.Changes[j].Key })
	sort.Strings(rep.NotComparable)
	sort.Strings(rep.Unconfirmed)
	rep.State = DriftInSync
	if len(rep.Changes) > 0 {
		rep.State = DriftDrifted
	}
	return rep
}

// keyDrift is one key's change, its values shown only when neither holds a
// digest.
func keyDrift(key string, change Change, base *BaselinePair, cur *Entry, at time.Time) KeyDrift {
	d := KeyDrift{Key: key, Change: change}
	if base != nil {
		d.BaselineHash = base.Hash
		d.Digest = HoldsDigest(base.Value)
	}
	if cur != nil {
		d.CurrentHash = cur.Hash
		d.Digest = d.Digest || HoldsDigest(cur.Value)
		observed, seen := cur.ObservedAt, cur.LastSeenAt
		if !observed.IsZero() {
			d.ObservedAt = &observed
		}
		if !seen.IsZero() {
			d.LastSeenAt, d.AgeSeconds = &seen, ageOf(at, seen)
		}
	}
	if !d.Digest {
		if base != nil {
			d.BaselineValue = base.Value
		}
		if cur != nil {
			d.CurrentValue = cur.Value
		}
	}
	return d
}

// ageOf is how many whole seconds before at the time t was, never negative.
func ageOf(at, t time.Time) *int {
	age := max(int(at.Sub(t)/time.Second), 0)
	return &age
}
