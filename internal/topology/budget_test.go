package topology_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// TestOneBudgetMovesEveryDerivedConstant is the assertion the whole phase
// exists to make possible.
//
// Before Phase 96c the retention-shaped constants were independent
// literals, and the measured consequence was that two of them sat three
// seconds apart by coincidence. This fails if any of them stops moving
// with the budget, which is the only thing that keeps "derived" true
// rather than merely claimed.
func TestOneBudgetMovesEveryDerivedConstant(t *testing.T) {
	small := topology.OutageBudget(2 * time.Minute)
	large := topology.OutageBudget(4 * time.Hour)

	if topology.DerivedMaxAge(large) <= topology.DerivedMaxAge(small) {
		t.Error("MaxAge did not move with the budget")
	}
	if topology.DerivedDedupTTLFloor(large) <= topology.DerivedDedupTTLFloor(small) {
		t.Error("the dedup TTL floor did not move with the budget")
	}
	// The duplicate window moves too, up to its cap. Below the cap it
	// must track the budget exactly.
	if got := topology.DerivedDuplicateWindow(small); got != small.Duration() {
		t.Errorf("DerivedDuplicateWindow(%s) = %v, want it to track the budget below the cap", small, got)
	}
}

// TestDuplicateWindowStaysBelowTheReclaimInterval is the invariant that
// keeps the Reaper working, and it is the one derivation that is
// deliberately not linear.
//
// internal/dispatch.Reaper republishes a job stranded for ten minutes,
// and its own doc comment says that is safe "precisely because it is not
// a retry within that window". If the duplicate window ever grew past the
// reclaim interval, the Reaper's republish would be suppressed as a
// duplicate and a stranded job would stop being recovered at all.
func TestDuplicateWindowStaysBelowTheReclaimInterval(t *testing.T) {
	const reclaimInterval = 10 * time.Minute

	for _, b := range []topology.OutageBudget{
		topology.MinOutageBudget,
		topology.DefaultOutageBudget,
		topology.MaxOutageBudget,
	} {
		if got := topology.DerivedDuplicateWindow(b); got >= reclaimInterval {
			t.Errorf("DerivedDuplicateWindow(%s) = %v, which is at or past internal/dispatch's %v stale reclaim interval; the Reaper's republish would be suppressed as a duplicate", b, got, reclaimInterval)
		}
	}
}

// TestDerivedRetentionAlwaysExceedsTheBudget is the inequality that makes
// the budget mean anything: a stream that forgets a message before the
// promised outage ends has not survived the outage.
func TestDerivedRetentionAlwaysExceedsTheBudget(t *testing.T) {
	for _, b := range []topology.OutageBudget{
		topology.MinOutageBudget,
		topology.DefaultOutageBudget,
		topology.MaxOutageBudget,
	} {
		if topology.DerivedMaxAge(b) <= b.Duration() {
			t.Errorf("DerivedMaxAge(%s) = %v, which does not exceed the budget it is meant to cover", b, topology.DerivedMaxAge(b))
		}
		if topology.DerivedDedupTTLFloor(b) <= b.Duration() {
			t.Errorf("DerivedDedupTTLFloor(%s) = %v, which does not exceed the budget", b, topology.DerivedDedupTTLFloor(b))
		}
	}
}

// TestDefaultBudgetReproducesTheShippedRetention proves the first release
// is a pure refactor for MaxAge rather than a live retention change on
// every existing install, which is what stops the very first upgrade
// tripping the destructive-lowering guard.
func TestDefaultBudgetReproducesTheShippedRetention(t *testing.T) {
	const shipped = 7 * 24 * time.Hour
	if got := topology.DerivedMaxAge(topology.DefaultOutageBudget); got != shipped {
		t.Errorf("DerivedMaxAge(default) = %v, want the previously shipped %v", got, shipped)
	}
}

// TestParseOutageBudget covers the operator-facing boundary.
func TestParseOutageBudget(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    topology.OutageBudget
		wantErr string
	}{
		{"empty means the default", "", topology.DefaultOutageBudget, ""},
		{"a plain duration", "20m", topology.OutageBudget(20 * time.Minute), ""},
		{"hours", "2h", topology.OutageBudget(2 * time.Hour), ""},
		{"the minimum", "1m", topology.MinOutageBudget, ""},
		{"the maximum", "12h", topology.MaxOutageBudget, ""},
		{"not a duration", "twenty minutes", 0, "write a Go duration"},
		{"seconds are not a duration string", "1200", 0, "write a Go duration"},
		{"below the minimum", "10s", 0, "outside the accepted range"},
		{"above the maximum", "48h", 0, "outside the accepted range"},
		{"negative", "-5m", 0, "outside the accepted range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := topology.ParseOutageBudget(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseOutageBudget(%q) = %v, want an error", tt.raw, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOutageBudget(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseOutageBudget(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

// FuzzOutageBudgetDerivation asserts the invariants hold for every budget
// a parser could produce, including the ones nobody would type.
//
// The invariant that matters most is the one the server enforces and this
// code must never violate: a stream's Duplicates window may not exceed
// its MaxAge. With the multipliers chosen it holds by construction for
// every positive budget, which is exactly what makes it worth asserting,
// because a later "simplification" of the multipliers is what would break
// it, and it would break at startup against a real server.
func FuzzOutageBudgetDerivation(f *testing.F) {
	for _, seed := range []int64{
		int64(topology.MinOutageBudget), int64(topology.DefaultOutageBudget),
		int64(topology.MaxOutageBudget), 0, -1, 1, int64(time.Hour),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw int64) {
		b := topology.OutageBudget(raw)
		if !b.Valid() {
			// Out of range budgets never reach the derivations, because
			// ParseOutageBudget is the only way to build one.
			return
		}

		maxAge := topology.DerivedMaxAge(b)
		dup := topology.DerivedDuplicateWindow(b)
		floor := topology.DerivedDedupTTLFloor(b)

		if dup <= 0 {
			t.Fatalf("budget %s derived a non-positive duplicate window %v", b, dup)
		}
		if maxAge <= 0 {
			t.Fatalf("budget %s derived a non-positive MaxAge %v", b, maxAge)
		}
		if dup > maxAge {
			t.Fatalf("budget %s derived Duplicates %v exceeding MaxAge %v, which a real server rejects", b, dup, maxAge)
		}
		if maxAge <= b.Duration() {
			t.Fatalf("budget %s derived MaxAge %v that does not cover it", b, maxAge)
		}
		if floor <= b.Duration() {
			t.Fatalf("budget %s derived a dedup floor %v that does not cover it", b, floor)
		}
		if dup >= 10*time.Minute {
			t.Fatalf("budget %s derived a duplicate window %v at or past the stale reclaim interval", b, dup)
		}
	})
}
