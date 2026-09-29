// Package journal_test: the seal that marks a run's journal complete.
package journal_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// sealRunID is a run id shaped like the uuid the engine mints.
const sealRunID = "0f8fad5b-d9cb-469f-a165-70867728950e"

func TestSealWritesAnOwnerOnlyFileBesideTheJournal(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.FixedZone("x", 3600))
	if err := store.Seal(sealRunID, at); err != nil {
		t.Fatalf("Seal: %v", err)
	}

	path := filepath.Join(journalDir(root), sealRunID+".end")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat seal: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("seal has mode %04o, want 0600", got)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- a path under t.TempDir()
	if err != nil {
		t.Fatalf("read seal: %v", err)
	}
	var got journal.Seal
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("seal is not JSON: %v (%q)", err, raw)
	}
	if got.RunID != sealRunID || !got.SealedAt.Equal(at) || got.SealedAt.Location() != time.UTC {
		t.Errorf("seal = %+v, want run %s sealed at %v in UTC", got, sealRunID, at)
	}
}

// TestSealRefusesASecondSealForOneRun proves a run is sealed once: a
// second seal would mean two runs shared an id, and overwriting the first
// would hide that.
func TestSealRefusesASecondSealForOneRun(t *testing.T) {
	store, err := journal.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Seal(sealRunID, time.Now()); err != nil {
		t.Fatalf("first Seal: %v", err)
	}
	err = store.Seal(sealRunID, time.Now())
	if err == nil || !strings.Contains(err.Error(), "already sealed") {
		t.Fatalf("second Seal = %v, want it refused as already sealed", err)
	}
}

// TestSealAndPathForRefuseARunIDThatCouldNameAnotherFile covers the one
// validation both share with the journal's own file name.
func TestSealAndPathForRefuseARunIDThatCouldNameAnotherFile(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for _, bad := range []string{"", "../escape", "a/b", "run.id", strings.Repeat("a", 65)} {
		if err := store.Seal(bad, time.Now()); err == nil {
			t.Errorf("Seal(%q) = nil, want it refused", bad)
		}
		if path, err := store.PathFor(bad); err == nil {
			t.Errorf("PathFor(%q) = %q, want it refused", bad, path)
		}
	}
	entries, err := os.ReadDir(journalDir(root))
	if err != nil {
		t.Fatalf("read journal dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("refused ids left %d files behind", len(entries))
	}
}

func TestPathForNamesTheRunsJournalFile(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.PathFor(sealRunID)
	if err != nil {
		t.Fatalf("PathFor: %v", err)
	}
	if want := filepath.Join(journalDir(root), sealRunID+".jsonl"); got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
}
