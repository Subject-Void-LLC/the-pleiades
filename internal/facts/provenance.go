// Package facts: where a stored value came from (Phase 41b), and the order
// the system itself keeps between two observations, which replaces the clock
// as what decides a device's current value (LESSONS 371, FAILURE_PATTERNS
// 831).
package facts

import (
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Order is a run's place in an order the system keeps: the Crawl CLI's run
// counter (.pleiades/clock) or the device lease revision a Runner held. Two
// orders compare only when they come from the same Source; a counter that
// started again (a deleted clock, a recreated lock store) has a new Source.
type Order struct {
	// Source names the counter, with the life of it the token belongs to:
	// "clock:<life>" on the CLI, "lease:<store>" on the Walk tier. Empty
	// means the order is unknown (a value stored before Phase 41b).
	Source string `json:"source,omitempty"`
	// Token is the counter's value for the run.
	Token uint64 `json:"token,omitempty"`
}

// Known reports whether o says anything.
func (o Order) Known() bool { return o.Source != "" && o.Token != 0 }

// Comparable reports whether o and other come from one counter, so their
// tokens say which run came later.
func (o Order) Comparable(other Order) bool {
	return o.Known() && other.Known() && o.Source == other.Source
}

// Provenance says which run observed a value: the twin's join between a fact
// and the run, task and Runner behind it. Every field may be empty for a
// value stored before Phase 41b, which reads as unknown provenance.
type Provenance struct {
	// Job is the Controller's job ID; empty on the Crawl CLI.
	Job string `json:"job,omitempty"`
	// Run is the run's ID: the CLI's journal run ID, or the job's run on a
	// Runner.
	Run string `json:"run,omitempty"`
	// Node is the task's node ID in the run's graph.
	Node string `json:"node,omitempty"`
	// Method is the FQCN of the method that emitted the value.
	Method string `json:"method,omitempty"`
	// Attempt is the dispatch's delivery attempt on a Runner; 0 on the CLI.
	Attempt int `json:"attempt,omitempty"`
	// Runner is the Runner's ID; empty on the CLI.
	Runner string `json:"runner,omitempty"`
	// Order is the run's place in the order the system keeps.
	Order Order `json:"order,omitzero"`
	// Collection is the Collection Method came from, at the declarations the
	// core linked or its loader hashed (Phase 136b): the engine sets it, as
	// it sets the journal's, and no method writes its own. Zero for a value
	// stored before Phase 136b, which reads as unknown like every other empty
	// field here; omitzero leaves the key out, so such a value's chain hash
	// is the one it was stored with.
	Collection wire.CollectionIdentity `json:"collection,omitzero"`
}

// ErrCollectionIdentity marks a provenance whose Collection identity no
// build writes for its method.
var ErrCollectionIdentity = errors.New("a collection identity no build writes for its method")

// CheckCollection holds p's Collection to what the engine writes: zero, or
// an identity that passes wire.CollectionIdentity.Valid and agrees with
// Method. A first-party group's identity names the group Method belongs to
// (collection.FirstPartyGroupOf); a program's names a method in no
// first-party group, since a program may not use one's namespace. The rule
// reads only the static group list, so a Controller, which links no
// Collection, holds a Runner's batch to it as the CLI holds its own writes.
//
// It is a rule for writing, never for reading: the group list may grow on a
// minor release, so a program's identity stored today could fail it later.
// A reader hashes and returns what was stored, and the chain, which covers
// the identity, is what finds one edited.
func (p Provenance) CheckCollection() error {
	id := p.Collection
	if id.IsZero() {
		return nil
	}
	if err := id.Valid(); err != nil {
		return fmt.Errorf("%w: %w", ErrCollectionIdentity, err)
	}
	group, firstParty := collection.FirstPartyGroupOf(p.Method)
	switch {
	case id.Program && firstParty:
		return fmt.Errorf("%w: a program's identity for %q, a method of the first-party group %s", ErrCollectionIdentity, p.Method, group.Name)
	case !id.Program && !firstParty:
		return fmt.Errorf("%w: group %s for %q, a method in no first-party group", ErrCollectionIdentity, id.Group, p.Method)
	case !id.Program && group.Name != id.Group:
		return fmt.Errorf("%w: group %s for %q, a method of %s", ErrCollectionIdentity, id.Group, p.Method, group.Name)
	}
	return nil
}

// Observation is one task's prepared facts on one device: what Record
// stores.
type Observation struct {
	// Entries are the facts, as Prepare returned them.
	Entries []Entry
	// At is when the task finished, kept for display and as the order of
	// last resort between values whose Orders do not compare.
	At time.Time
	// By is the run and task that observed them.
	By Provenance
}

// Later reports whether an observation (order a, time ta) came after one
// (order b, time tb): by the order the system keeps when the two compare and
// differ, otherwise by the clock. Equal tokens are one dispatch (every batch
// carries the lease's one fencing token), whose own observations only the
// clock orders. The second result says the clock decided.
func Later(a Order, ta time.Time, b Order, tb time.Time) (after, byClock bool) {
	if a.Comparable(b) && a.Token != b.Token {
		return a.Token > b.Token, false
	}
	return ta.After(tb), true
}

// notAfter reports whether a happened at or before b, by the same rule.
func notAfter(a Order, ta time.Time, b Order, tb time.Time) bool {
	after, _ := Later(a, ta, b, tb)
	return !after
}

// StoredTime is t as both stores keep it: UTC, to the microsecond, which is
// as fine as PostgreSQL's timestamptz keeps a time. A time is hashed into the
// chain, so it must read back exactly as it was hashed on every dialect.
func StoredTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

// ClockSource is the Order source of the Crawl CLI's run counter
// (.pleiades/clock, Phase 40a). The counter is rebuilt from the journals
// rather than restarted, so it has one life per project; a project whose
// .pleiades was deleted loses its facts with it.
const ClockSource = "clock"

// RunOrdered is the Crawl CLI's recorder: a Store whose observations are
// ordered by the project's run counter, a tick taken when each task that
// emitted facts finished (the engine's FactOrderer), so two runs on one
// device at once order by when they observed, not by when they began
// (the reviewer's F4). Tick is journal.NextTick over the project.
type RunOrdered struct {
	Store
	Tick func() (uint64, error)
}

// ObservationOrder takes the next tick of the project's counter.
func (o RunOrdered) ObservationOrder() (Order, error) {
	tick, err := o.Tick()
	if err != nil {
		return Order{}, err
	}
	return Order{Source: ClockSource, Token: tick}, nil
}
