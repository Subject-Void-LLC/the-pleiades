// Tests of the file store and the Collection identity (Phase 136b): the
// writer refuses one no build writes, and the readers return what was stored,
// edited or not, leaving an edit for the chain to find.
package facts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
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

// TestFileStore_AnEditedIdentityBreaksTheChainAndReadsAsStored: the chain
// covers the identity, so a history line edited to name another group is
// found by Verify at that value, and is read back as it now stands rather
// than refused or rewritten.
func TestFileStore_AnEditedIdentityBreaksTheChainAndReadsAsStored(t *testing.T) {
	store, dir := identityStore(t)
	editGroup(t, dir, "dev1.jsonl")
	verdict, err := store.Verify(context.Background(), "dev1")
	if err != nil || verdict.Break == nil || verdict.Break.Index != 0 {
		t.Fatalf("Verify = %+v, %v; want a break at the edited value", verdict, err)
	}
	history, err := store.History(context.Background(), "dev1", "os.kernel.release")
	if err != nil || len(history) != 1 || history[0].By.Collection.Group != "file" {
		t.Fatalf("History = %+v, %v; want the edited value read as it stands", history, err)
	}
}

// TestFileStore_AStoredIdentityALaterRuleRefusesStillReads: CheckCollection
// reads the first-party group list, which a minor release may grow, so a
// program's identity stored today can fail it later. A history line whose
// chain matches its identity reads and verifies whatever the rule now says:
// here a program's identity for a first-party method, which the rule
// refuses, written with its chain recomputed as a store would have.
func TestFileStore_AStoredIdentityALaterRuleRefusesStillReads(t *testing.T) {
	store, dir := identityStore(t)
	path := filepath.Join(dir, "dev1.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &line); err != nil {
		t.Fatal(err)
	}
	var by facts.Provenance
	byRaw, _ := json.Marshal(line["by"])
	if err := json.Unmarshal(byRaw, &by); err != nil {
		t.Fatal(err)
	}
	by.Collection = wire.CollectionIdentity{Group: "facts", Program: true, Manifests: factsIdentity.Manifests}
	if by.CheckCollection() == nil {
		t.Fatal("the fixture's identity passes today's rule; it must be one the rule refuses")
	}
	observed, err := time.Parse(time.RFC3339Nano, line["observed_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	chain, err := facts.ChainHash("dev1", "", line["key"].(string), line["hash"].(string), observed, by)
	if err != nil {
		t.Fatal(err)
	}
	line["by"], line["chain"] = by, chain
	rewritten, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(rewritten, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	verdict, err := store.Verify(context.Background(), "dev1")
	if err != nil || verdict.Break != nil || verdict.Values != 1 {
		t.Fatalf("Verify = %+v, %v; want the one value verifying", verdict, err)
	}
	history, err := store.History(context.Background(), "dev1", "os.kernel.release")
	if err != nil || len(history) != 1 || history[0].By.Collection != by.Collection {
		t.Fatalf("History = %+v, %v; want the value with its stored identity", history, err)
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
