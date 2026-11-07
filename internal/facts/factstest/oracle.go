// Package factstest: the store as it was before seen and changed were split
// (Phase 41h), kept as the reference every store's derived reads are held to.
//
// Before 41h, a stored value carried its last confirmation in place, and each
// observation that confirmed it moved it (the ent store's confirm). Now a
// store writes one observation per gather and reads the last confirmation
// back from them (facts.Sightings). The oracle is the old algorithm, in
// memory, line for line with the ent store it came from, so a test can replay
// one sequence of observations into both and require the same answers.
package factstest

import (
	"sort"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// oracleRow is one stored value with its last confirmation kept in place.
type oracleRow struct {
	e       facts.Entry
	deleted bool
}

// oracle is the mutable store for one device, rows in the order written.
type oracle struct {
	rows []*oracleRow
}

// record stores obs as the ent store did before 41h.
func (o *oracle) record(obs facts.Observation) {
	at := facts.StoredTime(obs.At)
	byKey := map[string][]*oracleRow{}
	for _, row := range o.rows {
		if !row.deleted {
			byKey[row.e.Key] = append(byKey[row.e.Key], row)
		}
	}
	for _, e := range obs.Entries {
		rows := byKey[e.Key]
		if e.Kind == facts.KindVolatile {
			o.recordVolatile(rows, e, obs.By, at)
			continue
		}
		cur, ok := o.valueAt(rows, obs.By.Order, at)
		if ok && cur.e.Hash == e.Hash {
			confirm(cur, obs.By, at)
			continue
		}
		o.create(e, facts.KindStable, at, obs.By)
		prior := make([]facts.Entry, len(rows))
		for i, row := range rows {
			prior[i] = row.e
		}
		if back, ok := facts.Resumed(prior, facts.Entry{Key: e.Key, Hash: e.Hash, ObservedAt: at, By: obs.By}); ok {
			o.create(back, facts.KindStable, back.ObservedAt, back.By)
		}
	}
}

// recordVolatile is the ent store's recordVolatile.
func (o *oracle) recordVolatile(rows []*oracleRow, e facts.Entry, by facts.Provenance, at time.Time) {
	var held []*oracleRow
	for _, row := range rows {
		if row.e.Kind == facts.KindVolatile {
			held = append(held, row)
		}
	}
	if len(held) > 0 {
		newest := held[len(held)-1]
		if after, _ := facts.Later(by.Order, at, newest.e.LastBy.Order, newest.e.LastSeenAt); !after {
			return
		}
		if newest.e.Hash == e.Hash && len(held) == 1 {
			confirm(newest, by, at)
			return
		}
		for _, row := range held {
			row.deleted = true
		}
	}
	o.create(e, facts.KindVolatile, at, by)
}

// confirm moves row's last confirmation when the observation came later.
func confirm(row *oracleRow, by facts.Provenance, at time.Time) {
	if after, _ := facts.Later(by.Order, at, row.e.LastBy.Order, row.e.LastSeenAt); after {
		row.e.LastSeenAt, row.e.LastBy = at, by
	}
}

// create writes a row first observed and last confirmed at at by by.
func (o *oracle) create(e facts.Entry, kind facts.Kind, at time.Time, by facts.Provenance) {
	o.rows = append(o.rows, &oracleRow{e: facts.Entry{Key: e.Key, Value: e.Value, Hash: e.Hash, Kind: kind, ObservedAt: at, LastSeenAt: at, By: by, LastBy: by}})
}

// valueAt is the ent store's valueAt over rows.
func (o *oracle) valueAt(rows []*oracleRow, order facts.Order, at time.Time) (*oracleRow, bool) {
	var found *oracleRow
	for _, row := range rows {
		if row.e.Kind != facts.KindStable {
			continue
		}
		if after, _ := facts.Later(row.e.By.Order, row.e.ObservedAt, order, at); after {
			continue
		}
		if found == nil {
			found = row
			continue
		}
		if after, _ := facts.Later(found.e.By.Order, found.e.ObservedAt, row.e.By.Order, row.e.ObservedAt); !after {
			found = row
		}
	}
	return found, found != nil
}

// current is the ent store's Current.
func (o *oracle) current() []facts.Entry {
	current := map[string]facts.Entry{}
	for _, row := range o.rows {
		if row.deleted {
			continue
		}
		cur, ok := current[row.e.Key]
		if !ok {
			current[row.e.Key] = row.e
			continue
		}
		if after, _ := facts.Later(cur.LastBy.Order, cur.LastSeenAt, row.e.LastBy.Order, row.e.LastSeenAt); !after {
			current[row.e.Key] = row.e
		}
	}
	out := make([]facts.Entry, 0, len(current))
	for _, e := range current {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// history is the ent store's History.
func (o *oracle) history(key string) []facts.Entry {
	var stable, volatile []facts.Entry
	for _, row := range o.rows {
		if row.deleted || row.e.Key != key {
			continue
		}
		if row.e.Kind == facts.KindVolatile {
			volatile = append(volatile, row.e)
			continue
		}
		stable = append(stable, row.e)
	}
	return append(facts.InRunOrder(stable), volatile...)
}
