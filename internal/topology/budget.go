// The one number an operator states about a disrupted link, and the
// retention constants derived from it.
//
// Before Phase 96c the three retention-shaped constants in this package
// were independent literals that happened to sit near each other:
// streamMaxAge 7 days, streamDuplicateWindow 2 minutes, and the dedup
// bucket TTL 24 hours. Nothing documented a relationship and nothing
// enforced one. The measured consequence was that the duplicate window
// (2 minutes) and the point at which a severed client used to give up
// permanently (2 minutes 3 seconds) sat three seconds apart by pure
// coincidence, so a retry driven by watching the connection die was
// already outside the window meant to make it safe.
//
// This file replaces the coincidence with a derivation. One value is
// stated, everything retention-shaped is computed from it, and the
// relationships are asserted by tests rather than described in prose.

package topology

import (
	"fmt"
	"time"
)

// OutageBudget is the longest link outage a deployment promises to
// survive.
//
// It is the single operator-facing number Phase 83's setup wizard asks
// for ("what is the longest link outage this deployment must survive?"),
// and the only input to the derivations below.
//
// It is a named type rather than a bare time.Duration so it cannot be
// passed where an unrelated duration is expected, and its zero value is
// deliberately not a budget: every constructor that takes one rejects
// zero rather than quietly substituting a default, for the same reason
// StreamRole counts from iota+1.
type OutageBudget time.Duration

const (
	// DefaultOutageBudget is what a deployment gets when the operator
	// states nothing.
	//
	// Thirty minutes is chosen against the deployment targets the
	// specifications actually quantify rather than picked: a datacenter
	// answers "minutes", a GEO satellite link answers "twenty-minute
	// worst case", and a low-earth-orbit pass gap is tens of minutes. It
	// is also roughly fifteen times the 2 minutes 3 seconds at which the
	// pre-Phase-96a client gave up for good, which puts enough distance
	// between the two that nobody mistakes the relationship for luck.
	DefaultOutageBudget = OutageBudget(30 * time.Minute)

	// MinOutageBudget and MaxOutageBudget bound what an operator may
	// state. Below a minute the derived windows stop covering an ordinary
	// broker restart. Above twelve hours the derived retention holds
	// dispatch payloads, which carry the credentials a job runs with, on
	// an unauthenticated broker for longer than any of this is designed
	// for; a deployment that genuinely needs longer is a store and
	// forward problem rather than a configuration one.
	MinOutageBudget = OutageBudget(time.Minute)
	MaxOutageBudget = OutageBudget(12 * time.Hour)
)

// maxAgeMultiplier and dedupTTLFloorMultiplier express the two
// relationships this package promises, as multipliers rather than as
// literals, so changing the budget cannot leave one of them behind.
//
// maxAgeMultiplier is 336 because 336 times the default budget is
// exactly the 7 days this stream already retained, which makes the first
// release a pure refactor for MaxAge rather than a live retention change
// on every existing install.
const (
	maxAgeMultiplier        = 336
	dedupTTLFloorMultiplier = 2
)

// Duration returns b as a plain time.Duration.
func (b OutageBudget) Duration() time.Duration { return time.Duration(b) }

// String renders the budget the way an operator wrote it.
func (b OutageBudget) String() string { return time.Duration(b).String() }

// Valid reports whether b is inside the accepted range.
func (b OutageBudget) Valid() bool { return b >= MinOutageBudget && b <= MaxOutageBudget }

// ParseOutageBudget turns an operator-supplied string into a budget,
// applying the default for an empty one and refusing anything outside
// the accepted range.
//
// It lives here rather than in each composition root so the bounds exist
// once. Two binaries parsing the same variable with their own copies of
// the range is how they eventually disagree about what is legal, which
// is the same class of defect as the duplicated getenv helpers this
// module already carries.
func ParseOutageBudget(raw string) (OutageBudget, error) {
	if raw == "" {
		return DefaultOutageBudget, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid outage budget %q: %w (write a Go duration, for example 30m or 2h)", raw, err)
	}

	b := OutageBudget(d)
	if !b.Valid() {
		return 0, fmt.Errorf(
			"outage budget %s is outside the accepted range %s to %s: below the minimum the derived windows stop covering an ordinary broker restart, and above the maximum a dispatch payload's credentials would be retained on the broker longer than this design intends",
			b, MinOutageBudget, MaxOutageBudget)
	}
	return b, nil
}

// DerivedMaxAge is how long the stream retains a message, derived from b.
//
// It must exceed the budget by a wide margin rather than merely equal it:
// retention has to cover the outage plus however long the fleet then
// takes to drain the backlog, and it doubles as the audit window an
// operator reads history from.
func DerivedMaxAge(b OutageBudget) time.Duration {
	return time.Duration(b) * maxAgeMultiplier
}

// DerivedDuplicateWindow is how long the server remembers a
// Nats-Msg-Id, derived from b but CAPPED, which is the one place the
// derivation is deliberately not linear.
//
// The cap exists because a second mechanism already depends on this
// window being much shorter than the stale fan-out reclaim interval.
// internal/dispatch.Reaper republishes a job that has sat stale for
// DefaultFanOutLeaseTTL, and its own doc comment explains that this is
// safe "precisely because it is not a retry within that window": the
// original publish's dedup window has long since closed by the time a
// reap tick fires. Letting the duplicate window grow past that reclaim
// interval would silently convert the Reaper's republish from a fresh
// delivery into a suppressed duplicate, so a stranded job would stop
// being recovered at all.
//
// Half the reclaim interval keeps a clear margin on both sides. The
// budget still raises the window from the two minutes that made the
// original coincidence dangerous, and the cap is asserted by a test
// rather than left to this comment.
func DerivedDuplicateWindow(b OutageBudget) time.Duration {
	const reclaimInterval = 10 * time.Minute
	capped := reclaimInterval / 2

	if d := time.Duration(b); d < capped {
		return d
	}
	return capped
}

// DerivedDedupTTLFloor is the shortest application-level dedup memory
// that is coherent with b.
//
// The bucket TTL is not derived directly because it is bucket-wide and
// serves consumers with their own lifetimes; what the budget imposes is
// a floor. A dedup memory shorter than the outage it is meant to cover
// is worse than none, because it looks like protection and is not.
func DerivedDedupTTLFloor(b OutageBudget) time.Duration {
	return time.Duration(b) * dedupTTLFloorMultiplier
}
