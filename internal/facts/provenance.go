// Package facts: where a stored value came from (Phase 41b), and the order
// the system itself keeps between two observations, which replaces the clock
// as what decides a device's current value (LESSONS 371, FAILURE_PATTERNS
// 831).
package facts

import (
	"context"
	"time"
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
// (order b, time tb): by the order the system keeps when the two compare,
// otherwise by the clock. The second result says the clock decided.
func Later(a Order, ta time.Time, b Order, tb time.Time) (after, byClock bool) {
	if a.Comparable(b) {
		return a.Token > b.Token, false
	}
	return ta.After(tb), true
}

// notAfter reports whether a happened at or before b, by the same rule.
func notAfter(a Order, ta time.Time, b Order, tb time.Time) bool {
	if a.Comparable(b) {
		return a.Token <= b.Token
	}
	return !ta.After(tb)
}

// ClockSource is the Order source of the Crawl CLI's run counter
// (.pleiades/clock, Phase 40a). The counter is rebuilt from the journals
// rather than restarted, so it has one life per project; a project whose
// .pleiades was deleted loses its facts with it.
const ClockSource = "clock"

// RunOrdered is a Store whose every observation is stamped with one run's
// order: what the Crawl CLI hands the engine for a run that took its tick.
type RunOrdered struct {
	Store
	Order Order
}

// Record implements Store, stamping the run's order on obs.
func (o RunOrdered) Record(ctx context.Context, device string, obs Observation) error {
	obs.By.Order = o.Order
	return o.Store.Record(ctx, device, obs)
}
