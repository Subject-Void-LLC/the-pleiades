// Tests of the file store's failure paths (file.go, file_store.go): each
// refuses or reports rather than writing half of anything.
package facts_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestRunOrdered_TakesATickForEachObservation: the CLI's recorder orders an
// observation by a tick of the project counter, and says so when it cannot
// take one.
func TestRunOrdered_TakesATickForEachObservation(t *testing.T) {
	store, err := facts.NewFileStore(filepath.Join(t.TempDir(), "facts"))
	if err != nil {
		t.Fatal(err)
	}
	next := uint64(40)
	ordered := facts.RunOrdered{Store: store, Tick: func() (uint64, error) { next++; return next, nil }}
	for want := uint64(41); want <= 42; want++ {
		if o, err := ordered.ObservationOrder(); err != nil || o != (facts.Order{Source: facts.ClockSource, Token: want}) {
			t.Fatalf("order = %+v, %v; want clock %d", o, err, want)
		}
	}
	broken := facts.RunOrdered{Store: store, Tick: func() (uint64, error) { return 0, errors.New("the counter's lock was held") }}
	if _, err := broken.ObservationOrder(); err == nil {
		t.Error("a tick that failed gave an order")
	}
}

// TestFileStore_FailuresAreReportedAndLeaveNoTemporaryFile covers a store
// over something that is not a directory, a state file that does not decode,
// and a directory that refuses the state's temporary file, which leaves no
// temporary file behind.
func TestFileStore_FailuresAreReportedAndLeaveNoTemporaryFile(t *testing.T) {
	ctx := context.Background()
	obs := facts.Observation{Entries: prepared(t, map[string]any{"ansible_kernel": "6.1"}), At: time.Now()}

	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.OpenFileStore(file); err == nil {
		t.Error("a file opened as a store")
	}
	if _, err := facts.NewFileStore(filepath.Join(file, "below")); err == nil {
		t.Error("a store made below a file")
	}
	if _, err := facts.OpenFileStore(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, facts.ErrNoStore) {
		t.Errorf("a missing store: %v, want ErrNoStore", err)
	}

	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev1.state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "dev1", obs); err == nil {
		t.Error("a state file that does not decode was written over")
	}
	if _, err := store.Current(ctx, "dev1"); err == nil {
		t.Error("a state file that does not decode was read")
	}
	// The chain is all in the history, so a broken state file does not stop
	// a check of it.
	if v, err := store.Verify(ctx, "dev1"); err != nil || v.Break != nil {
		t.Errorf("a broken state file stopped a check of an intact history: %+v, %v", v, err)
	}
	if err := store.Record(ctx, "../dev", obs); !errors.Is(err, facts.ErrInvalid) {
		t.Errorf("a device name that leaves the directory: %v", err)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory refuses nothing here")
	}
	locked := filepath.Join(t.TempDir(), "facts")
	ro, err := facts.NewFileStore(locked)
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.Record(ctx, "dev1", obs); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	next := facts.Observation{Entries: prepared(t, map[string]any{"ansible_kernel": "6.2"}), At: time.Now().Add(time.Minute)}
	if err := ro.Record(ctx, "dev1", next); err == nil {
		t.Error("a directory that refuses new files took a new state")
	}
	entries, _ := os.ReadDir(locked)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
