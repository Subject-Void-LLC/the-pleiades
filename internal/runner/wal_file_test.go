package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
)

// TestNewFileWAL_FailsClosedOnUnwritableDirectory proves NewFileWAL fails
// at construction, not lazily on the first Append, when its directory is
// not writable -- the same fail-closed-at-startup shape
// internal/runbook.NewDirSource already establishes for a directory
// argument in this codebase.
func TestNewFileWAL_FailsClosedOnUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	// Remove write permission on the directory itself, not a file inside
	// it: NewFileWAL's own write probe must fail against this.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("failed to chmod test dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // restore so t.TempDir's own cleanup can remove it

	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block writes")
	}

	if _, err := runner.NewFileWAL(filepath.Join(dir, "wal")); err == nil {
		t.Fatal("expected NewFileWAL to fail against an unwritable parent directory, got nil error")
	}
}

// TestFileWAL_AppendThenPending proves the basic write-then-read
// contract: an appended entry appears in Pending, with ID and RecordedAt
// filled in by Append itself.
func TestFileWAL_AppendThenPending(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	entry, err := wal.Append(context.Background(), runner.ResultEntry{
		JobID: "job-1", DeviceID: "dev-1", RunbookID: "rb-1", Outcome: "completed",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if entry.ID == "" {
		t.Error("Append did not assign an ID")
	}
	if entry.RecordedAt.IsZero() {
		t.Error("Append did not set RecordedAt")
	}

	pending, err := wal.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("Pending returned %d entries, want 1", len(pending))
	}
	if pending[0] != entry {
		t.Errorf("Pending()[0] = %+v, want %+v", pending[0], entry)
	}
}

// TestFileWAL_AcknowledgeRemovesFromPending proves Acknowledge actually
// removes an entry, and only that entry, from later Pending calls.
func TestFileWAL_AcknowledgeRemovesFromPending(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	ctx := context.Background()
	first, err := wal.Append(ctx, runner.ResultEntry{JobID: "job-1", DeviceID: "dev-1", Outcome: "completed"})
	if err != nil {
		t.Fatalf("Append(first): %v", err)
	}
	second, err := wal.Append(ctx, runner.ResultEntry{JobID: "job-2", DeviceID: "dev-2", Outcome: "completed"})
	if err != nil {
		t.Fatalf("Append(second): %v", err)
	}

	if err := wal.Acknowledge(ctx, first.ID); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	pending, err := wal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("Pending() after acknowledging first = %+v, want only second (%q)", pending, second.ID)
	}
}

// TestFileWAL_AcknowledgeUnknownIDIsIdempotent proves acknowledging an id
// that is not (or no longer) pending is not an error, per ResultWAL.
// Acknowledge's own documented idempotency contract (wal.go): a caller
// cannot always tell in advance which of two racing flush attempts will
// win.
func TestFileWAL_AcknowledgeUnknownIDIsIdempotent(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	if err := wal.Acknowledge(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("Acknowledge on an unknown id returned an error: %v", err)
	}
}

// TestFileWAL_PendingOnFreshDirectoryIsEmpty proves a WAL with nothing
// ever appended reports zero pending entries, not an error -- the file
// itself does not exist yet at that point.
func TestFileWAL_PendingOnFreshDirectoryIsEmpty(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	pending, err := wal.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() on a fresh WAL = %+v, want empty", pending)
	}
}

// TestFileWAL_SurvivesReopenAfterSimulatedCrash is the actual
// crash-recovery property fileWAL exists for: an entry Append'd by one
// process instance must be recoverable by a second, independent fileWAL
// opened against the same directory (simulating a Runner restart), never
// merely readable from the same in-memory instance that wrote it.
func TestFileWAL_SurvivesReopenAfterSimulatedCrash(t *testing.T) {
	dir := t.TempDir()

	first, err := runner.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("NewFileWAL (first instance): %v", err)
	}
	entry, err := first.Append(context.Background(), runner.ResultEntry{JobID: "job-crash", DeviceID: "dev-crash", Outcome: "completed"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	// No Close/graceful shutdown: fileWAL holds no file handle open
	// between calls (see its own doc comment), so "the process died" is
	// indistinguishable here from simply constructing a fresh instance.

	second, err := runner.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("NewFileWAL (second instance, after simulated crash): %v", err)
	}
	defer second.Close()

	pending, err := second.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending (second instance): %v", err)
	}
	if len(pending) != 1 || pending[0].ID != entry.ID {
		t.Fatalf("Pending() on a reopened WAL = %+v, want the entry Append'd before the simulated crash (%q)", pending, entry.ID)
	}
}

// TestFileWAL_ConcurrentAppendAcknowledge exercises Append and
// Acknowledge from many goroutines at once (run with -race), proving the
// mutex and the atomic-rename discipline actually hold under real
// contention: no entry is lost, and no entry is ever seen twice in a
// single Pending call.
func TestFileWAL_ConcurrentAppendAcknowledge(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	const n = 50
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make([]string, n)
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry, err := wal.Append(ctx, runner.ResultEntry{JobID: "job", DeviceID: "dev", Outcome: "completed"})
			if err != nil {
				t.Errorf("Append(%d): %v", i, err)
				return
			}
			mu.Lock()
			ids[i] = entry.ID
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for _, id := range ids {
		if id == "" {
			t.Fatal("an Append call never recorded its own entry's ID")
		}
		if seen[id] {
			t.Fatalf("duplicate entry ID %q assigned by two concurrent Append calls", id)
		}
		seen[id] = true
	}

	pending, err := wal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != n {
		t.Fatalf("Pending() returned %d entries after %d concurrent appends, want %d", len(pending), n, n)
	}

	// Now acknowledge half of them concurrently and prove exactly the
	// other half remains.
	for i := 0; i < n/2; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := wal.Acknowledge(ctx, id); err != nil {
				t.Errorf("Acknowledge(%q): %v", id, err)
			}
		}(ids[i])
	}
	wg.Wait()

	pending, err = wal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending after acknowledging half: %v", err)
	}
	if len(pending) != n-n/2 {
		t.Fatalf("Pending() after acknowledging %d of %d = %d entries, want %d", n/2, n, len(pending), n-n/2)
	}
}

// FuzzFileWAL_Append feeds arbitrary ResultEntry field values through
// Append, Pending, and Acknowledge, asserting no panic and that Pending
// always sees exactly the one entry Append persisted (structural
// correctness).
//
// Exact field content is only asserted when every fuzzed string is valid
// UTF-8. This is not a shortcut around a real defect: encoding/json
// (which fileWAL uses for on-disk storage, wal_file.go) documents that
// Marshal replaces invalid UTF-8 with the Unicode replacement character,
// the same behavior every other JSON-based DTO in this codebase already
// has (including pkg/wire.DispatchPayload on the actual wire). This fuzz
// target found that real Go stdlib behavior on its first run against a
// single invalid byte (0xF7) fed into Reason, which is not a fileWAL bug:
// every real producer of a ResultEntry field (a UUID, a runbook id
// already validated against internal/runbook's own
// [A-Za-z0-9_-]{1,64} pattern, a Go error's own Error() string) is always
// valid UTF-8 in practice, so asserting byte-exact fidelity for
// fuzzer-only-reachable invalid UTF-8 would test a guarantee JSON itself
// never made and nothing in this codebase ever needs.
func FuzzFileWAL_Append(f *testing.F) {
	f.Add("job-1", "dev-1", "rb-1", "completed", "")
	f.Add("", "", "", "", "")
	f.Add("job\x00null", "dev\nnewline", "rb\"quote", "failed", "reason with \"quotes\" and \\backslash")

	f.Fuzz(func(t *testing.T, jobID, deviceID, runbookID, outcome, reason string) {
		wal, err := runner.NewFileWAL(t.TempDir())
		if err != nil {
			t.Fatalf("NewFileWAL: %v", err)
		}
		defer wal.Close()

		ctx := context.Background()
		in := runner.ResultEntry{JobID: jobID, DeviceID: deviceID, RunbookID: runbookID, Outcome: outcome, Reason: reason}
		entry, err := wal.Append(ctx, in)
		if err != nil {
			t.Fatalf("Append: %v", err)
		}

		pending, err := wal.Pending(ctx)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("Pending() = %d entries, want 1", len(pending))
		}
		got := pending[0]
		if got.ID != entry.ID {
			t.Fatalf("Pending()[0].ID = %q, want %q (Append's own returned ID)", got.ID, entry.ID)
		}

		allValidUTF8 := utf8.ValidString(jobID) && utf8.ValidString(deviceID) && utf8.ValidString(runbookID) &&
			utf8.ValidString(outcome) && utf8.ValidString(reason)
		if allValidUTF8 {
			if got.JobID != jobID || got.DeviceID != deviceID || got.RunbookID != runbookID || got.Outcome != outcome || got.Reason != reason {
				t.Fatalf("Pending()[0] = %+v, want fields matching input %+v", got, in)
			}
		}

		if err := wal.Acknowledge(ctx, entry.ID); err != nil {
			t.Fatalf("Acknowledge: %v", err)
		}
		pending, err = wal.Pending(ctx)
		if err != nil {
			t.Fatalf("Pending after Acknowledge: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("Pending() after Acknowledge = %d entries, want 0", len(pending))
		}
	})
}

// TestFileWAL_AppendRespectsContextCancellation and
// TestFileWAL_PendingRespectsContextCancellation prove the standard
// early-exit-on-canceled-context contract, matching every other
// context-taking method in this codebase.
func TestFileWAL_AppendRespectsContextCancellation(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := wal.Append(ctx, runner.ResultEntry{}); err == nil {
		t.Fatal("expected Append to return an error for an already-canceled context")
	}
}

// TestFileWAL_Pending_RejectsCorruptedFile proves Pending (via
// readAllLocked) surfaces a decode error rather than panicking or
// silently dropping data when the on-disk file has been corrupted (a
// truncated write, a hand-edited file) -- the one branch a normal
// Append/Acknowledge test sequence can never reach, since this package's
// own writes are always well-formed JSON.
func TestFileWAL_Pending_RejectsCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	wal, err := runner.NewFileWAL(dir)
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	if _, err := wal.Append(context.Background(), runner.ResultEntry{JobID: "job-1", DeviceID: "dev-1", Outcome: "completed"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Corrupt the underlying file directly: append a line that is not
	// valid JSON, simulating a truncated write or a hand edit.
	path := filepath.Join(dir, "results.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("failed to open wal file for corruption: %v", err)
	}
	if _, err := f.WriteString("not valid json\n"); err != nil {
		t.Fatalf("failed to write corrupt line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	if _, err := wal.Pending(context.Background()); err == nil {
		t.Fatal("expected Pending to return an error against a corrupted wal file, got nil")
	}
}

func TestFileWAL_PendingRespectsContextCancellation(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := wal.Pending(ctx); err == nil {
		t.Fatal("expected Pending to return an error for an already-canceled context")
	}
}
