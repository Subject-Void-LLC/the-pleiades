// Package facts: what a store writes after a late change, so a confirmation
// kept only as a last sighting is not lost behind it. Both stores use it.
package facts

// Resumed is the value a key came back to after a late change, and whether
// there is one to write. prior is the key's stable values as stored before
// late (each with when, and by which run, it was last confirmed); late is the
// value just stored, observed before something already stored.
//
// A store writes a value only when it changes and keeps a later observation
// of the same value as a last sighting. So when run y confirms value A at
// order 11 and run x's change to B at order 10 is stored after it, the
// history ended at B while A was current, and the next run to see A recorded
// a change nobody saw (FAILURE_PATTERNS 923). The answer is A again, first
// observed (as far as the store knows) at y's sighting, by y.
//
// There is one only when the value current as of late was confirmed after
// late and no stored value came between them: a stored value in between
// means the key's later changes are already written, and the last sighting
// may belong to a later spell of the same value.
func Resumed(prior []Entry, late Entry) (Entry, bool) {
	var before Entry
	found := false
	for _, e := range prior {
		if e.Kind == KindVolatile || !notAfter(e.By.Order, e.ObservedAt, late.By.Order, late.ObservedAt) {
			continue
		}
		// The latest at or before late; the later stored on a tie, as a
		// store's own valueAt chooses.
		if !found || notAfter(before.By.Order, before.ObservedAt, e.By.Order, e.ObservedAt) {
			before, found = e, true
		}
	}
	if !found || before.Hash == late.Hash {
		return Entry{}, false
	}
	// Confirmed after late, or there is nothing to keep.
	if after, _ := Later(before.LastBy.Order, before.LastSeenAt, late.By.Order, late.ObservedAt); !after {
		return Entry{}, false
	}
	for _, e := range prior {
		if e.Kind == KindVolatile {
			continue
		}
		afterLate, _ := Later(e.By.Order, e.ObservedAt, late.By.Order, late.ObservedAt)
		if afterLate && notAfter(e.By.Order, e.ObservedAt, before.LastBy.Order, before.LastSeenAt) {
			return Entry{}, false
		}
	}
	return Entry{
		Key: late.Key, Value: before.Value, Hash: before.Hash, Kind: KindStable,
		ObservedAt: before.LastSeenAt, LastSeenAt: before.LastSeenAt,
		By: before.LastBy, LastBy: before.LastBy,
	}, true
}

// mergeRepeats reads a history in run order with each value that follows
// itself merged into the one before it: first observed as the earlier says,
// last confirmed as whichever was confirmed later. Two stored values in a row
// can be the same when a late observation of a value lands just before a
// row Resumed wrote for it (A, B, A at 12, A at 15): the device held A from
// 12 on, which is one value, not a change from A to A (FAILURE_PATTERNS 936). The stored rows, and so the
// chain, are untouched.
func mergeRepeats(entries []Entry) []Entry {
	out := entries[:0]
	for _, e := range entries {
		if n := len(out); n > 0 && out[n-1].Hash == e.Hash && out[n-1].Kind == e.Kind {
			prev := &out[n-1]
			if after, _ := Later(e.LastBy.Order, e.LastSeenAt, prev.LastBy.Order, prev.LastSeenAt); after {
				prev.LastSeenAt, prev.LastBy = e.LastSeenAt, e.LastBy
			}
			continue
		}
		out = append(out, e)
	}
	return out
}
