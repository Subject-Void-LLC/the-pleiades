// Package factstest: random sequences of observations, replayed into a store
// and into the oracle, whose reads must agree (Phase 41h).
package factstest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// differentialSequences is how many sequences one run replays, each into a
// store of its own, and differentialObservations how many observations each
// holds.
const (
	differentialSequences    = 120
	differentialObservations = 14
)

// randomObservation is one observation of a few of the sequence's keys, each
// stable or volatile and one of three values, by a run whose order may
// compare with the others', match one of them (one dispatch) or not compare at
// all, at a time that may disagree with that order.
func randomObservation(t *testing.T, rng *rand.Rand, prefix string) facts.Observation {
	t.Helper()
	keys := []string{"a", "b", "c"}
	var entries []facts.Entry
	for _, k := range keys {
		if rng.IntN(3) == 0 {
			continue
		}
		kind := facts.KindStable
		if rng.IntN(4) == 0 {
			kind = facts.KindVolatile
		}
		entries = append(entries, entries1(t, kind, prefix+k, []string{"x", "y", "z"}[rng.IntN(3)])...)
	}
	if len(entries) == 0 {
		entries = entries1(t, facts.KindStable, prefix+"a", "x")
	}
	sources := []string{facts.ClockSource, facts.ClockSource, facts.ClockSource, "lease:e1", ""}
	order := facts.Order{Source: sources[rng.IntN(len(sources))], Token: 1 + rng.Uint64N(8)}
	if order.Source == "" {
		order.Token = 0
	}
	run := fmt.Sprintf("r%d", rng.IntN(1000))
	return facts.Observation{
		Entries: entries,
		At:      base.Add(time.Duration(rng.IntN(40)) * time.Minute),
		By:      facts.Provenance{Run: run, Node: "n1", Method: "facts.gather", Order: order},
	}
}

// entries1 prepares one key of kind with value.
func entries1(t *testing.T, kind facts.Kind, key, value string) []facts.Entry {
	t.Helper()
	return entries(t, kind, map[string]any{key: value})
}

// comparableEntry is what the comparison reads of an entry: everything but the
// value, which its hash stands for.
type comparableEntry struct {
	Key, Hash            string
	Kind                 facts.Kind
	ObservedAt, LastSeen time.Time
	By, LastBy           facts.Provenance
	OrderedByClock       bool
}

// view is entries as compared.
func view(entries []facts.Entry, prefix string) []comparableEntry {
	var out []comparableEntry
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, prefix) {
			continue
		}
		out = append(out, comparableEntry{e.Key, e.Hash, e.Kind, e.ObservedAt.UTC(), e.LastSeenAt.UTC(), e.By, e.LastBy, e.OrderedByClock})
	}
	return out
}

// derivedReadsMatchTheOracle: every sequence's Current and History, after
// each observation, are what the mutable store gave for the same sequence.
func derivedReadsMatchTheOracle(t *testing.T, newStore NewStore) {
	// A fixed seed, on purpose: a failing sequence must replay the same way, and
	// nothing here is secret.
	rng := rand.New(rand.NewPCG(41, 8)) // #nosec G404 -- a seeded, reproducible test sequence, not a secret
	for seq := 0; seq < differentialSequences; seq++ {
		// A subtest each, so each sequence's store is closed when it ends
		// rather than all of them at the end of the run.
		t.Run(fmt.Sprintf("sequence %d", seq), func(t *testing.T) {
			replaySequence(t, newStore, rng, seq)
		})
	}
}

// replaySequence replays one random sequence into a fresh store and the
// oracle, comparing every read after each observation.
func replaySequence(t *testing.T, newStore NewStore, rng *rand.Rand, seq int) {
	ctx := context.Background()
	s, dev := newStore(t)
	prefix := fmt.Sprintf("s%d.", seq)
	var o oracle
	var replay []string
	for n := 0; n < differentialObservations; n++ {
		obs := randomObservation(t, rng, prefix)
		replay = append(replay, describe(obs))
		o.record(obs)
		if err := s.Record(ctx, dev, obs); err != nil {
			t.Fatalf("Record: %v", err)
		}
		got, err := s.Current(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		if g, w := view(got, prefix), view(o.current(), prefix); !reflect.DeepEqual(g, w) {
			t.Fatalf("after %d observations:\n%s\nCurrent = %+v\nwant      %+v", n+1, strings.Join(replay, "\n"), g, w)
		}
		for _, k := range []string{"a", "b", "c"} {
			h, err := s.History(ctx, dev, prefix+k)
			if err != nil {
				t.Fatal(err)
			}
			if g, w := view(h, prefix), view(o.history(prefix+k), prefix); !reflect.DeepEqual(g, w) {
				t.Fatalf("after %d observations:\n%s\nHistory(%s) = %+v\nwant           %+v", n+1, strings.Join(replay, "\n"), k, g, w)
			}
		}
	}
}

// describe is one observation in a line, for a failure's replay.
func describe(obs facts.Observation) string {
	var parts []string
	for _, e := range obs.Entries {
		parts = append(parts, fmt.Sprintf("%s=%v(%s)", e.Key, e.Value, e.Kind))
	}
	return fmt.Sprintf("  %s %s:%d at +%s: %s", obs.By.Run, obs.By.Order.Source, obs.By.Order.Token, obs.At.Sub(base), strings.Join(parts, " "))
}
