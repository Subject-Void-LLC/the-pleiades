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
	if err := adapter.Execute(ctx, payload); err != nil {
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
