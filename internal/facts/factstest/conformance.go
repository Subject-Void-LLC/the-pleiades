// Package factstest is the conformance suite every facts.Store runs through
// identically (Phase 41b): the Crawl CLI's file store and the Controller's
// ent store keep one set of rules, so each is held to the same tests rather
// than to two copies that could drift, as the sync plugins share theirs.
package factstest

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// NewStore returns a fresh store and the ID of a device it may record for.
type NewStore func(t *testing.T) (facts.Store, string)

// base is the time every case's observations are measured from.
var base = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

// Run runs every case against stores newStore makes.
func Run(t *testing.T, newStore NewStore) {
	t.Run("a volatile key keeps one value, the latest", func(t *testing.T) { volatileKeepsOne(t, newStore) })
	t.Run("the run order decides, not the clock", func(t *testing.T) { runOrderDecides(t, newStore) })
	t.Run("orders that do not compare fall back to the clock and say so", func(t *testing.T) { clockFallback(t, newStore) })
	t.Run("provenance says who first saw a value and who last confirmed it", func(t *testing.T) { provenanceKept(t, newStore) })
	t.Run("stable values form a chain that verifies", func(t *testing.T) { chainVerifies(t, newStore) })
	t.Run("a key whose kind changed reads its newest value", func(t *testing.T) { kindChange(t, newStore) })
}

// entries prepares observed as a method declaring every key kind would.
func entries(t *testing.T, kind facts.Kind, observed map[string]any) []facts.Entry {
	t.Helper()
	declared := func(string) (facts.Spec, bool) { return facts.Spec{Kind: kind}, true }
	out, problems, err := facts.Prepare(observed, facts.Guard{Declared: declared})
	if err != nil || len(problems) != 0 {
		t.Fatalf("Prepare: %v, %v", problems, err)
	}
	return out
}

// by is a provenance with run and a clock order of token.
func by(run string, token uint64) facts.Provenance {
	return facts.Provenance{Run: run, Node: "n1", Method: "facts.gather", Order: facts.Order{Source: facts.ClockSource, Token: token}}
}

// record stores one observation, failing the test on an error.
func record(t *testing.T, s facts.Store, device string, kind facts.Kind, observed map[string]any, at time.Time, p facts.Provenance) {
	t.Helper()
	if err := s.Record(context.Background(), device, facts.Observation{Entries: entries(t, kind, observed), At: at, By: p}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// current is device's current value of key, failing when there is none.
func current(t *testing.T, s facts.Store, device, key string) facts.Entry {
	t.Helper()
	all, err := s.Current(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no current %s in %+v", key, all)
	return facts.Entry{}
}

// history is key's history on device.
func history(t *testing.T, s facts.Store, device, key string) []facts.Entry {
	t.Helper()
	h, err := s.History(context.Background(), device, key)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func volatileKeepsOne(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	for i, up := range []int{100, 200, 300} {
		record(t, s, dev, facts.KindVolatile, map[string]any{"uptime": up}, base.Add(time.Duration(i)*time.Minute), by("r", uint64(i+1)))
	}
	// A late observation (an older run, a later clock) leaves the newer one.
	record(t, s, dev, facts.KindVolatile, map[string]any{"uptime": 150}, base.Add(time.Hour), by("late", 2))
	h := history(t, s, dev, "uptime")
	if len(h) != 1 || h[0].Kind != facts.KindVolatile || h[0].Value != jsonInt(300) {
		t.Fatalf("history = %+v, want the one value 300", h)
	}
	// The same value confirmed again moves its last sighting.
	record(t, s, dev, facts.KindVolatile, map[string]any{"uptime": 300}, base.Add(2*time.Hour), by("again", 9))
	if e := current(t, s, dev, "uptime"); e.LastBy.Run != "again" || !e.LastSeenAt.Equal(base.Add(2*time.Hour)) || e.By.Run != "r" {
		t.Fatalf("current = %+v, want first seen by r, last by again", e)
	}
}

func runOrderDecides(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	// Run 2 finished at 10:00; run 1 finished at 11:00 on a host whose clock
	// was ahead, and its batch arrived last. Run 2 came later by the order
	// the system keeps, so its value is current (FAILURE_PATTERNS 831).
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.2"}, base, by("run2", 2))
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.1"}, base.Add(time.Hour), by("run1", 1))
	if e := current(t, s, dev, "ansible_kernel"); e.Value != "6.2" {
		t.Fatalf("current = %v, want 6.2 from the later run", e.Value)
	}
	h := history(t, s, dev, "ansible_kernel")
	if len(h) != 2 || h[0].Value != "6.1" || h[1].Value != "6.2" || h[1].OrderedByClock {
		t.Fatalf("history = %+v, want 6.1 then 6.2 in run order", h)
	}
}

func clockFallback(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	lease := facts.Provenance{Run: "walk", Order: facts.Order{Source: "lease:e1", Token: 50}}
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.1"}, base, by("crawl", 7))
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.2"}, base.Add(time.Minute), lease)
	h := history(t, s, dev, "ansible_kernel")
	if len(h) != 2 || h[0].Value != "6.1" || !h[1].OrderedByClock {
		t.Fatalf("history = %+v, want the second placed by the clock and marked", h)
	}
	if e := current(t, s, dev, "ansible_kernel"); e.Value != "6.2" {
		t.Fatalf("current = %v, want the clock's later 6.2", e.Value)
	}
}

func provenanceKept(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.1"}, base, by("first", 1))
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.1"}, base.Add(time.Minute), by("second", 2))
	e := current(t, s, dev, "ansible_kernel")
	if e.By.Run != "first" || e.LastBy.Run != "second" || e.By.Order.Token != 1 || e.LastBy.Order.Token != 2 || e.By.Method != "facts.gather" {
		t.Fatalf("current = %+v, want first by run first and last by run second", e)
	}
	if h := history(t, s, dev, "ansible_kernel"); len(h) != 1 {
		t.Fatalf("a confirmation wrote a value: %+v", h)
	}
}

func chainVerifies(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	record(t, s, dev, facts.KindStable, map[string]any{"a": "1", "b": "1"}, base, by("r1", 1))
	record(t, s, dev, facts.KindStable, map[string]any{"a": "2"}, base.Add(time.Minute), by("r2", 2))
	// Volatile values are not chained.
	record(t, s, dev, facts.KindVolatile, map[string]any{"up": 5}, base.Add(time.Minute), by("r2", 2))
	v, err := s.Verify(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	if v.Break != nil || v.Values != 3 || v.Unchained != 0 || len(v.Head) != 64 {
		t.Fatalf("verdict = %+v, want three chained values and no break", v)
	}
	empty, _ := newStore(t)
	if v, err := empty.Verify(context.Background(), dev); err != nil || v.Values != 0 || v.Break != nil {
		t.Fatalf("an empty store: %+v, %v", v, err)
	}
}

func kindChange(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	record(t, s, dev, facts.KindStable, map[string]any{"load": 1}, base, by("r1", 1))
	record(t, s, dev, facts.KindVolatile, map[string]any{"load": 2}, base.Add(time.Minute), by("r2", 2))
	if e := current(t, s, dev, "load"); e.Value != jsonInt(2) || e.Kind != facts.KindVolatile {
		t.Fatalf("current = %+v, want the volatile 2", e)
	}
	if h := history(t, s, dev, "load"); len(h) != 2 || h[0].Kind != facts.KindStable {
		t.Fatalf("history = %+v, want the stable value kept and the volatile one after it", h)
	}
}

// jsonInt is n as a store reads a number back: as written, never a float.
func jsonInt(n int) any { return json.Number(strconv.Itoa(n)) }
