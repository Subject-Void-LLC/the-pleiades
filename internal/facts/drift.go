// Package facts: drift, a device's current stable facts against one of its
// baselines (Phase 41d), computed on read by the pure Drift. The report's
// types are in drift_report.go.
package facts

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// NoBaseline is the report of a device with no baseline to compare, saying
// why.
func NoBaseline(device, reason string) DriftReport {
	rep := emptyReport(device, nil)
	rep.State, rep.Reason = DriftNoBaseline, reason
	return rep
}

// Unreadable is the report of a device whose stored files did not read:
// unknown, the reason naming what (its baselines, its facts) and why. A line
// that does not open says how that can come about, beside the plaintext of
// a release before sealing, which ErrUnsealed's own words name.
func Unreadable(device, what string, err error) DriftReport {
	rep := emptyReport(device, nil)
	rep.State, rep.Reason = DriftUnknown, fmt.Sprintf("%s could not be read: %v", what, err)
	if errors.Is(err, ErrSealed) && !errors.Is(err, ErrUnsealed) {
		rep.Reason += " (sealed under another master key, edited, or moved from another host or place)"
	}
	return rep
}

// emptyReport is a report of device against b (nil for none) with every list
// empty, never null.
func emptyReport(device string, b *Baseline) DriftReport {
	rep := DriftReport{Device: device, NotComparable: []string{}, KindChanged: []string{}, Changes: []KeyDrift{}, Unconfirmed: []UnconfirmedKey{}}
	if b != nil {
		rep.Baseline = b.Summary()
	}
	return rep
}

// family is the family a key belongs to for Unconfirmed: the key up to and
// including its first dot, or the whole key when it has none.
func family(key string) string {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		return key[:i+1]
	}
	return key
}

// currentView is a device's current entries as Drift reads them.
type currentView struct {
	// stable is each stable key's entry, the first of a key listed twice.
	stable map[string]Entry
	// volatile is each key any entry calls volatile.
	volatile map[string]bool
	// latest is each family's newest confirmation, by the clock.
	latest map[string]time.Time
	// last is the newest confirmation of any entry.
	last time.Time
}

// viewOf reads current into a currentView. A key volatile in any entry is
// never stable, whatever else claims it.
func viewOf(current []Entry) currentView {
	v := currentView{stable: map[string]Entry{}, volatile: map[string]bool{}, latest: map[string]time.Time{}}
	for _, e := range current {
		if e.Kind == KindVolatile {
			v.volatile[e.Key] = true
		}
	}
	for _, e := range current {
		if e.LastSeenAt.After(v.last) {
			v.last = e.LastSeenAt
		}
		if v.volatile[e.Key] {
			continue
		}
		if _, dup := v.stable[e.Key]; !dup {
			v.stable[e.Key] = e
		}
		if f := family(e.Key); e.LastSeenAt.After(v.latest[f]) {
			v.latest[f] = e.LastSeenAt
		}
	}
	return v
}

// confirmed reports whether e's value was confirmed by the latest gather of
// its family: last seen no earlier than the family's newest confirmation.
func (v currentView) confirmed(e Entry) bool {
	return !e.LastSeenAt.Before(v.latest[family(e.Key)])
}

// Drift compares current, a device's current entries, with its baseline b,
// at time at. Only stable keys the baseline's selector covers are compared,
// each hash in constant time (SameMAC). A key stable on one side and volatile
// on the other is never compared, and is listed in KindChanged. A store keeps
// no absence, so every difference, and every match, is held to the latest
// gather of the key's family: a value that gather did not confirm is listed
// in Unconfirmed with what it would have been, and is not drift. A device is
// drifted on a confirmed difference alone; with none it is in sync, or
// unknown when it was never gathered or no key of the baseline could be
// compared.
func Drift(b Baseline, current []Entry, at time.Time) DriftReport {
	rep := emptyReport(b.Device, &b)
	if len(current) == 0 {
		rep.State, rep.Reason = DriftUnknown, "the device's facts were never gathered"
		return rep
	}
	v := viewOf(current)
	if !v.last.IsZero() {
		last := v.last
		rep.FactsLastSeenAt, rep.FactsAgeSeconds = &last, ageOf(at, last)
	}
	inBaseline, volatileNow := map[string]bool{}, 0
	for _, p := range b.Pairs {
		if inBaseline[p.Key] {
			// A key listed twice is read once.
			continue
		}
		inBaseline[p.Key] = true
		switch {
		case v.volatile[p.Key]:
			rep.KindChanged = append(rep.KindChanged, p.Key)
			volatileNow++
			continue
		case p.NotComparable:
			rep.NotComparable = append(rep.NotComparable, p.Key)
			continue
		}
		rep.Compared++
		cur, ok := v.stable[p.Key]
		same := ok && SameMAC(p.Hash, cur.Hash)
		switch {
		case !ok:
			// Never reported by the device at all: the one absence a store
			// can show.
			rep.Changes = append(rep.Changes, keyDrift(p.Key, ChangeRemoved, &p, nil, at))
		case !v.confirmed(cur):
			would := ChangeMatching
			if !same {
				would = ChangeChanged
			}
			rep.Unconfirmed = append(rep.Unconfirmed, unconfirmedKey(p.Key, would, cur, at))
		case !same:
			rep.Changes = append(rep.Changes, keyDrift(p.Key, ChangeChanged, &p, &cur, at))
		default:
			rep.Matching++
		}
	}
	wasVolatile := map[string]bool{}
	for _, key := range b.Volatile {
		wasVolatile[key] = true
	}
	for key, e := range v.stable {
		switch {
		case inBaseline[key] || !b.Keys.Matches(key):
		case wasVolatile[key]:
			// Volatile when the baseline was taken, so it holds no value to
			// compare with: not an added key.
			rep.KindChanged = append(rep.KindChanged, key)
		case !v.confirmed(e):
			rep.Unconfirmed = append(rep.Unconfirmed, unconfirmedKey(key, ChangeAdded, e, at))
		default:
			rep.Changes = append(rep.Changes, keyDrift(key, ChangeAdded, nil, &e, at))
		}
	}
	sort.Slice(rep.Changes, func(i, j int) bool { return rep.Changes[i].Key < rep.Changes[j].Key })
	sort.Slice(rep.Unconfirmed, func(i, j int) bool { return rep.Unconfirmed[i].Key < rep.Unconfirmed[j].Key })
	sort.Strings(rep.NotComparable)
	sort.Strings(rep.KindChanged)
	switch {
	case len(rep.Changes) > 0:
		rep.State = DriftDrifted
	case rep.Compared == 0:
		rep.State = DriftUnknown
		rep.Reason = fmt.Sprintf("no key of the baseline could be compared (%d not comparable, %d volatile now), so it says nothing of this host", len(rep.NotComparable), volatileNow)
	default:
		rep.State = DriftInSync
	}
	return rep
}

// keyDrift is one key's change, its values shown only when neither holds a
// digest and its key names none.
func keyDrift(key string, change Change, base *BaselinePair, cur *Entry, at time.Time) KeyDrift {
	d := KeyDrift{Key: key, Change: change, Digest: DigestKey(key)}
	if base != nil {
		d.BaselineHash = base.Hash
		d.Digest = d.Digest || HoldsDigest(base.Value)
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

// unconfirmedKey is one key the latest gather of its family did not confirm,
// with what it would have been and when its value was last seen.
func unconfirmedKey(key string, would Change, cur Entry, at time.Time) UnconfirmedKey {
	u := UnconfirmedKey{Key: key, WouldBe: would}
	if seen := cur.LastSeenAt; !seen.IsZero() {
		u.LastSeenAt, u.AgeSeconds = &seen, ageOf(at, seen)
	}
	return u
}

// ageOf is how many whole seconds before at the time t was, never negative.
func ageOf(at, t time.Time) *int {
	age := max(int(at.Sub(t)/time.Second), 0)
	return &age
}
