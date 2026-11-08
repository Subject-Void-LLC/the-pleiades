// Tests of the file store's readers against a Collection identity no build
// writes (Phase 136b), each file edited the way someone tampering would.
package facts_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// identityStore records one stable and one volatile value with the facts
// group's identity on device dev1, and returns the store and its directory.
func identityStore(t *testing.T) (*facts.FileStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := facts.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	declared := func(key string) (facts.Spec, bool) {
		if key == "uptime" {
			return facts.Spec{Kind: facts.KindVolatile}, true
		}
		return facts.Spec{Kind: facts.KindStable}, true
	}
	entries, _, err := facts.Prepare(map[string]any{"os.kernel.release": "6.18.33", "uptime": 5}, facts.Guard{Declared: declared})
	if err != nil {
		t.Fatal(err)
	}
	by := facts.Provenance{Run: "r", Node: "n1", Method: "facts.gather", Order: facts.Order{Source: facts.ClockSource, Token: 1}, Collection: factsIdentity}
	if err := store.Record(context.Background(), "dev1", facts.Observation{Entries: entries, At: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC), By: by}); err != nil {
		t.Fatal(err)
	}
	return store, dir
}

// editGroup rewrites the facts group's name to another group's in one of
// dev1's files.
func editGroup(t *testing.T, dir, file string) {
	t.Helper()
	path := filepath.Join(dir, file)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.ReplaceAll(raw, []byte(`"group":"facts"`), []byte(`"group":"file"`))
	if bytes.Equal(edited, raw) {
		t.Fatalf("%s names no facts group to edit: %s", file, raw)
	}
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFileStore_KeepsTheIdentityItRecorded: each value reads back with the
// identity it was recorded under, the observation too.
func TestFileStore_KeepsTheIdentityItRecorded(t *testing.T) {
	store, _ := identityStore(t)
	current, err := store.Current(context.Background(), "dev1")
	if err != nil || len(current) != 2 {
		t.Fatalf("Current = %+v, %v", current, err)
	}
	for _, e := range current {
		if e.By.Collection != factsIdentity || e.LastBy.Collection != factsIdentity {
			t.Fatalf("%s reads back with %+v and %+v, want %+v", e.Key, e.By.Collection, e.LastBy.Collection, factsIdentity)
		}
	}
}

// TestFileStore_RefusesAHistoryLineNamingAnotherGroup: a history line is a
// chained record, so one naming an identity no build writes for its method
// is refused as a line no store wrote, not read as unknown.
func TestFileStore_RefusesAHistoryLineNamingAnotherGroup(t *testing.T) {
	store, dir := identityStore(t)
	editGroup(t, dir, "dev1.jsonl")
	if _, err := store.History(context.Background(), "dev1", "os.kernel.release"); err == nil {
		t.Fatal("a history line naming another group's identity was read")
	}
}

// TestFileStore_AVolatileValueNamingAnotherGroupReadsAsUnknown: a volatile
// value is not chained and the next run replaces it, so it keeps its value
// and reads as of unknown Collection.
func TestFileStore_AVolatileValueNamingAnotherGroupReadsAsUnknown(t *testing.T) {
	store, dir := identityStore(t)
	editGroup(t, dir, "dev1.state.json")
	history, err := store.History(context.Background(), "dev1", "uptime")
	if err != nil || len(history) != 1 {
		t.Fatalf("History(uptime) = %+v, %v; want the one volatile value", history, err)
	}
	if !history[0].By.Collection.IsZero() {
		t.Fatalf("the edited volatile value reads with %+v, want no identity", history[0].By.Collection)
	}
}

// TestFileStore_SkipsAnObservationNamingAnotherGroup: an observation line is
// left out and counted like any other damaged one, which costs a
// confirmation and never a value.
func TestFileStore_SkipsAnObservationNamingAnotherGroup(t *testing.T) {
	store, dir := identityStore(t)
	entries, _, err := facts.Prepare(map[string]any{"os.kernel.release": "6.18.33"}, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	confirm := facts.Provenance{Run: "r2", Node: "n1", Method: "facts.gather", Order: facts.Order{Source: facts.ClockSource, Token: 2}, Collection: factsIdentity}
	if err := store.Record(context.Background(), "dev1", facts.Observation{Entries: entries, At: time.Date(2026, 11, 8, 11, 0, 0, 0, time.UTC), By: confirm}); err != nil {
		t.Fatal(err)
	}
	kernel := func() facts.Entry {
		t.Helper()
		current, err := store.Current(context.Background(), "dev1")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range current {
			if e.Key == "os.kernel.release" {
				return e
			}
		}
		t.Fatalf("no os.kernel.release in %+v", current)
		return facts.Entry{}
	}
	if e := kernel(); e.LastBy.Run != "r2" {
		t.Fatalf("before the edit the value was last confirmed by %q, want r2", e.LastBy.Run)
	}
	editGroup(t, dir, "dev1.seen.jsonl")
	e := kernel()
	if e.Value != "6.18.33" || e.LastBy.Run != "r" {
		t.Fatalf("after the edit: %+v; want the value kept, confirmed last by its first run", e)
	}
}

// TestFileStore_RefusesAnObservationNamingAnotherGroup: the writer refuses
// what its reader would, before writing anything.
func TestFileStore_RefusesAnObservationNamingAnotherGroup(t *testing.T) {
	store, dir := identityStore(t)
	before, err := os.ReadFile(filepath.Join(dir, "dev1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := facts.Prepare(map[string]any{"os.kernel.release": "6.18.34"}, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	wrong := facts.Provenance{Run: "r2", Method: "file.copy", Order: facts.Order{Source: facts.ClockSource, Token: 2}, Collection: factsIdentity}
	err = store.Record(context.Background(), "dev1", facts.Observation{Entries: entries, At: time.Date(2026, 11, 8, 11, 0, 0, 0, time.UTC), By: wrong})
	if !errors.Is(err, facts.ErrInvalid) {
		t.Fatalf("Record = %v, want ErrInvalid", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "dev1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused observation wrote to the history")
	}
}
