// The Crawl CLI's fact files are written by appending (Phase 41h): an
// unchanged gather rewrites nothing, and a change appends to the history.
package facts_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// snapshot is every file in dir but the lock, by name.
func snapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = raw
	}
	return out
}

// TestFileStore_AGatherOnlyAppends: a second gather of the same values leaves
// every file as it was but for lines added at its end, the state file
// included (before 41h it was rewritten with each value's new last sighting);
// a gather that changes a stable value appends it, and only a volatile value
// replaced rewrites the state.
func TestFileStore_AGatherOnlyAppends(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	gather := func(n int, kernel string, uptime int) {
		t.Helper()
		entries := prepared(t, map[string]any{"os.kernel": kernel, "os.family": "debian"})
		up := prepared(t, map[string]any{"host.uptime_seconds": uptime})
		up[0].Kind = facts.KindVolatile
		by := facts.Provenance{Run: fmt.Sprintf("run%d", n), Order: facts.Order{Source: facts.ClockSource, Token: uint64(n)}}
		if err := store.Record(ctx, "dev1", facts.Observation{Entries: append(entries, up...), At: t0.Add(time.Duration(n) * time.Hour), By: by}); err != nil {
			t.Fatalf("gather %d: %v", n, err)
		}
	}
	// appendedOnly fails unless every file in before is a prefix of itself
	// now, and those in same are unchanged.
	appendedOnly := func(before map[string][]byte, same ...string) {
		t.Helper()
		after := snapshot(t, dir)
		for name, old := range before {
			if !bytes.HasPrefix(after[name], old) {
				t.Errorf("%s was rewritten, not appended to", name)
			}
		}
		for _, name := range same {
			if !bytes.Equal(before[name], after[name]) {
				t.Errorf("%s changed", name)
			}
		}
	}
	gather(1, "6.1", 100)
	first := snapshot(t, dir)
	gather(2, "6.1", 100)
	appendedOnly(first, "dev1.jsonl", "dev1.state.json")
	second := snapshot(t, dir)
	gather(3, "6.2", 200)
	delete(second, "dev1.state.json") // a volatile value replaced rewrites it
	appendedOnly(second)
	current, err := store.Current(ctx, "dev1")
	if err != nil || len(current) != 3 {
		t.Fatalf("Current = %+v, %v", current, err)
	}
	for _, e := range current {
		if e.LastBy.Run != "run3" {
			t.Errorf("%s last confirmed by %q, want run3", e.Key, e.LastBy.Run)
		}
	}
	kernel, err := store.History(ctx, "dev1", "os.kernel")
	if err != nil || len(kernel) != 2 || kernel[0].LastBy.Run != "run2" {
		t.Fatalf("History(os.kernel) = %+v, %v; want 6.1 last confirmed by run2, then 6.2", kernel, err)
	}
}
