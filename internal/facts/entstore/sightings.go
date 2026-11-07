// Package entstore: what a record reads of when the values it compares with
// were last confirmed (Phase 41h), and the row a key held as of a run.
package entstore

import (
	"context"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// lastSightings is each row of byKey as an entry, with its last confirmation
// where the record needs it: the value a volatile key holds, and the value a
// stable key held as of obs when obs changes it (facts.Resumed reads when that
// value was last confirmed). Every other row reads as first observed.
//
// Reading every observation since a row was written would make each gather
// cost more as the device's history grows, so the database is asked first
// whether any observation since then could have confirmed it at or after obs
// (notEarlierThan). When none could, the answer is known without reading
// them: a volatile value last confirmed before obs is replaced or confirmed,
// and no stable value comes back after a late change. Only otherwise, a late
// batch, are they read and folded (facts.Sightings).
func lastSightings(ctx context.Context, tx *ent.Tx, device int, obs facts.Observation, byKey map[string][]*ent.Fact) (map[int]facts.Entry, error) {
	at := facts.StoredTime(obs.At)
	last := map[int]facts.Entry{}
	for _, rows := range byKey {
		for _, row := range rows {
			last[row.ID] = entry(row)
		}
	}
	var needed []*ent.Fact
	exact := false
	for _, e := range obs.Entries {
		rows := byKey[e.Key]
		var row *ent.Fact
		if e.Kind == facts.KindVolatile {
			if held := volatileRows(rows); len(held) > 0 {
				row = held[len(held)-1]
				// A held value first observed at or after obs: only the
				// fold says whether obs is later than its last confirmation.
				if newest := last[row.ID]; !laterThan(facts.Entry{By: obs.By, ObservedAt: at}, newest) {
					exact = true
				}
			}
		} else {
			if cur, ok := valueAt(rows, obs.By.Order, at); ok && cur.Hash != e.Hash {
				row = cur
			}
		}
		if row != nil {
			needed = append(needed, row)
		}
	}
	if len(needed) == 0 {
		return last, nil
	}
	from := needed[0].ObservationSeq
	for _, row := range needed {
		from = min(from, row.ObservationSeq)
	}
	if !exact {
		later, err := anyNotEarlier(ctx, tx, device, from, obs.By.Order, at)
		if err != nil {
			return nil, err
		}
		if !later {
			return last, nil
		}
	}
	seen, err := observationsAfter(ctx, txClient{tx}, device, from)
	if err != nil {
		return nil, err
	}
	var stored []facts.Stored
	var ids []int
	for _, rows := range byKey {
		for _, row := range rows {
			stored = append(stored, facts.Stored{Entry: last[row.ID], Seq: row.ObservationSeq})
			ids = append(ids, row.ID)
		}
	}
	for i, e := range facts.Sightings(stored, seen) {
		last[ids[i]] = e
	}
	return last, nil
}

// valueAt is the stable row a key held as of (order, at), of rows (the key's,
// in the order written): facts.ValueAt's choice, as the row it was read from.
func valueAt(rows []*ent.Fact, order facts.Order, at time.Time) (*ent.Fact, bool) {
	var found *ent.Fact
	var foundEntry facts.Entry
	for _, row := range rows {
		if row.Kind != entfact.KindStable {
			continue
		}
		e := entry(row)
		if after, _ := facts.Later(e.By.Order, e.ObservedAt, order, at); after {
			continue
		}
		if found == nil || !laterThan(foundEntry, e) {
			found, foundEntry = row, e
		}
	}
	return found, found != nil
}

// volatileRows is the volatile rows of rows, in the order written.
func volatileRows(rows []*ent.Fact) []*ent.Fact {
	var held []*ent.Fact
	for _, row := range rows {
		if row.Kind == entfact.KindVolatile {
			held = append(held, row)
		}
	}
	return held
}
