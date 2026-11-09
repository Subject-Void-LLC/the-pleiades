// Package facts_test: a new fact may not be named "." or "..", which no URL
// carries as the {key} of its history's address, while a key an earlier
// release stored under either still reads back.
package facts_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestValidKey_RefusesTheNamesNoURLCarries tables the rule with its near
// misses, which stay usable fact names.
func TestValidKey_RefusesTheNamesNoURLCarries(t *testing.T) {
	for key, want := range map[string]bool{
		"":          false,
		".":         false,
		"..":        false,
		"...":       true,
		".hidden":   true,
		"os.family": true,
		"a/..":      false, // a slash, refused as before
	} {
		if got := facts.ValidKey(key); got != want {
			t.Errorf("ValidKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestPrepare_DoesNotStoreADotKey: an observation holding "." and ".." beside
// a real fact stores the real one and reports the other two, with the
// reason, rather than refusing the observation.
func TestPrepare_DoesNotStoreADotKey(t *testing.T) {
	entries, problems, err := facts.Prepare(map[string]any{".": "x", "..": "y", "kernel": "6.1"}, facts.Guard{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "kernel" {
		t.Errorf("stored %+v, want only kernel", entries)
	}
	refused := map[string]bool{}
	for _, p := range problems {
		if strings.Contains(p.Reason, `"." or ".."`) {
			refused[p.Key] = true
		}
	}
	if !refused["."] || !refused[".."] {
		t.Errorf("problems %+v, want \".\" and \"..\" reported with the reason", problems)
	}
}

// TestFileStore_StillReadsADotKeyAnEarlierReleaseStored writes a history line
// for the key "." the way a writer before this rule could (Record checks no
// key; Prepare did not refuse it then) and reads it back. A reader held to
// ValidKey would call the whole history corrupt.
func TestFileStore_StillReadsADotKeyAnEarlierReleaseStored(t *testing.T) {
	store, err := facts.NewFileStore(filepath.Join(t.TempDir(), "facts"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := facts.Hash(".", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Record(ctx, "d1", facts.Observation{Entries: []facts.Entry{{Key: ".", Value: "legacy", Hash: hash}}, At: time.Now()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	current, err := store.Current(ctx, "d1")
	if err != nil {
		t.Fatalf("Current over a history holding the key \".\": %v", err)
	}
	if len(current) != 1 || current[0].Key != "." {
		t.Errorf("Current = %+v, want the stored \".\" fact", current)
	}
}
