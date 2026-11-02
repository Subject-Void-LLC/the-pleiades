// Package journal_test: the project counter that orders the Crawl tier's
// runs without a clock (clock.go), and the ticks a run's journal and seal
// carry.
package journal_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// counterFile is the project counter's path under root.
func counterFile(root string) string { return filepath.Join(root, ".pleiades", "clock") }

// tickedRun records one entry for runID through a store that took a start
// tick, seals it, and returns the store's start tick.
func tickedRun(t *testing.T, root, runID string, startedAt time.Time) uint64 {
	t.Helper()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	tick, err := store.BeginRun()
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	entry := engine.JournalEntry{RunID: runID, Sequence: 1, NodeID: "tasks[0]", DAGVersion: digest,
		Outcome: engine.OutcomeChanged, StartedAt: startedAt, FinishedAt: startedAt.Add(time.Second)}
	if err := store.Record(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := store.Seal(runID, startedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return tick
}

// TestTickClockCountsUpAndPersists: each tick is one more than the last, and
// the file holds the latest.
func TestTickClockCountsUpAndPersists(t *testing.T) {
	root := t.TempDir()
	for want := uint64(1); want <= 3; want++ {
		got, err := journal.TickClock(root)
		if err != nil || got != want {
			t.Fatalf("tick %d = %d, %v", want, got, err)
		}
	}
	raw, err := os.ReadFile(counterFile(root))
	if err != nil || strings.TrimSpace(string(raw)) != "3" {
		t.Errorf("the counter file holds %q, %v; want 3", raw, err)
	}
}

// TestTickClockGivesOverlappingRunsDistinctTicks: runs ticking at once (the
// run lock is shared, so they can) never share a tick.
func TestTickClockGivesOverlappingRunsDistinctTicks(t *testing.T) {
	root := t.TempDir()
	const n = 24
	ticks := make([]uint64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ticks[i], errs[i] = journal.TickClock(root)
		}(i)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for i, tick := range ticks {
		if errs[i] != nil {
			t.Fatalf("tick %d: %v", i, errs[i])
		}
		if seen[tick] {
			t.Fatalf("two runs got tick %d", tick)
		}
		seen[tick] = true
	}
}

// TestRunTicksAreStampedAndSealed: every entry of a run that took a start
// tick carries it, whatever the entry brought, and the seal holds a later
// end tick, which the reader returns.
func TestRunTicksAreStampedAndSealed(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	start, err := store.BeginRun()
	if err != nil {
		t.Fatal(err)
	}
	e := func(seq int, node string, brought uint64) engine.JournalEntry {
		return engine.JournalEntry{RunID: readerRun, Sequence: seq, NodeID: node, DAGVersion: digest, RunTick: brought,
			Outcome: engine.OutcomeChanged, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	}
	if err := store.Record(context.Background(), []engine.JournalEntry{e(1, "tasks[0]", 0), e(2, "tasks[1]", 999)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Seal(readerRun, time.Now()); err != nil {
		t.Fatal(err)
	}
	run, err := journal.ReadRun(root, readerRun)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range run.Entries {
		if entry.RunTick != start {
			t.Errorf("entry %s carries tick %d, want the run's start tick %d", entry.NodeID, entry.RunTick, start)
		}
	}
	if run.EndTick <= start {
		t.Errorf("the seal's end tick is %d, not after the start tick %d", run.EndTick, start)
	}
}

// TestTickClockRebuildsAMissingOrBehindCounter: a deleted counter, one set
// back, and one holding nothing readable are each rebuilt from the journals,
// so a new run never reads as older than one already journaled.
func TestTickClockRebuildsAMissingOrBehindCounter(t *testing.T) {
	for name, damage := range map[string]func(path string) error{
		"deleted":    os.Remove,
		"set back":   func(path string) error { return os.WriteFile(path, []byte("1\n"), 0o600) },
		"unreadable": func(path string) error { return os.WriteFile(path, []byte("not a number\n"), 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			tickedRun(t, root, readerRun, time.Now().UTC())
			run, err := journal.ReadRun(root, readerRun)
			if err != nil {
				t.Fatal(err)
			}
			if err := damage(counterFile(root)); err != nil {
				t.Fatal(err)
			}
			next, err := journal.TickClock(root)
			if err != nil {
				t.Fatal(err)
			}
			if next <= run.EndTick {
				t.Errorf("after the counter was %s, the next tick is %d, not after the journaled end tick %d", name, next, run.EndTick)
			}
		})
	}
}

// TestListRunsOrdersByTickNotClock: two runs whose clock times read in the
// opposite order to their ticks (a clock stepped back between them) list in
// tick order, and a run from before the counter lists first.
func TestListRunsOrdersByTickNotClock(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	const first, second, legacy = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	tickedRun(t, root, first, now)
	// The clock went back an hour before the second run.
	tickedRun(t, root, second, now.Add(-time.Hour))
	// A run journaled before the counter existed: no tick, and the latest time.
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	old := engine.JournalEntry{RunID: legacy, Sequence: 1, NodeID: "tasks[0]", DAGVersion: digest,
		Outcome: engine.OutcomeChanged, StartedAt: now.Add(time.Hour), FinishedAt: now.Add(time.Hour)}
	if err := store.Record(context.Background(), []engine.JournalEntry{old}); err != nil {
		t.Fatal(err)
	}
	runs, err := journal.ListRuns(root)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range runs {
		order = append(order, r.ID[:1])
	}
	if got := strings.Join(order, ","); got != "3,1,2" {
		t.Errorf("runs listed %s, want 3,1,2 (the legacy run, then by tick)", got)
	}
}

// TestTickClockWaitsForAnotherTick: a tick finds the counter's lock held by
// another for a moment and waits rather than failing.
func TestTickClockWaitsForAnotherTick(t *testing.T) {
	root := t.TempDir()
	if _, err := journal.TickClock(root); err != nil {
		t.Fatal(err)
	}
	release := holdClockLock(t, root)
	done := make(chan uint64, 1)
	go func() {
		tick, err := journal.TickClock(root)
		if err != nil {
			t.Errorf("TickClock: %v", err)
		}
		done <- tick
	}()
	time.Sleep(100 * time.Millisecond)
	release()
	select {
	case tick := <-done:
		if tick != 2 {
			t.Errorf("tick = %d, want 2", tick)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the tick never took the lock once it was free")
	}
	if raw, _ := os.ReadFile(counterFile(root)); strings.TrimSpace(string(raw)) != strconv.Itoa(2) {
		t.Errorf("counter %q, want 2", raw)
	}
}

// holdClockLock takes the counter's lock and returns the function that lets
// it go.
func holdClockLock(t *testing.T, root string) func() {
	t.Helper()
	release, err := journal.HoldClockLock(root)
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}
}

// TestTickClockFailsLoudly: a counter that cannot be ticked is an error the
// run reports, never a tick quietly skipped: a project directory that is a
// file, a lock held past the wait, and a .pleiades nothing can be written in.
func TestTickClockFailsLoudly(t *testing.T) {
	t.Run("a project directory that is a file", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(root, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := journal.TickClock(root); err == nil {
			t.Error("a tick under a file succeeded")
		}
	})
	t.Run("a lock held past the wait", func(t *testing.T) {
		root := t.TempDir()
		if _, err := journal.TickClock(root); err != nil {
			t.Fatal(err)
		}
		release := holdClockLock(t, root)
		defer release()
		journal.SetClockLockWait(t, 50*time.Millisecond)
		if _, err := journal.TickClock(root); err == nil || !strings.Contains(err.Error(), "was held for over") {
			t.Errorf("a tick behind a held lock: %v", err)
		}
	})
	t.Run("a journal directory that is a file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".pleiades"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".pleiades", "journal"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := journal.TickClock(root); err == nil {
			t.Error("a tick that could not read the journals succeeded")
		}
	})
	t.Run("a counter that is a directory", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".pleiades", "clock", "inside"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := journal.TickClock(root); err == nil || !strings.Contains(err.Error(), "failed to replace the run counter") {
			t.Errorf("a tick over a counter that is a directory: %v", err)
		}
	})
	t.Run("a .pleiades nothing can be written in", func(t *testing.T) {
		if os.Geteuid() == 0 || runtime.GOOS == "windows" {
			t.Skip("needs a directory this user cannot write")
		}
		root := t.TempDir()
		store, err := journal.NewFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.BeginRun(); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, ".pleiades")
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if _, err := store.BeginRun(); err == nil {
			t.Error("a start tick in a read-only .pleiades succeeded")
		}
		if err := store.Seal(readerRun, time.Now()); err == nil || !strings.Contains(err.Error(), "failed to tick") {
			t.Errorf("a seal whose end tick could not be written: %v", err)
		}
	})
}

// TestTickClockPassesOverWhatItCannotRead: the rebuild reads a floor from
// each journal and seal and passes over whatever does not decode, an empty
// journal, a subdirectory or an oversized seal, leaving those to the strict
// reader.
func TestTickClockPassesOverWhatItCannotRead(t *testing.T) {
	root := t.TempDir()
	tickedRun(t, root, readerRun, time.Now().UTC())
	run, err := journal.ReadRun(root, readerRun)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".pleiades", "journal")
	for name, body := range map[string]string{
		"aaaaaaaa-0000-4000-8000-000000000001.jsonl": "",
		"aaaaaaaa-0000-4000-8000-000000000002.jsonl": "not json\n",
		"aaaaaaaa-0000-4000-8000-000000000003.end":   "not json",
		"aaaaaaaa-0000-4000-8000-000000000004.end":   strings.Repeat("x", 5<<10),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "aaaaaaaa-0000-4000-8000-000000000005.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(counterFile(root)); err != nil {
		t.Fatal(err)
	}
	next, err := journal.TickClock(root)
	if err != nil {
		t.Fatal(err)
	}
	if next != run.EndTick+1 {
		t.Errorf("rebuilt tick %d, want one past the journaled end tick %d", next, run.EndTick)
	}
}
