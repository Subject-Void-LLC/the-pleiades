// Package facts_test holds the table of claim-rule edge cases (Phase 41b), one row per case.
package facts_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestDecideClaim holds each edge case of the claim rules to what it must
// do, one row per case, so none of them surprises a later reader (Phase 41b).
func TestDecideClaim(t *testing.T) {
	lease := func(token uint64) facts.Order { return facts.Order{Source: "lease:e1", Token: token} }
	other := func(token uint64) facts.Order { return facts.Order{Source: "lease:e2", Token: token} }
	none := facts.Order{}
	base := facts.Claim{Attempt: 2, Runner: "r1", Order: lease(10)}
	for _, tc := range []struct {
		name    string
		held    facts.Claim
		next    facts.Claim
		refused string // a substring of the reason, or "" for accepted
		want    facts.Claim
	}{
		{name: "no claim held, the batch sets it", held: facts.Claim{}, next: base, want: base},
		{name: "a batch naming no Runner is refused", held: facts.Claim{}, next: facts.Claim{Attempt: 1, Order: lease(1)}, refused: "names no Runner"},
		{name: "same attempt, same Runner, same order: accepted", held: base, next: base, want: base},
		{name: "a redelivered batch of one dispatch is idempotent", held: base, next: base, want: base},
		{name: "same attempt, different Runner: refused", held: base, next: facts.Claim{Attempt: 2, Runner: "r2", Order: lease(10)}, refused: "other than"},
		{name: "same attempt, order moved forward: refused", held: base, next: facts.Claim{Attempt: 2, Runner: "r1", Order: lease(11)}, refused: "differs"},
		{name: "same attempt, order moved back: refused", held: base, next: facts.Claim{Attempt: 2, Runner: "r1", Order: lease(9)}, refused: "differs"},
		{name: "a lost order does not refuse, and does not claim one", held: base, next: facts.Claim{Attempt: 2, Runner: "r1", Order: none}, want: base},
		{name: "an order the claim lacks is taken from the batch", held: facts.Claim{Attempt: 2, Runner: "r1"}, next: base, want: base},
		{name: "a counter that restarted does not compare, and does not refuse", held: base, next: facts.Claim{Attempt: 2, Runner: "r1", Order: other(1)}, want: base},
		{name: "an earlier attempt is refused once a later one holds the device", held: base, next: facts.Claim{Attempt: 1, Runner: "r0", Order: lease(5)}, refused: "from attempt 1"},
		{name: "a reclaim by another Runner takes the claim", held: base, next: facts.Claim{Attempt: 3, Runner: "r2", Order: lease(12)}, want: facts.Claim{Attempt: 3, Runner: "r2", Order: lease(12)}},
		{name: "a reclaim whose order is lower than the run it replaces is refused", held: base, next: facts.Claim{Attempt: 3, Runner: "r2", Order: lease(4)}, refused: "lower"},
		{name: "a reclaim after a counter restart is taken, since the orders do not compare", held: base, next: facts.Claim{Attempt: 3, Runner: "r2", Order: other(1)}, want: facts.Claim{Attempt: 3, Runner: "r2", Order: other(1)}},
		{name: "a reclaim with no order is taken", held: base, next: facts.Claim{Attempt: 3, Runner: "r2"}, want: facts.Claim{Attempt: 3, Runner: "r2"}},
		{name: "attempt zero is an ordinary attempt", held: facts.Claim{Attempt: 0, Runner: "r1", Order: lease(3)}, next: facts.Claim{Attempt: 0, Runner: "r1", Order: lease(3)}, want: facts.Claim{Attempt: 0, Runner: "r1", Order: lease(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := facts.DecideClaim(tc.held, tc.next)
			if tc.refused != "" {
				if !strings.Contains(reason, tc.refused) {
					t.Fatalf("reason %q, want it to mention %q", reason, tc.refused)
				}
				if got != tc.held {
					t.Fatalf("a refused batch changed the claim: %+v, want the held %+v", got, tc.held)
				}
				return
			}
			if reason != "" {
				t.Fatalf("refused with %q, want accepted", reason)
			}
			if got != tc.want {
				t.Fatalf("claim %+v, want %+v", got, tc.want)
			}
		})
	}
}
