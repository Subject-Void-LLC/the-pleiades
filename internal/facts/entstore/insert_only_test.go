// The fact history is insert-only (Phase 41h): proven by the database itself,
// which a trigger makes refuse any UPDATE of a stored fact and any DELETE of a
// stable one, on SQLite and on PostgreSQL through the real migrations.
package entstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// sqliteRefusals make SQLite refuse rewriting the history.
var sqliteRefusals = []string{
	"CREATE TRIGGER facts_never_updated BEFORE UPDATE ON facts BEGIN SELECT RAISE(ABORT, 'a stored fact was updated'); END",
	"CREATE TRIGGER facts_history_kept BEFORE DELETE ON facts WHEN OLD.kind = 'stable' BEGIN SELECT RAISE(ABORT, 'a stored value was deleted'); END",
}

// postgresRefusals make PostgreSQL refuse the same.
var postgresRefusals = []string{
	"CREATE FUNCTION refuse_history_edit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'a stored fact got a %', TG_OP; END $$",
	"CREATE TRIGGER facts_never_updated BEFORE UPDATE ON facts FOR EACH ROW EXECUTE FUNCTION refuse_history_edit()",
	"CREATE TRIGGER facts_history_kept BEFORE DELETE ON facts FOR EACH ROW WHEN (OLD.kind = 'stable') EXECUTE FUNCTION refuse_history_edit()",
}

// gatherAgainAndAgain records a host's gather, makes the database refuse any
// edit of what it stored, and gathers again: an unchanged gather, then one
// that changes one value and one volatile value. Each must store, and the
// reads must say each value was last confirmed by the gather after it.
func gatherAgainAndAgain(t *testing.T, client *ent.Client, device string, db *sql.DB, refusals []string) {
	t.Helper()
	ctx := context.Background()
	store := entstore.New(client)
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	gather := func(n int, kernel string, uptime int) {
		t.Helper()
		entries := prepared(t, map[string]any{"os.kernel": kernel, "os.family": "debian"})
		up := prepared(t, map[string]any{"host.uptime_seconds": uptime})
		up[0].Kind = facts.KindVolatile
		by := facts.Provenance{Run: fmt.Sprintf("run%d", n), Order: facts.Order{Source: facts.ClockSource, Token: uint64(n)}}
		if err := store.Record(ctx, device, facts.Observation{Entries: append(entries, up...), At: t0.Add(time.Duration(n) * time.Hour), By: by}); err != nil {
			t.Fatalf("gather %d: %v", n, err)
		}
	}
	gather(1, "6.1", 100)
	for _, stmt := range refusals {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// The same values again: before 41h, each moved its row's last_seen_at.
	gather(2, "6.1", 100)
	// A change, and a volatile value replaced.
	gather(3, "6.2", 200)
	current, err := store.Current(ctx, device)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"os.family": "run3", "os.kernel": "run3", "host.uptime_seconds": "run3"}
	for _, e := range current {
		if e.LastBy.Run != want[e.Key] {
			t.Errorf("%s last confirmed by %q, want %q", e.Key, e.LastBy.Run, want[e.Key])
		}
	}
	family, err := store.History(ctx, device, "os.family")
	if err != nil || len(family) != 1 || family[0].By.Run != "run1" || !family[0].LastSeenAt.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("History(os.family) = %+v, %v; want one value, first seen by run1 and last at run3's time", family, err)
	}
	kernel, err := store.History(ctx, device, "os.kernel")
	if err != nil || len(kernel) != 2 || kernel[0].LastBy.Run != "run2" {
		t.Fatalf("History(os.kernel) = %+v, %v; want 6.1 last confirmed by run2, then 6.2", kernel, err)
	}
}

// TestStore_AGatherUpdatesNoStoredFact: on SQLite, a database that refuses
// every UPDATE of a stored fact still takes every gather.
func TestStore_AGatherUpdatesNoStoredFact(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "facts.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gatherAgainAndAgain(t, client, dev.DeviceID, db, sqliteRefusals)
}

// TestStore_AGatherUpdatesNoStoredFactOnPostgres: the same on PostgreSQL,
// where an UPDATE leaves a dead row version for vacuum, through the
// migrations a Controller applies. A volatile value that changes is still a
// DELETE and an INSERT, by design (one value kept), and the trigger allows it:
// this removes the per-gather updates of unchanged values, not every dead row.
func TestStore_AGatherUpdatesNoStoredFactOnPostgres(t *testing.T) {
	client, device, db := postgresClientDB(t)
	gatherAgainAndAgain(t, client, device, db, postgresRefusals)
}
