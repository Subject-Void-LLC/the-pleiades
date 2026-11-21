// Tests that the store's reads of the facts table use an index on SQLite:
// the statements Current, History, Verify and Record really send are
// captured through a recording ent driver over a database built by the real
// versioned migrations, and each one that reads the facts table is held to
// its EXPLAIN QUERY PLAN, which must search the table through an index and
// never scan it.
package entstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// statement is one query a driver was handed, with its arguments.
type statement struct {
	query string
	args  []any
}

// recorder keeps every statement the drivers it wraps send.
type recorder struct {
	mu    sync.Mutex
	stmts []statement
}

// keep records query and its arguments, when they are the []any every ent
// builder hands a driver.
func (r *recorder) keep(query string, args any) {
	list, _ := args.([]any)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, statement{query: query, args: list})
}

// take returns what was recorded since the last take, and forgets it.
func (r *recorder) take() []statement {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.stmts
	r.stmts = nil
	return out
}

// recordingDriver is a dialect.Driver that records every statement before
// handing it to the real driver, inside a transaction too.
type recordingDriver struct {
	dialect.Driver
	rec *recorder
}

// Exec records the statement and runs it.
func (d recordingDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.rec.keep(query, args)
	return d.Driver.Exec(ctx, query, args, v)
}

// Query records the statement and runs it.
func (d recordingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.rec.keep(query, args)
	return d.Driver.Query(ctx, query, args, v)
}

// Tx starts a real transaction whose statements are recorded too.
func (d recordingDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return recordingTx{Tx: tx, rec: d.rec}, nil
}

// recordingTx is a dialect.Tx that records every statement it runs.
type recordingTx struct {
	dialect.Tx
	rec *recorder
}

// Exec records the statement and runs it.
func (t recordingTx) Exec(ctx context.Context, query string, args, v any) error {
	t.rec.keep(query, args)
	return t.Tx.Exec(ctx, query, args, v)
}

// Query records the statement and runs it.
func (t recordingTx) Query(ctx context.Context, query string, args, v any) error {
	t.rec.keep(query, args)
	return t.Tx.Query(ctx, query, args, v)
}

// readsFacts reports whether query reads the facts table itself, not
// fact_observations or another table whose name starts the same way.
func readsFacts(query string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.ToUpper(query)), "SELECT") &&
		(strings.Contains(query, "FROM `facts`") || strings.Contains(query, `FROM "facts"`))
}

// queryPlan is SQLite's plan for s, one detail line per step.
func queryPlan(t *testing.T, db *sql.DB, s statement) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+s.query, s.args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %s: %v", s.query, err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// checkFactPlans holds every statement among stmts that reads the facts
// table to its plan: a step on facts must search it through an index, and
// none may scan it. It logs each plan, and fails when op sent no such
// statement, since then nothing was checked.
func checkFactPlans(t *testing.T, db *sql.DB, op string, stmts []statement) {
	t.Helper()
	checked := 0
	for _, s := range stmts {
		if !readsFacts(s.query) {
			continue
		}
		checked++
		plan := queryPlan(t, db, s)
		t.Logf("%s: %s\n    plan: %s", op, s.query, strings.Join(plan, " | "))
		searched := false
		for _, step := range plan {
			if strings.HasPrefix(step, "SCAN facts") {
				t.Errorf("%s scans the facts table (%q) for %s", op, step, s.query)
			}
			if strings.HasPrefix(step, "SEARCH facts USING ") && strings.Contains(step, "INDEX ") {
				searched = true
			}
		}
		if !searched {
			t.Errorf("%s reads the facts table through no index: plan %q for %s", op, plan, s.query)
		}
	}
	if checked == 0 {
		t.Errorf("%s sent no statement reading the facts table, so nothing was checked", op)
	}
}

// TestQueryPlans_FactReadsUseAnIndexAndScanNoTable: over a database built by
// the real SQLite migrations, holding three devices' facts, every statement
// Current, History, Verify and Record send that reads the facts table
// searches it through an index and never scans it.
func TestQueryPlans_FactReadsUseAnIndexAndScanNoTable(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.Apply(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("applying the real migrations: %v", err)
	}
	rec := &recorder{}
	client := ent.NewClient(ent.Driver(recordingDriver{Driver: entsql.OpenDB(dialect.SQLite, db), rec: rec}))
	store := entstore.New(client, factstest.Keys(), factstest.Envelope())

	// Three devices, each with a few stable facts and a volatile one,
	// recorded twice so each has a history and a chain.
	var devices []string
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for d := range 3 {
		dev := client.Device.Create().SetName(fmt.Sprintf("web%d", d)).SetType("linux_server").
			SetProperties(map[string]any{"host": fmt.Sprintf("10.0.0.%d", d+1)}).SaveX(ctx)
		devices = append(devices, dev.DeviceID)
		for run := range 2 {
			entries, _, err := facts.Prepare(map[string]any{
				"os.kernel.release":      fmt.Sprintf("6.%d", run),
				"package.openssh-server": map[string]any{"version": fmt.Sprintf("9.%d", run)},
				"host.name":              fmt.Sprintf("web%d", d),
			}, facts.Guard{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Record(ctx, dev.DeviceID, facts.Observation{Entries: entries, At: at.Add(time.Duration(run) * time.Minute)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The write path's reads, from one more observation of a device that
	// already holds rows: its rows by token (store.go), its chain head, and
	// its key state (sealed.go).
	rec.take()
	entries, _, err := facts.Prepare(map[string]any{"os.kernel.release": "6.9", "host.name": "web1"}, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, devices[1], facts.Observation{Entries: entries, At: at.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	checkFactPlans(t, db, "Record", rec.take())

	if got, err := store.Current(ctx, devices[1]); err != nil || len(got) != 3 {
		t.Fatalf("Current = %d facts, %v; want 3", len(got), err)
	}
	checkFactPlans(t, db, "Current", rec.take())

	if got, err := store.History(ctx, devices[1], "os.kernel.release"); err != nil || len(got) != 3 {
		t.Fatalf("History = %d values, %v; want 3", len(got), err)
	}
	checkFactPlans(t, db, "History", rec.take())

	if verdict, err := store.Verify(ctx, devices[1]); err != nil || verdict.Break != nil {
		t.Fatalf("Verify = %+v, %v; want an unbroken chain", verdict, err)
	}
	checkFactPlans(t, db, "Verify", rec.take())
}
