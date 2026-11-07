// Package facts: when a stored value was last confirmed, read from the
// observations rather than kept on the value (Phase 41h, "seen versus
// changed").
//
// A store writes a value only when it changes, and a row once written is
// never edited: the history is insert-only. What every gather does write is
// one observation: the device's next sequence number, when and by which run
// it was made, and which keys it covered (its Coverage, kept once per
// distinct set). Each stored value names the observation that wrote it.
//
// From those, Sightings reads each value's last confirmation as the stores
// used to keep it in place: an observation that covered a key and wrote
// nothing for it confirmed the value the key held as of that observation,
// among the values already stored when it was made, and a confirmation counts
// when it came later than the one before it. The answers are the ones the
// mutable last_seen_at gave, observation for observation; only where they come
// from has changed.
package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"
)

// Coverage is which keys one observation covered, by kind, each list sorted.
type Coverage struct {
	Stable   []string `json:"stable,omitempty"`
	Volatile []string `json:"volatile,omitempty"`
}

// CoverageOf is the coverage of entries.
func CoverageOf(entries []Entry) Coverage {
	var c Coverage
	for _, e := range entries {
		if e.Kind == KindVolatile {
			c.Volatile = append(c.Volatile, e.Key)
			continue
		}
		c.Stable = append(c.Stable, e.Key)
	}
	sort.Strings(c.Stable)
	sort.Strings(c.Volatile)
	return c
}

// Digest names c: SHA-256, hex-encoded, over the canonical JSON of its two
// lists, so a store keeps each distinct coverage once and an observation
// names it.
func (c Coverage) Digest() string {
	stable, volatile := c.Stable, c.Volatile
	if stable == nil {
		stable = []string{}
	}
	if volatile == nil {
		volatile = []string{}
	}
	canon, err := Canonical([]any{stable, volatile})
	if err != nil {
		// Two lists of strings always encode.
		return ""
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// Seen is one stored observation as Sightings reads it.
type Seen struct {
	// Seq is the observation's place among the device's observations, from
	// 1; every value it wrote carries the same number.
	Seq int64
	// At and By are when it was made and by which run.
	At time.Time
	By Provenance
	// Covers is what it covered.
	Covers *Coverage
}

// Stored is a stored value as Sightings reads it: the entry with its first
// sighting (ObservedAt and By), and the observation that wrote it.
type Stored struct {
	Entry
	// Seq is the observation that wrote the value; 0 for one stored before
	// observations were kept, which every observation finds already there.
	Seq int64
}

// sighting is one confirmation: when, and by which run.
type sighting struct {
	at time.Time
	by Provenance
}

// later reports whether s came after t, by Later.
func (s sighting) later(t sighting) bool {
	after, _ := Later(s.by.Order, s.at, t.by.Order, t.at)
	return after
}

// Sightings returns stored as entries, each with LastSeenAt and LastBy set to
// its last confirmation: its own first sighting, moved by every observation in
// seen (taken in Seq order) that covered its key with its kind, wrote nothing
// for that key, found it to be the key's value as of the observation among
// the values stored before it, and came later than the confirmation before.
// For a stable key that value is ValueAt's; for a volatile key, the newest
// volatile value stored before it. The result is in stored's order.
func Sightings(stored []Stored, seen []Seen) []Entry {
	out := make([]Entry, len(stored))
	last := make([]sighting, len(stored))
	stable, volatile := map[string][]int{}, map[string][]int{}
	for i, s := range stored {
		out[i] = s.Entry
		last[i] = sighting{at: s.ObservedAt, by: s.By}
		if s.Kind == KindVolatile {
			volatile[s.Key] = append(volatile[s.Key], i)
			continue
		}
		stable[s.Key] = append(stable[s.Key], i)
	}
	// Each key's values in the order they were written: by observation, then
	// as given, which is how each store lists them.
	for _, rows := range []map[string][]int{stable, volatile} {
		for _, idx := range rows {
			sort.SliceStable(idx, func(a, b int) bool { return stored[idx[a]].Seq < stored[idx[b]].Seq })
		}
	}
	order := append([]Seen(nil), seen...)
	sort.SliceStable(order, func(a, b int) bool { return order[a].Seq < order[b].Seq })
	for _, o := range order {
		if o.Covers == nil {
			continue
		}
		s := sighting{at: o.At, by: o.By}
		for _, key := range o.Covers.Stable {
			if i, ok := stableTarget(stored, stable[key], o); ok && s.later(last[i]) {
				last[i] = s
			}
		}
		for _, key := range o.Covers.Volatile {
			if i, ok := volatileTarget(stored, volatile[key], o.Seq); ok && s.later(last[i]) {
				last[i] = s
			}
		}
	}
	for i := range out {
		out[i].LastSeenAt, out[i].LastBy = last[i].at, last[i].by
	}
	return out
}

// stableTarget is the value o confirmed of a stable key whose values are
// idx: none when o wrote one of them itself (o changed the key), else, of
// the values written before o, the latest at or before o, the later written
// on a tie.
func stableTarget(stored []Stored, idx []int, o Seen) (int, bool) {
	found, ok := -1, false
	for _, i := range idx {
		s := stored[i]
		if s.Seq == o.Seq {
			return -1, false
		}
		if s.Seq > o.Seq {
			break
		}
		if !notAfter(s.By.Order, s.ObservedAt, o.By.Order, o.At) {
			continue
		}
		if !ok || !isLater(stored[found].By.Order, stored[found].ObservedAt, s.By.Order, s.ObservedAt) {
			found, ok = i, true
		}
	}
	return found, ok
}

// volatileTarget is the value an observation numbered seq confirmed of a
// volatile key whose values are idx: none when it wrote one itself, else the
// newest written before it.
func volatileTarget(stored []Stored, idx []int, seq int64) (int, bool) {
	found, ok := -1, false
	for _, i := range idx {
		switch s := stored[i]; {
		case s.Seq == seq:
			return -1, false
		case s.Seq < seq:
			found, ok = i, true
		}
	}
	return found, ok
}

// ValueAt is the stable value a key held as of (order, at), of values (the
// key's, in the order they were written): of those first observed at or
// before it, the latest, the later written on a tie. It is what a store
// compares a new observation with.
func ValueAt(values []Entry, order Order, at time.Time) (Entry, bool) {
	var found Entry
	ok := false
	for _, e := range values {
		if e.Kind == KindVolatile || !notAfter(e.By.Order, e.ObservedAt, order, at) {
			continue
		}
		if !ok || !isLater(found.By.Order, found.ObservedAt, e.By.Order, e.ObservedAt) {
			found, ok = e, true
		}
	}
	return found, ok
}
