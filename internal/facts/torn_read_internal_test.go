// A read of the file store that a Record lands in the middle of (Phase 41h).
package facts

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestLoad_ARecordBetweenReadsCreditsNoReplacedValue: an unlocked reader has
// read the observations when a run replaces a stable value and a volatile
// one, and then reads the values. The replaced values must not read as
// confirmed by the run that replaced them: load reads the observations
// before the values, so everything read after is newer than every
// observation folded.
func TestLoad_ARecordBetweenReadsCreditsNoReplacedValue(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "facts"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	gather := func(run string, token uint64, kernel string, uptime int) {
		t.Helper()
		stable, _, err := Prepare(map[string]any{"os.kernel": kernel}, Guard{})
		if err != nil {
			t.Fatal(err)
		}
		up, _, err := Prepare(map[string]any{"host.uptime_seconds": uptime}, Guard{})
		if err != nil {
			t.Fatal(err)
		}
		up[0].Kind = KindVolatile
		obs := Observation{Entries: append(stable, up...), At: t0.Add(time.Duration(token) * time.Minute), By: Provenance{Run: run, Order: Order{Source: ClockSource, Token: token}}}
		if err := store.Record(ctx, "dev1", obs); err != nil {
			t.Fatal(err)
		}
	}
	gather("run1", 1, "6.1", 100)
	r, err := store.root("dev1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	sf, err := readSeen(r, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	staleLines, _, err := readLines(r, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	gather("run2", 2, "6.2", 200)
	// The order the first version read in, values then observations, to show
	// the test tells the two apart: run2's observation lands on 6.1.
	fresh, err := readSeen(r, "dev1")
	if err != nil {
		t.Fatal(err)
	}
	stale := make([]Stored, len(staleLines))
	for i, l := range staleLines {
		stale[i] = Stored{Entry: Entry{Key: l.Key, Value: l.Value, Hash: l.Hash, ObservedAt: l.ObservedAt, Kind: KindStable, By: l.By}, Seq: l.Seq}
	}
	if got := Sightings(stale, fresh.seen); len(got) != 1 || got[0].LastBy.Run != "run2" {
		t.Fatalf("values read before observations: %+v; the test no longer reproduces the torn read", got)
	}
	d, err := loadAfter(r, "dev1", sf)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range append(d.written(), d.volatile["host.uptime_seconds"]) {
		if e.LastBy.Run != e.By.Run {
			t.Errorf("%s = %v reads as confirmed by %s, which did not observe it", e.Key, e.Value, e.LastBy.Run)
		}
	}
}
