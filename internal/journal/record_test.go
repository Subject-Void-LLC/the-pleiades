// Package journal_test: the write path.
//
// Every assertion reads the file back with os.ReadFile and a plain JSON
// decode, never through the store that wrote it. A test that read its
// own writer back would prove the writer agreed with itself, which is
// the one thing nobody doubts.
package journal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// newStore builds a store over a fresh project directory and returns
// both, since most assertions need the path as well as the store.
func newStore(t *testing.T) (*journal.FileStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store, root
}

// entry builds a minimally populated entry for a run.
func entry(runID string, sequence int, nodeID string) engine.JournalEntry {
	return engine.JournalEntry{
		RunID:    runID,
		Sequence: sequence,
		NodeID:   nodeID,
		DAGID:    "a-runbook",
		Outcome:  engine.OutcomeRan,
	}
}

// readLines returns the raw lines of a run's file, decoded as generic
// JSON objects so the assertions are about the bytes on disk rather than
// about a round trip through the same struct that wrote them.
func readLines(t *testing.T, root, runID string) []map[string]interface{} {
	t.Helper()
	path := filepath.Join(root, ".pleiades", "journal", runID+".jsonl")
	data, err := os.ReadFile(path) // #nosec G304 -- a path this test built from its own t.TempDir
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", line, err)
		}
		out = append(out, obj)
	}
	return out
}

func TestRecordWritesOneLinePerEntry(t *testing.T) {
	store, root := newStore(t)
	err := store.Record(context.Background(), []engine.JournalEntry{
		entry("run-a", 1, "tasks[0]"),
		entry("run-a", 2, "tasks[1]"),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	lines := readLines(t, root, "run-a")
	if len(lines) != 2 {
		t.Fatalf("file holds %d lines, want 2", len(lines))
	}
	if got := lines[0]["node_id"]; got != "tasks[0]" {
		t.Errorf("first line node_id = %v, want tasks[0]", got)
	}
	if got := lines[1]["sequence"]; got != float64(2) {
		t.Errorf("second line sequence = %v, want 2", got)
	}
}

func TestRecordUsesTheSnakeCaseNamesAsTheFormat(t *testing.T) {
	// The json tags are the on-disk format. A reader and a later Walk
	// tier publisher both depend on these exact names, so they are
	// asserted rather than left to whatever the struct happens to spell.
	store, root := newStore(t)
	if err := store.Record(context.Background(), []engine.JournalEntry{entry("run-a", 1, "tasks[0]")}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	line := readLines(t, root, "run-a")[0]
	for _, key := range []string{
		"job_id", "attempt", "run_id", "sequence", "node_id", "dag_id", "dag_version",
		"fqcn", "fqcn_unresolved", "provider_program", "provider_digest",
		"task_name", "register", "device_id",
		"started_at", "finished_at", "outcome", "failure_stage",
		"skip_kind", "skip_ordinal", "skip_total",
		"stat_keys", "undeclared_stat_count", "param_keys", "undeclared_param_count",
		"inverse_fqcn", "inverse_fqcn_unresolved", "inverse_param_keys",
		"undeclared_inverse_param_count", "diff_recorded",
		"inverse_params", "inverse_complete", "inverse_partial",
		"action_changed", "authored_rollback", "rollback_of", "undoes_node", "undoes_step",
	} {
		if _, ok := line[key]; !ok {
			t.Errorf("the record has no %q key: %v", key, line)
		}
	}
	if len(line) != 38 {
		t.Errorf("the record holds %d keys, want 38: no field carries omitempty, so every line is whole", len(line))
	}
	// An entry with no recorded undo values writes [], never null, as the
	// three key vectors do.
	if params, ok := line["inverse_params"].([]any); !ok || len(params) != 0 {
		t.Errorf("inverse_params = %#v, want an empty array", line["inverse_params"])
	}
}

func TestRecordAppendsRatherThanTruncating(t *testing.T) {
	// One Record call per topological level means a run's file is built
	// up across several calls. Truncating would leave only the last
	// level, which is the opposite of an append-only audit record.
	store, root := newStore(t)
	for i := 1; i <= 3; i++ {
		if err := store.Record(context.Background(), []engine.JournalEntry{entry("run-a", i, fmt.Sprintf("tasks[%d]", i))}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	if lines := readLines(t, root, "run-a"); len(lines) != 3 {
		t.Errorf("file holds %d lines after three calls, want 3", len(lines))
	}
}

func TestRecordSplitsConcurrentRunsIntoTheirOwnFiles(t *testing.T) {
	// One Executor can serve concurrent Run calls, so a batch carrying
	// two run ids is allowed. A sink that read entries[0].RunID and wrote
	// the rest under it would put one run's entries in another's file.
	store, root := newStore(t)
	err := store.Record(context.Background(), []engine.JournalEntry{
		entry("run-a", 1, "tasks[0]"),
		entry("run-b", 1, "tasks[0]"),
		entry("run-a", 2, "tasks[1]"),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if lines := readLines(t, root, "run-a"); len(lines) != 2 {
		t.Errorf("run-a holds %d lines, want 2", len(lines))
	}
	if lines := readLines(t, root, "run-b"); len(lines) != 1 {
		t.Errorf("run-b holds %d lines, want 1", len(lines))
	}
}

func TestRecordWritesTheFileOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	store, root := newStore(t)
	if err := store.Record(context.Background(), []engine.JournalEntry{entry("run-a", 1, "tasks[0]")}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	path := filepath.Join(root, ".pleiades", "journal", "run-a.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("%s has mode %04o, want 0600", path, got)
	}
}

func TestRecordIsSafeUnderConcurrentCalls(t *testing.T) {
	// The port requires it: the sink is held on the Executor, not per
	// run, so two Record calls can be in flight with different run ids.
	// O_APPEND alone does not make a several hundred byte line atomic
	// against another writer, which is why there is a mutex. Run this
	// under -race.
	store, root := newStore(t)
	const writers, perWriter = 8, 25

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				e := entry(fmt.Sprintf("run-%d", w), i, fmt.Sprintf("tasks[%d]", i))
				if err := store.Record(context.Background(), []engine.JournalEntry{e}); err != nil {
					t.Errorf("Record: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	for w := 0; w < writers; w++ {
		runID := fmt.Sprintf("run-%d", w)
		if lines := readLines(t, root, runID); len(lines) != perWriter {
			t.Errorf("%s holds %d lines, want %d", runID, len(lines), perWriter)
		}
	}
}

func TestRecordKeepsTheUnresolvedSentinelReadable(t *testing.T) {
	// encoding/json escapes angle brackets by default for safe embedding
	// in HTML, which this file is not. Left on, the sentinel the
	// projection stores for an unresolved FQCN lands as an escape
	// sequence instead of the readable name it was chosen to be.
	store, root := newStore(t)
	e := entry("run-a", 1, "tasks[0]")
	e.FQCN = "<unregistered>"
	e.FQCNUnresolved = true
	if err := store.Record(context.Background(), []engine.JournalEntry{e}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	path := filepath.Join(root, ".pleiades", "journal", "run-a.jsonl")
	data, err := os.ReadFile(path) // #nosec G304 -- a path this test built from its own t.TempDir
	if err != nil {
		t.Fatalf("reading the file: %v", err)
	}
	if !strings.Contains(string(data), `"<unregistered>"`) {
		t.Errorf("the sentinel was escaped on disk: %s", data)
	}
}

func TestRecordWritesEmptyKeyVectorsRatherThanNull(t *testing.T) {
	// The projection leaves a vector nil whenever it admitted no keys,
	// and encoding/json writes null for a nil slice. Both mean "no
	// keys", so the difference carries nothing and only makes every
	// reader handle two spellings of one fact.
	store, root := newStore(t)
	if err := store.Record(context.Background(), []engine.JournalEntry{entry("run-a", 1, "tasks[0]")}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	line := readLines(t, root, "run-a")[0]
	for _, key := range []string{"stat_keys", "param_keys", "inverse_param_keys"} {
		value, ok := line[key]
		if !ok {
			t.Fatalf("no %q key", key)
		}
		if value == nil {
			t.Errorf("%q is null, want an empty array", key)
			continue
		}
		if _, ok := value.([]interface{}); !ok {
			t.Errorf("%q is %T, want an array", key, value)
		}
	}
}

func TestRecordOnNoEntriesWritesNothing(t *testing.T) {
	store, root := newStore(t)
	if err := store.Record(context.Background(), nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".pleiades", "journal"))
	if err != nil {
		t.Fatalf("reading the journal directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("an empty batch created %d files", len(entries))
	}
}

func TestRecordRefusesARunIDThatCouldNameAnotherFile(t *testing.T) {
	// The engine mints RunID as a uuid, so nothing produces a bad one
	// today. It is checked anyway: "safe because of who usually produces
	// it, not validated" is the exact trap FAILURE_PATTERNS.md #63
	// records, where an unvalidated id widened a NATS subject wildcard
	// into every job's logs and every producer of it was a uuid too.
	store, _ := newStore(t)
	for name, runID := range map[string]string{
		"empty":          "",
		"traversal":      "../../etc/passwd",
		"separator":      "a/b",
		"dot":            "..",
		"null byte":      "a\x00b",
		"absolute":       "/etc/passwd",
		"too long":       strings.Repeat("a", 65),
		"space":          "run a",
		"tilde":          "~",
		"windows walk":   `..\..\x`,
		"shell metachar": "run;rm -rf /",
	} {
		t.Run(name, func(t *testing.T) {
			err := store.Record(context.Background(), []engine.JournalEntry{entry(runID, 1, "tasks[0]")})
			if err == nil {
				t.Errorf("Record accepted the run id %q", runID)
			}
		})
	}
}

func TestRecordAcceptsAUUIDShapedRunID(t *testing.T) {
	// The negative control for the rule above. A whitelist that refused
	// the one shape the engine actually mints would refuse every run.
	store, root := newStore(t)
	const runID = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	if err := store.Record(context.Background(), []engine.JournalEntry{entry(runID, 1, "tasks[0]")}); err != nil {
		t.Fatalf("Record refused a uuid: %v", err)
	}
	if lines := readLines(t, root, runID); len(lines) != 1 {
		t.Errorf("file holds %d lines, want 1", len(lines))
	}
}

func TestRecordStopsOnAnExpiredContext(t *testing.T) {
	// The engine hands Record a detached context with its own timeout,
	// which the sink may not lengthen. A sink that ignored the deadline
	// would hold a level barrier open past the point the engine gave up
	// waiting for it.
	store, _ := newStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	if err := store.Record(ctx, []engine.JournalEntry{entry("run-a", 1, "tasks[0]")}); err == nil {
		t.Error("Record ignored an expired context")
	}
}

func TestRecordAcceptsAnUppercaseRunID(t *testing.T) {
	// The whitelist admits the whole of [A-Za-z0-9-] rather than the
	// lowercase hex a uuid happens to spell, so an id minted differently
	// later is not refused by a rule that was only ever about path
	// separators.
	store, root := newStore(t)
	const runID = "3F2504E0-4F89-41D3-9A0C-0305E82C3301"
	if err := store.Record(context.Background(), []engine.JournalEntry{entry(runID, 1, "tasks[0]")}); err != nil {
		t.Fatalf("Record refused an uppercase id: %v", err)
	}
	if lines := readLines(t, root, runID); len(lines) != 1 {
		t.Errorf("file holds %d lines, want 1", len(lines))
	}
}

func TestRecordReportsAFileItCannotOpen(t *testing.T) {
	// The directory is checked once, at construction. Something can still
	// take the permission away afterwards, and a sink that swallowed that
	// would look exactly like a run that journaled nothing.
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test depends on")
	}
	store, root := newStore(t)
	dir := filepath.Join(root, ".pleiades", "journal")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("restoring the directory: %v", err)
		}
	})

	if err := store.Record(context.Background(), []engine.JournalEntry{entry("run-a", 1, "tasks[0]")}); err == nil {
		t.Error("Record reported success writing into a directory it cannot write")
	}
}

func TestRecordReportsAFullDisk(t *testing.T) {
	// A disk that fills up is the realistic failure for an append-only
	// file that grows once per run with nothing pruning it, and it is the
	// one write failure that can be injected honestly rather than
	// simulated: /dev/full accepts an open and fails every write with
	// ENOSPC.
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is a Linux device")
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skipf("/dev/full is not present: %v", err)
	}

	store, root := newStore(t)
	const runID = "full-disk"
	path := filepath.Join(root, ".pleiades", "journal", runID+".jsonl")
	if err := os.Symlink("/dev/full", path); err != nil {
		t.Fatalf("pointing the run's file at /dev/full: %v", err)
	}

	err := store.Record(context.Background(), []engine.JournalEntry{entry(runID, 1, "tasks[0]")})
	if err == nil {
		t.Fatal("Record reported success writing to a device that accepts nothing")
	}
	if !strings.Contains(err.Error(), runID) {
		t.Errorf("the error does not name the run: %v", err)
	}
}
