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
	t.Run("within one run, the clock orders its observations", func(t *testing.T) { sameRunByClock(t, newStore) })
	t.Run("a time is kept to the microsecond and still verifies", func(t *testing.T) { microsecondTime(t, newStore) })
	t.Run("a late change keeps a later confirmation in the history", func(t *testing.T) { lateChangeKeepsConfirmation(t, newStore) })
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
	if v.Break != nil || v.Values != 3 || len(v.Head) != 64 {
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
	} // And back: a volatile value, then a stable one; the history lists the
	// stable values in run order and the volatile one last, on both stores.
	record(t, s, dev, facts.KindStable, map[string]any{"load": 3}, base.Add(2*time.Minute), by("r3", 3))
	h := history(t, s, dev, "load")
	if len(h) != 3 || h[0].Value != jsonInt(1) || h[1].Value != jsonInt(3) || h[2].Kind != facts.KindVolatile {
		t.Fatalf("history = %+v, want 1 and 3 stable in order, then the volatile 2", h)
	}
	if e := current(t, s, dev, "load"); e.Value != jsonInt(3) || e.Kind != facts.KindStable {
		t.Fatalf("current = %+v, want the stable 3, observed last", e)
	}
}

// jsonInt is n as a store reads a number back: as written, never a float.
func jsonInt(n int) any { return json.Number(strconv.Itoa(n)) }

// sameRunByClock: two observations of one run (one dispatch's batches share
// the lease's token) are ordered by when each was made, so a second gather
// after a reboot replaces the first uptime, and a batch stored late does
// not become current over the one made after it (the reviewer's F3).
func sameRunByClock(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	same := facts.Provenance{Run: "one", Order: facts.Order{Source: "lease:e1", Token: 7}}
	record(t, s, dev, facts.KindVolatile, map[string]any{"uptime": 9000}, base, same)
	record(t, s, dev, facts.KindVolatile, map[string]any{"uptime": 30}, base.Add(5*time.Minute), same)
	if e := current(t, s, dev, "uptime"); e.Value != jsonInt(30) {
		t.Fatalf("current uptime = %v, want 30 from the gather after the reboot", e.Value)
	}
	// Batch 2 (10:05, 6.2) stored before batch 1 (10:00, 6.1) of one job.
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.2"}, base.Add(5*time.Minute), same)
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.1"}, base, same)
	if e := current(t, s, dev, "ansible_kernel"); e.Value != "6.2" {
		t.Fatalf("current kernel = %v, want 6.2, observed last", e.Value)
	}
	h := history(t, s, dev, "ansible_kernel")
	if len(h) != 2 || h[0].Value != "6.1" || h[1].Value != "6.2" || !h[1].OrderedByClock {
		t.Fatalf("history = %+v, want 6.1 then 6.2, marked as placed by the clock", h)
	}
}

// microsecondTime: an observation's time keeps to the microsecond, as fine
// as every dialect keeps it, and the chain built from it verifies (the
// reviewer's F2: PostgreSQL's timestamptz drops nanoseconds).
func microsecondTime(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	at := base.Add(123456789 * time.Nanosecond)
	record(t, s, dev, facts.KindStable, map[string]any{"ansible_kernel": "6.1"}, at, by("r", 1))
	if e := current(t, s, dev, "ansible_kernel"); !e.ObservedAt.Equal(at.Truncate(time.Microsecond)) {
		t.Fatalf("observed at %s, want %s", e.ObservedAt.Format(time.RFC3339Nano), at.Truncate(time.Microsecond).Format(time.RFC3339Nano))
	}
	if v, err := s.Verify(context.Background(), dev); err != nil || v.Break != nil || v.Values != 1 {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
}

// lateChangeKeepsConfirmation: run y confirmed a value at order 11 and stored
// first; run x, ordered 10, saw another value and stored second. A
// confirmation was kept only as a last sighting, so the history ended at x's
// value, and run z at 12, finding the device unchanged since y, recorded a
// change nobody saw (FAILURE_PATTERNS 923, a residue of 831). The history now
// says the confirmed value came back at y's sighting, and z changes nothing.
func lateChangeKeepsConfirmation(t *testing.T, newStore NewStore) {
	s, dev := newStore(t)
	const key = "ansible_kernel"
	record(t, s, dev, facts.KindStable, map[string]any{key: "6.1"}, base, by("w", 5))
	record(t, s, dev, facts.KindStable, map[string]any{key: "6.1"}, base.Add(11*time.Minute), by("y", 11))
	record(t, s, dev, facts.KindStable, map[string]any{key: "6.2"}, base.Add(10*time.Minute), by("x", 10))
	values := func(h []facts.Entry) []string {
		var out []string
		for _, e := range h {
			out = append(out, e.Value.(string)+"@"+e.By.Run)
		}
		return out
	}
	want := []string{"6.1@w", "6.2@x", "6.1@y"}
	if h := values(history(t, s, dev, key)); len(h) != len(want) || h[0] != want[0] || h[1] != want[1] || h[2] != want[2] {
		t.Fatalf("history = %v, want %v: the value y confirmed after x's", h, want)
	}
	if e := current(t, s, dev, key); e.Value != "6.1" || e.LastBy.Run != "y" {
		t.Fatalf("current = %+v, want 6.1 last confirmed by y", e)
	}
	// z sees what y saw: nothing changed, so nothing is written under z.
	record(t, s, dev, facts.KindStable, map[string]any{key: "6.1"}, base.Add(12*time.Minute), by("z", 12))
	h := history(t, s, dev, key)
	if got := values(h); len(got) != 3 {
		t.Fatalf("history after z = %v, want no change recorded for z", got)
	}
	if e := current(t, s, dev, key); e.LastBy.Run != "z" {
		t.Fatalf("current = %+v, want 6.1 last confirmed by z", e)
	}
	// The line written for y's sighting is chained like any other.
	if v, err := s.Verify(context.Background(), dev); err != nil || v.Break != nil || v.Values != 3 {
		t.Fatalf("Verify = %+v, %v; want three chained values", v, err)
	}
}
