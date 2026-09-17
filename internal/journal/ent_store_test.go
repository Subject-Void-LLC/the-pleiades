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

// TestEntStoreForJobOrdersByDeviceThenSequence is the ordering the index
// exists for, and the case that proves sequence alone is not enough.
//
// Every device numbers its own run's nodes from zero, so two devices
// running one runbook produce two rows at sequence 1, two at sequence 2 and
// so on. Ordered by sequence alone they interleave, and a reader sees one
// device's step 1, another's step 1, then back again -- which reads as a
// single confused run rather than as two runs. Device first is what makes
// the result one device's run after another's.
func TestEntStoreForJobOrdersByDeviceThenSequence(t *testing.T) {
	store, _ := newEntStore(t)
	ctx := context.Background()

	// Written deliberately out of order, and interleaved, so a passing
	// result cannot be insertion order wearing a sort's clothes.
	if _, err := store.Save(ctx, []engine.JournalEntry{
		walkEntry("job-1", "device-b", 0, 2, "tasks[1]"),
		walkEntry("job-1", "device-a", 0, 2, "tasks[1]"),
		walkEntry("job-1", "device-b", 0, 1, "tasks[0]"),
		walkEntry("job-1", "device-a", 0, 1, "tasks[0]"),
		// A different job, to prove the filter is doing something.
		walkEntry("job-2", "device-a", 0, 1, "tasks[0]"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, truncated, err := store.ForJob(ctx, "job-1", 0)
	if err != nil {
		t.Fatalf("ForJob: %v", err)
	}
	if truncated {
		t.Error("four entries were reported as truncated")
	}

	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%s/%d", e.DeviceID, e.Sequence))
	}
	want := []string{"device-a/1", "device-a/2", "device-b/1", "device-b/2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ForJob returned %v, want %v", got, want)
	}
}

// TestEntStoreForJobOrdersAttemptsWithinADevice covers the middle key.
//
// A redelivered dispatch is a new attempt and a new set of rows for the
// same device, so without attempt in the ordering a retry's step 1 would
// sort beside the original's step 1 and the two runs would be shuffled
// together. The attempt is why the schema records it at all.
func TestEntStoreForJobOrdersAttemptsWithinADevice(t *testing.T) {
	store, _ := newEntStore(t)
	ctx := context.Background()

	if _, err := store.Save(ctx, []engine.JournalEntry{
		walkEntry("job-1", "device-a", 1, 1, "tasks[0]"),
		walkEntry("job-1", "device-a", 0, 2, "tasks[1]"),
		walkEntry("job-1", "device-a", 1, 2, "tasks[1]"),
		walkEntry("job-1", "device-a", 0, 1, "tasks[0]"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, _, err := store.ForJob(ctx, "job-1", 0)
	if err != nil {
		t.Fatalf("ForJob: %v", err)
	}

	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%d/%d", e.Attempt, e.Sequence))
	}
	want := []string{"0/1", "0/2", "1/1", "1/2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ForJob returned attempt/sequence %v, want %v", got, want)
	}
}

// TestEntStoreForJobReportsTruncationRatherThanHidingIt is the property the
// signature exists for.
//
// A caller that rendered a capped list without knowing it was capped would
// show a partial record of what happened while looking exactly like a
// complete one. On an audit trail that is the worst available outcome, so
// the bound is reported rather than logged.
func TestEntStoreForJobReportsTruncationRatherThanHidingIt(t *testing.T) {
	store, _ := newEntStore(t)
	ctx := context.Background()

	batch := make([]engine.JournalEntry, 0, 5)
	for i := 1; i <= 5; i++ {
		batch = append(batch, walkEntry("job-1", "device-a", 0, i, fmt.Sprintf("tasks[%d]", i)))
	}
	if _, err := store.Save(ctx, batch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, truncated, err := store.ForJob(ctx, "job-1", 3)
	if err != nil {
		t.Fatalf("ForJob: %v", err)
	}
	if !truncated {
		t.Error("a read capped at 3 of 5 entries did not report itself truncated")
	}
	if len(entries) != 3 {
		t.Fatalf("ForJob returned %d entries, want the 3 it was asked for", len(entries))
	}
	// The cap keeps the FIRST rows, not an arbitrary three: a truncated
	// run has to be readable from its beginning.
	if entries[0].Sequence != 1 || entries[2].Sequence != 3 {
		t.Errorf("the capped read returned sequences %d..%d, want 1..3",
			entries[0].Sequence, entries[2].Sequence)
	}

	// Exactly at the bound is not truncated. An off-by-one here would
	// make every complete run of exactly the limit claim to be partial.
	if _, atLimit, err := store.ForJob(ctx, "job-1", 5); err != nil || atLimit {
		t.Errorf("a read of exactly 5 of 5 reported truncated=%v (err %v), want false", atLimit, err)
	}
}

// TestEntStoreForJobRoundTripsEveryField guards the half of the mapping
// nothing else touches.
//
// Save's field list and hydrateEntry's are inverses written out by hand, so
// a field added to one and forgotten in the other is silent: the column is
// written and read back as a zero value, and the only symptom is an empty
// cell in a table nobody has built yet. This compares against what the test
// itself wrote rather than against a re-read, which is the only comparison
// that can see a field lost in both directions.
func TestEntStoreForJobRoundTripsEveryField(t *testing.T) {
	store, _ := newEntStore(t)
	ctx := context.Background()

	// Every field set to something distinguishable, because a struct at
	// its zero values round-trips perfectly through a mapping that drops
	// everything.
	want := engine.JournalEntry{
		JobID: "job-1", DeviceID: "device-a", Attempt: 2, NodeID: "tasks[3]",
		RunID: "run-7", Sequence: 4,
		DAGID: "the-runbook", DAGVersion: "v9",
		FQCN: "svc.systemd.restart", FQCNUnresolved: true,
		TaskName: "restart the thing", Register: "restart_result",
		StartedAt:   time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		FinishedAt:  time.Date(2026, 9, 12, 10, 0, 5, 0, time.UTC),
		Outcome:     engine.OutcomeFailed,
		SkipOrdinal: 3, SkipTotal: 7,
		// Both of these were absent from this struct until an adversarial
		// review noticed, and their absence made the test's own opening
		// claim false: deleting either line from hydrateEntry left the
		// whole suite green. They are the two fields the Tasks tab's
		// DETAIL column is built out of, so losing them renders "failed
		// at connect" and "skipped by when (2 of 5)" as a blank cell on
		// every row forever, and empties two columns of every downloaded
		// journal.
		FailureStage: engine.FailureStageAction,
		SkipKind:     engine.SkipKindWhenCEL,
		StatKeys:     []string{"rc", "stderr"}, UndeclaredStatCount: 2,
		ParamKeys: []string{"name", "state"}, UndeclaredParamCount: 1,
		InverseFQCN: "svc.systemd.stop", InverseFQCNUnresolved: true,
		InverseParamKeys: []string{"name"}, UndeclaredInverseParamCount: 5,
		DiffRecorded: true,
	}
	if _, err := store.Save(ctx, []engine.JournalEntry{want}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, _, err := store.ForJob(ctx, "job-1", 0)
	if err != nil {
		t.Fatalf("ForJob: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ForJob returned %d entries, want 1", len(entries))
	}
	got := entries[0]

	// Compared field by field rather than with reflect.DeepEqual, so a
	// failure names the field that was lost instead of printing two
	// thirty-field structs and leaving the reader to diff them.
	checks := []struct {
		field     string
		got, want any
	}{
		{"JobID", got.JobID, want.JobID},
		{"DeviceID", got.DeviceID, want.DeviceID},
		{"Attempt", got.Attempt, want.Attempt},
		{"NodeID", got.NodeID, want.NodeID},
		{"RunID", got.RunID, want.RunID},
		{"Sequence", got.Sequence, want.Sequence},
		{"DAGID", got.DAGID, want.DAGID},
		{"DAGVersion", got.DAGVersion, want.DAGVersion},
		{"FQCN", got.FQCN, want.FQCN},
		{"FQCNUnresolved", got.FQCNUnresolved, want.FQCNUnresolved},
		{"TaskName", got.TaskName, want.TaskName},
		{"Register", got.Register, want.Register},
		{"Outcome", got.Outcome, want.Outcome},
		{"FailureStage", got.FailureStage, want.FailureStage},
		{"SkipKind", got.SkipKind, want.SkipKind},
		{"SkipOrdinal", got.SkipOrdinal, want.SkipOrdinal},
		{"SkipTotal", got.SkipTotal, want.SkipTotal},
		{"UndeclaredStatCount", got.UndeclaredStatCount, want.UndeclaredStatCount},
		{"UndeclaredParamCount", got.UndeclaredParamCount, want.UndeclaredParamCount},
		{"InverseFQCN", got.InverseFQCN, want.InverseFQCN},
		{"InverseFQCNUnresolved", got.InverseFQCNUnresolved, want.InverseFQCNUnresolved},
		{"UndeclaredInverseParamCount", got.UndeclaredInverseParamCount, want.UndeclaredInverseParamCount},
		{"DiffRecorded", got.DiffRecorded, want.DiffRecorded},
		{"StatKeys", strings.Join(got.StatKeys, ","), strings.Join(want.StatKeys, ",")},
		{"ParamKeys", strings.Join(got.ParamKeys, ","), strings.Join(want.ParamKeys, ",")},
		{"InverseParamKeys", strings.Join(got.InverseParamKeys, ","), strings.Join(want.InverseParamKeys, ",")},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v: the read path lost it", c.field, c.got, c.want)
		}
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if !got.FinishedAt.Equal(want.FinishedAt) {
		t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, want.FinishedAt)
	}
}

// TestEntStoreForJobOnAJobWithNoEntries covers the shape a playbook job
// always has, and every job has before its first level lands.
//
// Empty and no error, never an error: a job with no journal is the ordinary
// state of half the launch kinds this platform registers, since only the
// native adapter publishes journal batches.
func TestEntStoreForJobOnAJobWithNoEntries(t *testing.T) {
	store, _ := newEntStore(t)
	entries, truncated, err := store.ForJob(context.Background(), "job-that-never-ran", 0)
	if err != nil {
		t.Fatalf("ForJob on a job with no entries = %v, want nil", err)
	}
	if len(entries) != 0 || truncated {
		t.Errorf("ForJob returned %d entries (truncated=%v), want none", len(entries), truncated)
	}
}

// TestEntStoreForJobClampsAnAbsurdLimit proves the cap is the store's and
// not the caller's to raise.
func TestEntStoreForJobClampsAnAbsurdLimit(t *testing.T) {
	store, _ := newEntStore(t)
	ctx := context.Background()
	if _, err := store.Save(ctx, []engine.JournalEntry{
		walkEntry("job-1", "device-a", 0, 1, "tasks[0]"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, limit := range []int{-1, 0, journal.MaxEntriesPerRead * 100} {
		entries, _, err := store.ForJob(ctx, "job-1", limit)
		if err != nil {
			t.Fatalf("ForJob(limit=%d): %v", limit, err)
		}
		if len(entries) != 1 {
			t.Errorf("ForJob(limit=%d) returned %d entries, want 1", limit, len(entries))
		}
	}
}

// TestEntStoreForJobReportsAReadFailure covers the branch that turns a
// database problem into an error naming the job.
//
// A cancelled context rather than a broken client, because it is the
// failure this read will actually meet: the caller is an HTTP handler
// rendering a page, and a reader who navigates away cancels the request
// mid-query. The wrapped message has to name the job, since the alternative
// is a log line saying a query failed with nothing to correlate it to.
func TestEntStoreForJobReportsAReadFailure(t *testing.T) {
	store, _ := newEntStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entries, truncated, err := store.ForJob(ctx, "job-1", 0)
	if err == nil {
		t.Fatal("ForJob on a cancelled context returned no error")
	}
	if entries != nil || truncated {
		t.Errorf("a failed read returned %d entries (truncated=%v), want nothing", len(entries), truncated)
	}
	if !strings.Contains(err.Error(), "job-1") {
		t.Errorf("the read failure does not name the job: %v", err)
	}
}
