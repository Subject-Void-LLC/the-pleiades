// Package facts holds the claim rules a Runner's facts batch is held to (Phase 41b): DecideClaim is pure, and ClaimStore is the port the dispatch record implements.
package facts

import (
	"context"
	"fmt"
)

// Claim is what a Runner's facts batch says about the run that observed it:
// the delivery attempt, the Runner's name and the order its device lease
// reported (Phase 41b). The Controller holds a device's batches of one job
// to the claim its first verified batch of each attempt set.
//
// This checks consistency, not truth: a Runner that is compromised can hold
// its own claim steady and still mislabel its values (the residual Phase 105
// closes with signing). What it stops is a Runner's later batches for one
// dispatch moving to another Runner's place, or an earlier attempt's late
// batch landing after a newer attempt has taken the device.
type Claim struct {
	Attempt int
	Runner  string
	Order   Order
}

// ClaimStore holds a device's claim for one job, in the job's own record.
// ClaimBatch decides the batch next against what is held and writes the new
// claim atomically, so two replicas consuming one job cannot both take the
// same place. It returns the refusal's reason, empty when the batch is
// accepted; an error is a failed read or write, and the bus redelivers.
type ClaimStore interface {
	ClaimBatch(ctx context.Context, jobID, deviceID string, next Claim) (refusal string, err error)
}

// comparableOrders reports whether two orders say enough to be compared: both are
// known and come from the same counter life. Anything else is compared by
// the clock elsewhere, and does not refuse.
func comparableOrders(a, b Order) bool {
	return a.Known() && b.Known() && a.Source == b.Source
}

// DecideClaim applies the claim rules to a batch next against held, the
// claim the record holds (zero when none). It returns the claim to write
// and, when the batch is refused, the reason, which names only fixed text and
// the attempts involved.
//
// The rules, in the order they apply:
//   - a batch naming no Runner is refused: its place cannot be checked;
//   - with no claim held, the batch sets it;
//   - an earlier attempt than the claim's is refused: a superseded delivery
//     whose device a newer attempt has taken;
//   - a later attempt takes the claim, unless both orders compare and the
//     new one is lower: a reclaimed run cannot be older than the run it
//     replaces;
//   - within one attempt, a different Runner is refused, and two comparable
//     orders that differ are refused (one dispatch holds one lease);
//   - a lost or unknown order (a shared lease, a counter restarted) does not
//     compare and never refuses; an order the claim lacks is taken from the
//     batch, so a later batch can compare with it.
func DecideClaim(held, next Claim) (Claim, string) {
	if next.Runner == "" {
		return held, "the batch names no Runner, so its place cannot be checked"
	}
	if held.Runner == "" {
		return next, ""
	}
	switch {
	case next.Attempt < held.Attempt:
		return held, fmt.Sprintf("the batch is from attempt %d, which a later attempt %d has taken", next.Attempt, held.Attempt)
	case next.Attempt > held.Attempt:
		if comparableOrders(held.Order, next.Order) && next.Order.Token < held.Order.Token {
			return held, fmt.Sprintf("attempt %d's order is lower than attempt %d's, which it replaces", next.Attempt, held.Attempt)
		}
		return next, ""
	}
	if next.Runner != held.Runner {
		return held, fmt.Sprintf("the batch names a Runner other than the one attempt %d's claim holds", held.Attempt)
	}
	if comparableOrders(held.Order, next.Order) && next.Order.Token != held.Order.Token {
		return held, fmt.Sprintf("attempt %d reports an order that differs from the one it first reported", held.Attempt)
	}
	if !held.Order.Known() && next.Order.Known() {
		held.Order = next.Order
	}
	return held, ""
}
