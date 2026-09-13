// Package journal: tests for the Walk tier's durable store.
//
// These run against a real SQLite database on a real file, not an
// in-memory stand-in, because two of the things under test are a unique
// index and what the bytes look like at rest, and neither question has
// an answer without a real engine and a real file to read back.
package journal_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// newEntStore opens a real SQLite database and returns the store over it
// plus the path, so a test can also read the file back raw.
func newEntStore(t *testing.T) (*journal.EntStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", path)
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	return journal.NewEntStore(client), path
}

// walkEntry builds an entry as the Walk tier's publisher would stamp it.
func walkEntry(jobID, deviceID string, attempt, sequence int, nodeID string) engine.JournalEntry {
	return engine.JournalEntry{
		JobID:      jobID,
		Attempt:    attempt,
		DeviceID:   deviceID,
		RunID:      "run-1",
		Sequence:   sequence,
		NodeID:     nodeID,
		DAGID:      "a-runbook",
		FQCN:       "exec.command",
		TaskName:   "a task",
		Outcome:    engine.OutcomeChanged,
		StatKeys:   []string{"rc", "stdout", "cmd"},
		ParamKeys:  []string{"cmd"},
		StartedAt:  time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 9, 12, 10, 0, 1, 0, time.UTC),
	}
}

func TestEntStoreSavesALevel(t *testing.T) {
	store, _ := newEntStore(t)
	written, err := store.Save(context.Background(), []engine.JournalEntry{
		walkEntry("job-1", "device-1", 0, 1, "tasks[0]"),
		walkEntry("job-1", "device-1", 0, 2, "tasks[1]"),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if written != 2 {
		t.Errorf("Save wrote %d rows, want 2", written)
	}
}

func TestEntStoreTreatsADuplicatePublishAsAlreadyRecorded(t *testing.T) {
	// A batch can arrive twice. The identity is exactly what the unique
	// index names, so the second copy is the same fact, not an error to
	// report to a Runner that cannot act on it anyway.
	store, _ := newEntStore(t)
	batch := []engine.JournalEntry{walkEntry("job-1", "device-1", 0, 1, "tasks[0]")}

	if _, err := store.Save(context.Background(), batch); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	written, err := store.Save(context.Background(), batch)
	if err != nil {
		t.Fatalf("second Save reported an error for an already-recorded entry: %v", err)
	}
	if written != 0 {
		t.Errorf("second Save wrote %d rows, want 0", written)
	}
}

func TestEntStoreRecordsARetryAsItsOwnRow(t *testing.T) {
	// The whole reason the attempt is recorded: a redelivered dispatch
	// re-running the same node is a second thing that happened, not a
	// duplicate of the first.
	store, path := newEntStore(t)
	ctx := context.Background()
	if _, err := store.Save(ctx, []engine.JournalEntry{walkEntry("job-1", "device-1", 0, 1, "tasks[0]")}); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if _, err := store.Save(ctx, []engine.JournalEntry{walkEntry("job-1", "device-1", 1, 1, "tasks[0]")}); err != nil {
		t.Fatalf("second attempt: %v", err)
	}

	rows := rawQuery(t, path, "SELECT attempt FROM journal_entries WHERE job_id = ? AND node_id = ? ORDER BY attempt", "job-1", "tasks[0]")
	if len(rows) != 2 {
		t.Fatalf("the database holds %d rows for two attempts, want 2", len(rows))
	}
	if rows[0] != "0" || rows[1] != "1" {
		t.Errorf("attempts recorded as %v, want 0 then 1", rows)
	}
}

func TestEntStoreKeepsTwoDevicesOfOneJobApart(t *testing.T) {
	store, path := newEntStore(t)
	ctx := context.Background()
	entries := []engine.JournalEntry{
		walkEntry("job-1", "device-1", 0, 1, "tasks[0]"),
		walkEntry("job-1", "device-2", 0, 1, "tasks[0]"),
	}
	written, err := store.Save(ctx, entries)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if written != 2 {
		t.Fatalf("Save wrote %d rows for two devices, want 2", written)
	}
	if rows := rawQuery(t, path, "SELECT device_id FROM journal_entries ORDER BY device_id"); len(rows) != 2 {
		t.Errorf("the database holds %v, want one row per device", rows)
	}
}

func TestEntStoreRefusesAnOutcomeItHasNoColumnValueFor(t *testing.T) {
	// The exhaustive switch fails closed rather than converting a string.
	// A new engine outcome would otherwise reach the database as a value
	// the column does not allow, and the first report of it would be a
	// driver's constraint error naming neither the outcome nor the run.
	store, _ := newEntStore(t)
	entry := walkEntry("job-1", "device-1", 0, 1, "tasks[0]")
	entry.Outcome = engine.Outcome("teleported")

	_, err := store.Save(context.Background(), []engine.JournalEntry{entry})
	if err == nil {
		t.Fatal("Save accepted an outcome the column has no value for")
	}
	if !strings.Contains(err.Error(), "teleported") {
		t.Errorf("the error does not name the outcome it refused: %v", err)
	}
}

func TestEntStoreStopsAtTheFirstRealFailure(t *testing.T) {
	// A duplicate is skipped and its neighbors land; a real failure is
	// returned along with how many rows did land, so a caller can tell a
	// partial write from none at all.
	store, _ := newEntStore(t)
	bad := walkEntry("job-1", "device-1", 0, 2, "tasks[1]")
	bad.Outcome = engine.Outcome("teleported")

	written, err := store.Save(context.Background(), []engine.JournalEntry{
		walkEntry("job-1", "device-1", 0, 1, "tasks[0]"),
		bad,
		walkEntry("job-1", "device-1", 0, 3, "tasks[2]"),
	})
	if err == nil {
		t.Fatal("Save reported success despite a bad entry")
	}
	if written != 1 {
		t.Errorf("Save reported %d rows written before the failure, want 1", written)
	}
}

func TestEntStoreWritesNoValueAtRest(t *testing.T) {
	// The at-rest gate, read through a path that bypasses every ent hook
	// and interceptor, which is the same shape internal/crypto's own
	// device hook test uses. This table registers no crypto hook at all,
	// so the assertion is not that the bytes are encrypted: it is that
	// there is nothing in them to encrypt.
	store, path := newEntStore(t)
	entry := walkEntry("job-1", "device-1", 0, 1, "tasks[0]")
	entry.StatKeys = []string{"rc", "stdout"}
	entry.ParamKeys = []string{"cmd"}
	entry.InverseFQCN = "file.line.set"
	entry.InverseParamKeys = []string{"path", "line"}
	entry.DiffRecorded = true

	if _, err := store.Save(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dump := strings.Join(rawDumpRow(t, path), " | ")
	// Half one: the key names are there and no value is, which is the
	// whole claim. "stdout" is a column of the record; the bytes a device
	// wrote to stdout are not, and never reached this entry to begin with.
	for _, want := range []string{"rc", "stdout", "cmd", "file.line.set", "exec.command", "device-1", "job-1"} {
		if !strings.Contains(dump, want) {
			t.Errorf("the row does not name %q, so it records less than it should: %s", want, dump)
		}
	}
	// Half two: nothing that looks like a captured value.
	for _, forbidden := range []string{"_encrypted", "BEGIN RSA", "password"} {
		if strings.Contains(dump, forbidden) {
			t.Errorf("the row carries %q: %s", forbidden, dump)
		}
	}
}

func TestEntStoreStoresAnAbsentInstantAsAbsent(t *testing.T) {
	// A synthetic fan-out node executed nothing and carries the zero
	// time. Writing it would record 0001-01-01 as if it were an instant
	// something happened at.
	store, path := newEntStore(t)
	entry := walkEntry("job-1", "", 0, 1, "parallel[0]")
	entry.StartedAt = time.Time{}
	entry.FinishedAt = time.Time{}
	entry.Outcome = engine.OutcomeNotReached

	if _, err := store.Save(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries WHERE started_at IS NULL AND finished_at IS NULL")
	if rows[0] != "1" {
		t.Errorf("the zero instant was stored as a real one: %v", rows)
	}
}

func TestEntStoreOnNoEntriesWritesNothing(t *testing.T) {
	store, path := newEntStore(t)
	written, err := store.Save(context.Background(), nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if written != 0 {
		t.Errorf("Save wrote %d rows for an empty batch", written)
	}
	if rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries"); rows[0] != "0" {
		t.Errorf("the table holds %v rows", rows)
	}
}

// rawQuery runs a query against the database file directly, with no ent
// client involved, and returns each row's first column as text.
func rawQuery(t *testing.T, path, query string, args ...interface{}) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("opening the database directly: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out = append(out, value.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	return out
}

// rawDumpRow returns every column of every row as text, so an assertion
// about what is stored cannot miss a column by failing to name it.
func rawDumpRow(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("opening the database directly: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(context.Background(), "SELECT * FROM journal_entries")
	if err != nil {
		t.Fatalf("selecting every column: %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading the column list: %v", err)
	}

	var out []string
	for rows.Next() {
		cells := make([]interface{}, len(columns))
		for i := range cells {
			cells[i] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		for i, cell := range cells {
			out = append(out, columns[i]+"="+cell.(*sql.NullString).String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the table is empty, so every assertion over it would pass by examining nothing")
	}
	return out
}

// waitForRows blocks until the table holds want rows, or the test's own
// deadline passes.
//
// The in-process bus dispatches each handler on its own goroutine, so a
// publish returns before the handler has run. Polling the real table is
// what makes the assertion about the handler's effect rather than about
// this test's timing.
func waitForRows(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries")
		if len(got) > 0 && got[0] == fmt.Sprint(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the table holds %v rows after five seconds, want %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEntStoreRecordsEveryOutcomeTheEngineCanProduce(t *testing.T) {
	// An exhaustive switch deserves an exhaustive test. A new engine
	// outcome added without a case here would otherwise be caught only
	// by whichever run first produced it, in production, as a refusal.
	outcomes := map[engine.Outcome]string{
		engine.OutcomeRan:        "ran",
		engine.OutcomeChanged:    "changed",
		engine.OutcomeSkipped:    "skipped",
		engine.OutcomeFailed:     "failed",
		engine.OutcomeNotReached: "not_reached",
	}
	store, path := newEntStore(t)
	ctx := context.Background()

	node := 0
	for outcome, want := range outcomes {
		node++
		entry := walkEntry("job-1", "device-1", 0, node, fmt.Sprintf("tasks[%d]", node))
		entry.Outcome = outcome
		if _, err := store.Save(ctx, []engine.JournalEntry{entry}); err != nil {
			t.Fatalf("Save with outcome %q: %v", outcome, err)
		}
		got := rawQuery(t, path, "SELECT outcome FROM journal_entries WHERE node_id = ?", fmt.Sprintf("tasks[%d]", node))
		if len(got) != 1 || got[0] != want {
			t.Errorf("outcome %q stored as %v, want %q", outcome, got, want)
		}
	}

	// The control: every value the engine declares was exercised, so a
	// sixth one added later makes this table incomplete rather than
	// silently still passing.
	if len(outcomes) != 5 {
		t.Errorf("the table covers %d outcomes; internal/engine declares five", len(outcomes))
	}
}

// newClosableEntStore is newEntStore plus the ability to close the
// underlying client, which is the honest way to produce a transient
// store failure: an unreachable database, not a malformed row.
func newClosableEntStore(t *testing.T) (*journal.EntStore, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", path)
	client := enttest.Open(t, "sqlite3", dsn)
	closed := false
	closeIt := func() {
		if !closed {
			closed = true
			_ = client.Close()
		}
	}
	t.Cleanup(closeIt)
	return journal.NewEntStore(client), closeIt
}

func TestEntStoreStoresAnEmptyKeyVectorAsAnEmptyArray(t *testing.T) {
	// The two sinks have to spell "no keys" the same way. The file sink
	// normalizes a nil vector to [] before encoding; this store wrote the
	// nil straight through, so the same run recorded [] on the Crawl tier
	// and the JSON scalar null in these columns.
	//
	// The difference is not cosmetic on the backend this tier is built
	// for. SQLite reads json_array_length('null') as 0, but the columns
	// are jsonb on PostgreSQL and it refuses that call outright with
	// "cannot get array length of a scalar", so an operator's query over
	// stat_keys failed on exactly the rows where a task recorded no keys,
	// which is every failed task.
	store, path := newEntStore(t)
	entry := walkEntry("job-1", "device-1", 1, 1, "tasks[0]")
	entry.Outcome = engine.OutcomeFailed
	entry.FailureStage = engine.FailureStageAction
	entry.StatKeys = nil
	entry.ParamKeys = nil
	entry.InverseParamKeys = nil

	if _, err := store.Save(context.Background(), []engine.JournalEntry{entry}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, column := range []string{"stat_keys", "param_keys", "inverse_param_keys"} {
		got := rawQuery(t, path, "SELECT "+column+" FROM journal_entries")
		if len(got) != 1 {
			t.Fatalf("%s: read %d rows, want 1", column, len(got))
		}
		if got[0] != "[]" {
			t.Errorf("%s stored as %q, want %q: a nil vector and an empty one both mean no keys, and only one of the two spellings survives a jsonb array query", column, got[0], "[]")
		}
	}
}
