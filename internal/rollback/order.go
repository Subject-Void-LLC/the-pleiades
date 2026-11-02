// Package rollback: which of two runs came later on a device, decided
// without a clock wherever the journals allow (Phase 40a).
//
// A journal's wall-clock times are one machine's clock on the Crawl tier,
// which can be stepped back between two runs, and two Runners' clocks on the
// Walk tier, which can disagree. Neither bounds the error, so a later run
// could read as an earlier one and a rollback undo beneath it. The planner
// orders runs instead by what the journals record for exactly this:
//
//   - On the Walk tier, each entry's LeaseRevision: the fencing token of the
//     device lease the Runner held, which only grows for a device. Another
//     job is later on a device when its highest revision there is above the
//     target's lowest, so a target redelivered under a new lease is still
//     ordered from its first attempt, as its first start was. Revisions are
//     compared only within one epoch of the lock bucket (lock.Fenced), since
//     a re-created bucket counts from one again. Only an
//     exclusive lease's token is recorded: holders of a shared lease (a
//     Kubernetes cluster's) overlap, so their entries carry none and order
//     by clock.
//   - On the Crawl tier, each run's start tick on the project counter
//     (RunTick, on every entry) and its end tick (in its seal). Another run
//     is later when it ended after the target began: its end tick is above
//     the target's start tick. A run that died before its seal is bounded by
//     the start tick of the first run that found it gone (internal/journal's
//     clock_live.go); one with no end tick even so was still going after the
//     target began, so it counts as later, and --despite-run accepts it. A
//     target journaled before the counter existed is older than every run
//     that carries a tick, so such a run counts as later than it.
//
// A pair of runs either of which predates this (no tick, no revision) is
// ordered by its clock times as before, and the planner says so.
package rollback

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// orderBasis says how two runs were ordered.
type orderBasis int

const (
	// byLogical is an order read from ticks or lease revisions.
	byLogical orderBasis = iota
	// byClock is an order read from wall-clock times, for a journal written
	// before ticks and revisions existed.
	byClock
	// byUnfinished is a run with a start tick and no end tick: it may have
	// changed a device at any time after it began, so it counts as later.
	byUnfinished
)

// laterOn returns the devices among devices on which other may have changed
// something after target began, and how that was decided.
func laterOn(target, other Run, devices map[string]bool) (map[string]bool, orderBasis) {
	later := map[string]bool{}
	if targetTick, otherTick := startTick(target), startTick(other); targetTick != 0 && otherTick != 0 {
		basis := byLogical
		after := other.EndTick > targetTick
		if other.EndTick == 0 {
			basis, after = byUnfinished, true
		}
		if after {
			for d := range devices {
				later[d] = true
			}
		}
		return later, basis
	}
	// Every run with a tick came after every run without one: a target from
	// before the counter is older than a run that carries one. The reverse
	// (a ticked target, an unticked other) is left to the clock, since an
	// older binary can still write an unticked run after a ticked one.
	if startTick(target) == 0 && startTick(other) != 0 {
		for d := range devices {
			later[d] = true
		}
		return later, byLogical
	}
	basis := byLogical
	clockLater := lastFinish(other).After(firstStart(target))
	for d := range devices {
		// A run that recorded nothing on d changed nothing there, whatever
		// its order: there is nothing to decide.
		if !touches(other, d) {
			continue
		}
		t, epoch, tok := lowestRevision(target, d)
		o, ook := highestRevision(other, d, epoch)
		if tok && ook {
			later[d] = o > t
			continue
		}
		// Either run's entries on d predate lease revisions, ran under a
		// shared or a lost lease, or come from another epoch of the lock
		// bucket: none of those orders by revision.
		basis = byClock
		later[d] = clockLater
	}
	return later, basis
}

// touches reports whether run recorded any entry on device.
func touches(run Run, device string) bool {
	for _, e := range run.Entries {
		if e.DeviceID == device {
			return true
		}
	}
	return false
}

// startTick is the project counter as run began, from its entries; zero for a
// run journaled before the counter existed.
func startTick(run Run) uint64 {
	for _, e := range run.Entries {
		if e.RunTick != 0 {
			return e.RunTick
		}
	}
	return 0
}

// lowestRevision is the lowest lease revision run's entries on device
// record, the epoch it belongs to, and whether any does.
func lowestRevision(run Run, device string) (uint64, string, bool) {
	var low uint64
	var epoch string
	for _, e := range run.Entries {
		if e.DeviceID == device && e.LeaseRevision != 0 && e.LeaseEpoch != "" && (low == 0 || e.LeaseRevision < low) {
			low, epoch = e.LeaseRevision, e.LeaseEpoch
		}
	}
	return low, epoch, low != 0
}

// highestRevision is the highest lease revision run's entries on device
// record within epoch, and whether any does. An entry with no revision, or
// one from another epoch, cannot be ordered against it, so when run has any
// such entry on device the answer is false and the pair is ordered by clock.
func highestRevision(run Run, device, epoch string) (uint64, bool) {
	var high uint64
	for _, e := range run.Entries {
		if e.DeviceID != device {
			continue
		}
		if e.LeaseRevision == 0 || e.LeaseEpoch != epoch {
			return 0, false
		}
		high = max(high, e.LeaseRevision)
	}
	return high, high != 0
}

// entryAfter reports whether a was recorded after b, two entries undoing one
// step of one undo (fullyUndone), without a clock where the journals allow:
// one run's entries by attempt then sequence, two Walk jobs' by lease
// revision, two Crawl runs' by start tick, and by clock only when an entry
// carries neither.
func entryAfter(a, b engine.JournalEntry) bool {
	switch {
	case a.RunID == b.RunID && a.JobID == b.JobID:
		if a.Attempt != b.Attempt {
			return a.Attempt > b.Attempt
		}
		return a.Sequence > b.Sequence
	case a.LeaseRevision != 0 && b.LeaseRevision != 0 && a.DeviceID == b.DeviceID && a.LeaseEpoch != "" && a.LeaseEpoch == b.LeaseEpoch:
		return a.LeaseRevision > b.LeaseRevision
	case a.RunTick != 0 && b.RunTick != 0:
		return a.RunTick > b.RunTick
	}
	return a.FinishedAt.After(b.FinishedAt)
}

// firstStart is when a run's earliest recorded task began.
func firstStart(run Run) time.Time {
	var first time.Time
	for _, e := range run.Entries {
		if !e.StartedAt.IsZero() && (first.IsZero() || e.StartedAt.Before(first)) {
			first = e.StartedAt
		}
	}
	return first
}

// lastFinish is when a run's latest recorded task finished.
func lastFinish(run Run) time.Time {
	var last time.Time
	for _, e := range run.Entries {
		if e.FinishedAt.After(last) {
			last = e.FinishedAt
		}
	}
	return last
}
