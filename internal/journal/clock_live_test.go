// Package journal_test: run markers bound a run that died before its seal
// (clock_live.go), and the counter refuses what no run writes.
package journal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// startedRun begins a run on a fresh store at root and records one entry for
// runID, returning the store and its start tick.
func startedRun(t *testing.T, root, runID string) (*journal.FileStore, uint64) {
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
		Outcome: engine.OutcomeChanged, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	if err := store.Record(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatal(err)
	}
	return store, tick
}

// TestRunMarkers_ADeadRunIsBoundedByTheNextRun: a run that died before its
// seal reads as ended by the start tick of the first run that began after it
// died, so it no longer counts as later than every run after it.
func TestRunMarkers_ADeadRunIsBoundedByTheNextRun(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" && runtime.GOOS != "windows" {
		t.Skip("needs a lock that tells a live process from a dead one")
	}
	root := t.TempDir()
	const dead, next = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	deadStore, _ := startedRun(t, root, dead)
	journal.DieForTest(deadStore)
	nextStore, nextTick := startedRun(t, root, next)
	if err := nextStore.Seal(next, time.Now()); err != nil {
		t.Fatal(err)
	}
	run, err := journal.ReadRun(root, dead)
	if err != nil {
		t.Fatal(err)
	}
	if run.Sealed || run.EndTick != nextTick {
		t.Errorf("the dead run reads sealed %v, end tick %d; want unsealed, ended by the next run's start tick %d", run.Sealed, run.EndTick, nextTick)
	}
	// The sealed run's own marker is gone.
	if left, _ := filepath.Glob(filepath.Join(root, ".pleiades", "clock.d", "*.live")); len(left) != 0 {
		t.Errorf("markers left after every run ended: %v", left)
	}
}

// TestRunMarkers_ALiveRunIsNotMarkedEnded: a run still going when another
// begins keeps no end tick, so it still counts as possibly later.
func TestRunMarkers_ALiveRunIsNotMarkedEnded(t *testing.T) {
	root := t.TempDir()
	const live, next = "33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"
	liveStore, _ := startedRun(t, root, live)
	nextStore, _ := startedRun(t, root, next)
	_ = nextStore
	run, err := journal.ReadRun(root, live)
	if err != nil {
		t.Fatal(err)
	}
	if run.EndTick != 0 {
		t.Errorf("a live run was given end tick %d", run.EndTick)
	}
	if err := liveStore.Seal(live, time.Now()); err != nil {
		t.Fatal(err)
	}
	if run, _ := journal.ReadRun(root, live); run.EndTick == 0 || !run.Sealed {
		t.Errorf("once sealed, the run reads %+v", run)
	}
}

// TestTickClockRefusesTheLargestTick: a journal recording the largest tick
// there is (no run writes it) is refused, not wrapped to zero, which would
// silently stop the project ticking.
func TestTickClockRefusesTheLargestTick(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"run_id":"` + readerRun + `","run_tick":18446744073709551615}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, readerRun+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.TickClock(root); !errors.Is(err, journal.ErrClockExhausted) {
		t.Errorf("TickClock over the largest tick: %v, want ErrClockExhausted", err)
	}
}

// TestTickClockReadsOnlyWhatTheStrictReaderWould: a journal reached through a
// link, and one others may read, are passed over by the rebuild as the strict
// reader refuses them, and a run marker's tick counts.
func TestTickClockReadsOnlyWhatTheStrictReaderWould(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes and links")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(outside, []byte(`{"run_tick":900}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "aaaaaaaa-0000-4000-8000-000000000001.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aaaaaaaa-0000-4000-8000-000000000002.jsonl"), []byte(`{"run_tick":800}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markers := filepath.Join(root, ".pleiades", "clock.d")
	if err := os.MkdirAll(markers, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markers, strconv.Itoa(40)+".live"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := journal.TickClock(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != 41 {
		t.Errorf("tick = %d, want 41: past the marker's 40, and not past the linked (900) or others-readable (800) journals", got)
	}
}

// TestRunMarkers_FailAndPassOverLoudly: a marker directory that cannot be
// made fails the run's start; markers that are not a run's (a name that is no
// number, a directory, a later run's) are passed over; a dead run already
// recorded keeps its first bound; a marker holding no number reads as none.
// (A marker at or past this run's own tick is another run's that began
// after it, which only a concurrent start can leave; it is passed over.)
func TestRunMarkers_FailAndPassOverLoudly(t *testing.T) {
	t.Run("a marker directory that is a file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".pleiades"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".pleiades", "clock.d"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		store, err := journal.NewFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.BeginRun(); err == nil {
			t.Error("a run started with no marker directory to mark itself in")
		}
	})
	t.Run("what is not a run's marker", func(t *testing.T) {
		root := t.TempDir()
		markers := filepath.Join(root, ".pleiades", "clock.d")
		if err := os.MkdirAll(filepath.Join(markers, "7.live"), 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"notanumber.live": "", "999.live": "", "5.ended": "not a number"} {
			if err := os.WriteFile(filepath.Join(markers, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		store, err := journal.NewFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		tick, err := store.BeginRun()
		if err != nil {
			t.Fatal(err)
		}
		if tick != 1000 {
			t.Errorf("tick %d, want 1000, past the 999 marker", tick)
		}
		// 999 began before this run and nobody holds its marker: it is gone.
		if _, err := os.Stat(filepath.Join(markers, "999.ended")); err != nil {
			t.Error("an earlier run's unheld marker was not recorded ended")
		}
		if _, err := os.Stat(filepath.Join(markers, "notanumber.live")); err != nil {
			t.Error("a marker that names no run was touched")
		}
		if _, err := os.Stat(filepath.Join(markers, "7.live")); err != nil {
			t.Error("a directory named like a marker was touched")
		}
	})
	t.Run("a dead run keeps its first bound", func(t *testing.T) {
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" && runtime.GOOS != "windows" {
			t.Skip("needs a lock that tells a live process from a dead one")
		}
		root := t.TempDir()
		const dead = "55555555-5555-4555-8555-555555555555"
		deadStore, deadTick := startedRun(t, root, dead)
		journal.DieForTest(deadStore)
		markers := filepath.Join(root, ".pleiades", "clock.d")
		// Recorded already, and its marker left behind (a crash between the
		// two writes).
		if err := os.WriteFile(filepath.Join(markers, strconv.FormatUint(deadTick, 10)+".ended"), []byte("3\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := journal.NewFileStore(root); err != nil {
			t.Fatal(err)
		}
		next, err := journal.NewFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := next.BeginRun(); err != nil {
			t.Fatal(err)
		}
		run, err := journal.ReadRun(root, dead)
		if err != nil {
			t.Fatal(err)
		}
		if run.EndTick != 3 {
			t.Errorf("end tick %d, want the first bound recorded, 3", run.EndTick)
		}
	})
}

// TestRunMarkers_AMarkerDirectoryThatCannotBeUsedFailsTheStart: one that
// cannot be listed fails the start once the run has marked itself, taking its
// own marker back; one nothing can be written in fails it before.
func TestRunMarkers_AMarkerDirectoryThatCannotBeUsedFailsTheStart(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a directory mode this user is held to")
	}
	for name, mode := range map[string]os.FileMode{"unlistable": 0o300, "read-only": 0o500} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			markers := filepath.Join(root, ".pleiades", "clock.d")
			if err := os.MkdirAll(markers, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(markers, mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(markers, 0o700) })
			store, err := journal.NewFileStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.BeginRun(); err == nil {
				t.Errorf("a run started with a %s marker directory", name)
			}
			_ = os.Chmod(markers, 0o700)
			if left, _ := filepath.Glob(filepath.Join(markers, "*.live")); len(left) != 0 {
				t.Errorf("a failed start left its marker: %v", left)
			}
		})
	}
}

// TestRunMarkers_EdgesOfTheirOwnFiles: a counter lock that is a directory
// fails the tick, and a run whose marker went missing before its seal still
// seals.
func TestRunMarkers_EdgesOfTheirOwnFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pleiades", "clock.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.TickClock(root); err == nil {
		t.Error("a tick with a directory for its lock succeeded")
	}

	root = t.TempDir()
	store, tick := startedRun(t, root, readerRun)
	if err := os.Remove(filepath.Join(root, ".pleiades", "clock.d", strconv.FormatUint(tick, 10)+".live")); err != nil {
		t.Fatal(err)
	}
	if err := store.Seal(readerRun, time.Now()); err != nil {
		t.Errorf("a run whose marker went missing could not seal: %v", err)
	}
}

// TestLocksRefuseALink: the run lock and the counter's lock are never opened
// through a symbolic link put at their names, which could point a lock, and
// the file it creates, anywhere this user may write.
func TestLocksRefuseALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows' open has no flag refusing a link")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".pleiades")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	for _, name := range []string{"run.lock", "clock.lock"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := journal.LockRuns(root, false); err == nil {
		t.Error("the run lock was taken through a link")
	}
	if _, err := journal.TickClock(root); err == nil {
		t.Error("the counter's lock was taken through a link")
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("a lock opened through a link created its target")
	}
}
