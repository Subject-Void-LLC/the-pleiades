// Package native: the Walk tier's journal, end to end in one process.
//
// Everything else tests one half. This drives the real Adapter.Execute
// through a real event bus into the real Controller-side subscriber and
// a real database, because the risk the two halves carry is precisely
// that they agree with their own tests and not with each other: the
// publisher marshals and the subscriber unmarshals, and a field that
// only one of them names is invisible to both.
//
// It is not a substitute for the real-NATS gate. It proves the format
// and the wiring; it cannot prove anything about redelivery, mesh
// permissions, or a broker at all.
package native

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestJournalRoundTripFromRunnerToDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "journal.db")
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", dbPath))
	t.Cleanup(func() { _ = client.Close() })

	bus := event.NewInProcessBus()
	t.Cleanup(func() { _ = bus.Close() })

	ctx := journal.WithAttempt(context.Background(), 2)
	subscriber := journal.NewSubscriber(journal.NewEntStore(client), quietLogger())
	if err := subscriber.Subscribe(ctx, bus); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	runbooks := writeRunbook(t, "pb-1",
		"id: pb-1\ntasks:\n  - name: first\n    fqcn: noop\n    params:\n      changed: true\n"+
			"  - name: second\n    fqcn: noop\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	payload := wire.DispatchPayload{
		JobID: "11111111-2222-3333-4444-555555555555", RunbookID: "pb-1",
		DeviceID: "device-1", DeviceName: "router1", DeviceHost: "10.0.0.1",
	}
	if _, err := adapter.Execute(ctx, payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rows := waitForJournalRows(t, dbPath, 2)

	// The format assertion. Every one of these values crossed a JSON
	// boundary between two packages, so a field either side spelled
	// differently would arrive empty here rather than fail to compile.
	for _, row := range rows {
		if row["job_id"] != payload.JobID {
			t.Errorf("row job_id = %q, want %q", row["job_id"], payload.JobID)
		}
		if row["device_id"] != "device-1" {
			t.Errorf("row device_id = %q, want device-1", row["device_id"])
		}
		if row["attempt"] != "2" {
			t.Errorf("row attempt = %q, want the 2 the delivery set", row["attempt"])
		}
		if row["run_id"] == "" {
			t.Error("row carries no run id, so the entries cannot be grouped by run")
		}
		if row["fqcn"] != "noop" {
			t.Errorf("row fqcn = %q, want noop", row["fqcn"])
		}
	}
	if rows[0]["task_name"] != "first" || rows[1]["task_name"] != "second" {
		t.Errorf("task names arrived as %q and %q, want first and second",
			rows[0]["task_name"], rows[1]["task_name"])
	}
	if rows[0]["outcome"] != "changed" {
		t.Errorf("the changed task recorded outcome %q, want changed", rows[0]["outcome"])
	}
	if rows[0]["sequence"] != "1" || rows[1]["sequence"] != "2" {
		t.Errorf("sequences arrived as %q and %q, want 1 and 2", rows[0]["sequence"], rows[1]["sequence"])
	}
}

// waitForJournalRows polls the database directly until it holds want
// rows, then returns them ordered by sequence.
//
// Read with database/sql rather than through the ent client, so the
// assertion is about what is stored and not about a round trip through
// the same code that stored it. The in-process bus runs each handler on
// its own goroutine, so a publish returns before the handler has run.
func waitForJournalRows(t *testing.T, path string, want int) []map[string]string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows := readJournalRows(t, path)
		if len(rows) >= want {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("the database holds %d journal rows after ten seconds, want %d", len(rows), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readJournalRows returns every journal row as a column-name to text map.
func readJournalRows(t *testing.T, path string) []map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("opening the database directly: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(context.Background(), "SELECT * FROM journal_entries ORDER BY sequence")
	if err != nil {
		t.Fatalf("selecting the journal: %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading the column list: %v", err)
	}

	var out []map[string]string
	for rows.Next() {
		cells := make([]interface{}, len(columns))
		for i := range cells {
			cells[i] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		row := make(map[string]string, len(columns))
		for i, cell := range cells {
			row[columns[i]] = cell.(*sql.NullString).String
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	return out
}

// TestJournalRoundTripKeepsEveryDispatchsCopyOfADevicelessNode is the
// regression test for a silent loss found by running a real two-device
// job rather than by any test.
//
// A node that resolves no device (a skipped task, a controller-side
// task, or the synthetic parallel marker) left DeviceID empty. The store
// identifies a row by (job, device, attempt, node), so each dispatch of
// one job produced the identical key for that node and every copy after
// the first was discarded as already recorded. A two-device job with one
// skipped task stored one skip row rather than two, with no error and no
// log line, and it did so only when both dispatches happened to land on
// the same attempt number, which made the audit record's completeness
// depend on whether JetStream had redelivered.
//
// Two dispatches of one job, same attempt, different devices, which is
// exactly what a fan-out to two devices is.
func TestJournalRoundTripKeepsEveryDispatchsCopyOfADevicelessNode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "journal.db")
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", dbPath))
	t.Cleanup(func() { _ = client.Close() })

	bus := event.NewInProcessBus()
	t.Cleanup(func() { _ = bus.Close() })

	ctx := journal.WithAttempt(context.Background(), 1)
	subscriber := journal.NewSubscriber(journal.NewEntStore(client), quietLogger())
	if err := subscriber.Subscribe(ctx, bus); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// The middle task is skipped on every device, which is what gives the
	// run a node with no device of its own.
	runbooks := writeRunbook(t, "pb-2",
		"id: pb-2\ntasks:\n  - name: first\n    fqcn: noop\n"+
			"  - name: skipped everywhere\n    fqcn: noop\n    when: \"1 == 2\"\n"+
			"  - name: third\n    fqcn: noop\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	const jobID = "66666666-7777-8888-9999-000000000000"
	for _, device := range []string{"device-1", "device-2"} {
		payload := wire.DispatchPayload{
			JobID: jobID, RunbookID: "pb-2",
			DeviceID: device, DeviceName: device, DeviceHost: "10.0.0.1",
		}
		if _, err := adapter.Execute(ctx, payload); err != nil {
			t.Fatalf("Execute for %s: %v", device, err)
		}
	}

	// Three nodes per dispatch, two dispatches. Before the fix this
	// timed out at five.
	rows := waitForJournalRows(t, dbPath, 6)
	if len(rows) != 6 {
		t.Fatalf("the journal holds %d rows for a two-device job of three nodes, want 6", len(rows))
	}

	// The half that matters: the skipped node is recorded once per
	// device, and each row names the device whose dispatch skipped it.
	// Without that, "was this task skipped on device-2" is a question the
	// durable record cannot answer.
	skippedBy := make(map[string]int, 2)
	for _, row := range rows {
		if row["outcome"] != "skipped" {
			continue
		}
		if row["device_id"] == "" {
			t.Error("a skipped node recorded no device, so two dispatches of this job collide on one row")
		}
		skippedBy[row["device_id"]]++
	}
	for _, device := range []string{"device-1", "device-2"} {
		if skippedBy[device] != 1 {
			t.Errorf("device %s has %d skip rows, want exactly 1: %v", device, skippedBy[device], skippedBy)
		}
	}
}
