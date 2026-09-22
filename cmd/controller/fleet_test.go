// Tests for the fleet loop (fleet.go) and `controller migrate --plan`
// (migrateplan.go).
//
// They run against a real, migrated SQLite database opened through the same
// ent.OpenDatabase path the controller takes, and plant heartbeat and history
// rows with raw SQL where no API writes them: a peer that went silent, and a
// migration numbered past this build's newest, as a newer build would leave.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// openMigrated opens a real, migrated SQLite database for these tests, and
// returns its DSN too.
func openMigrated(t *testing.T) (*ent.Client, string) {
	t.Helper()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "controller.db")
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: dsn, Schema: ent.SchemaServe})
	if err != nil {
		t.Fatalf("OpenDatabase(): %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, dsn
}

// rawSQLite opens the same file directly, for planting rows no API writes.
func rawSQLite(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+strings.TrimPrefix(dsn, "sqlite://")+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// nextMigration names a migration numbered one past this build's newest,
// which is the only shape a newer build's migration can take.
func nextMigration(t *testing.T, client *ent.Client) string {
	t.Helper()
	plan, err := client.InspectSchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := fmt.Sscanf(plan.BuildHead, "%04d_", &n); err != nil {
		t.Fatalf("reading the head %q: %v", plan.BuildHead, err)
	}
	return fmt.Sprintf("%04d_from_a_newer_build.sql", n+1)
}

// quietLogger discards everything.
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestFleet_KnowsWhoIsAlive proves the heartbeat registers this controller,
// counts a recently seen peer as alive and a silent one as gone, and always
// counts itself.
func TestFleet_KnowsWhoIsAlive(t *testing.T) {
	client, dsn := openMigrated(t)
	ctx := context.Background()
	members, err := joinFleet(ctx, client, quietLogger())
	if err != nil {
		t.Fatalf("joinFleet(): %v", err)
	}
	if err := client.Heartbeat(ctx, ent.InstanceBeat{InstanceID: "peer-live", Version: "x", MigrationHead: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Heartbeat(ctx, ent.InstanceBeat{InstanceID: "peer-gone", Version: "x", MigrationHead: "y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rawSQLite(t, dsn).Exec(`UPDATE controller_instances SET last_seen_unix = last_seen_unix - 600 WHERE instance_id = 'peer-gone'`); err != nil {
		t.Fatal(err)
	}

	ids, known := members.alive(ctx)
	if !known {
		t.Fatal("alive() could not read a heartbeat table that exists")
	}
	alive := strings.Join(ids, ",")
	if !strings.Contains(alive, members.id()) || !strings.Contains(alive, "peer-live") || strings.Contains(alive, "peer-gone") {
		t.Errorf("alive = %s; want this controller and peer-live, not peer-gone", alive)
	}

	// A tick sweeps syncs with the same answer.
	var swept []string
	members.recoverSyncs = func(_ context.Context, ids []string) { swept = ids }
	members.tick(ctx)
	if len(swept) != 2 {
		t.Errorf("the sweep was told %v is alive; want this controller and peer-live", swept)
	}

	members.leave(ctx)
	instances, err := client.Instances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range instances {
		if i.InstanceID == members.id() {
			t.Error("this controller's heartbeat outlived leave()")
		}
	}
}

// TestFleet_SweepsNothingWhenItCannotTellWhoIsAlive is FAILURE_PATTERNS.md
// #282: a heartbeat table that cannot be read must stop the sync sweep, not
// hand it a list naming this controller alone, which fails every live peer's
// in-flight sync. The control half proves the sweep does run once the table
// is readable, so the refusal is not the sweep being broken.
func TestFleet_SweepsNothingWhenItCannotTellWhoIsAlive(t *testing.T) {
	client, dsn := openMigrated(t)
	ctx := context.Background()
	members, err := joinFleet(ctx, client, quietLogger())
	if err != nil {
		t.Fatalf("joinFleet(): %v", err)
	}
	calls := 0
	members.recoverSyncs = func(context.Context, []string) { calls++ }

	// Control: a readable table, one sweep.
	members.sweep(ctx)
	if calls != 1 {
		t.Fatalf("with a readable heartbeat table the sweep ran %d times; want 1", calls)
	}

	// Make only the heartbeat read fail: the schema check and every other
	// table still work, as they would through a single failed query.
	if _, err := rawSQLite(t, dsn).Exec(`ALTER TABLE controller_instances RENAME TO controller_instances_away`); err != nil {
		t.Fatal(err)
	}
	if _, known := members.alive(ctx); known {
		t.Fatal("alive() claimed to know who is alive with no heartbeat table")
	}
	members.sweep(ctx)
	members.tick(ctx)
	if members.refused.Load() {
		t.Fatal("tick refused the schema, so it never reached the sweep; this test proves nothing about the tick")
	}
	if calls != 1 {
		t.Errorf("the sweep ran %d times after the heartbeat read failed; want none, since no owner can be proven gone", calls-1)
	}
}

// TestFleet_StopsWhenTheDatabaseMovesPastIt is the runtime half of the
// compatibility window: a newer build contracting the schema past this one
// while it runs makes this controller fail readiness and ask to stop.
func TestFleet_StopsWhenTheDatabaseMovesPastIt(t *testing.T) {
	client, dsn := openMigrated(t)
	ctx := context.Background()
	members, err := joinFleet(ctx, client, quietLogger())
	if err != nil {
		t.Fatalf("joinFleet(): %v", err)
	}
	check := members.schemaCheck()

	// Within the window first: still serving.
	raw := rawSQLite(t, dsn)
	newer := nextMigration(t, client)
	if _, err := raw.Exec(`INSERT INTO schema_migrations (version, applied_at, compatible_from) VALUES (?, ?, ?)`,
		newer, time.Now().UTC(), members.beat.MigrationHead); err != nil {
		t.Fatal(err)
	}
	members.tick(ctx)
	select {
	case reason := <-members.stop:
		t.Fatalf("stopped within the window: %s", reason)
	default:
	}
	if err := check.Probe(ctx); err != nil {
		t.Fatalf("readiness within the window = %v", err)
	}

	// Then a contract past it.
	if _, err := raw.Exec(`UPDATE schema_migrations SET compatible_from = version WHERE version = ?`, newer); err != nil {
		t.Fatal(err)
	}
	members.tick(ctx)
	select {
	case reason := <-members.stop:
		if !strings.Contains(reason, newer) {
			t.Errorf("stop reason = %q; want it to name the migration", reason)
		}
	default:
		t.Fatal("the controller kept serving a database contracted past it")
	}
	if err := check.Probe(ctx); err == nil {
		t.Error("readiness stayed green on a database this build cannot serve")
	}

	// Asked once, not once per tick.
	members.tick(ctx)
	select {
	case <-members.stop:
		t.Error("a second stop was sent")
	default:
	}
}

// TestRunMigrate_ReportsEveryVerdict drives `controller migrate --plan`
// against real SQLite databases in each state, through both views, and checks
// the exit code a script would act on.
func TestRunMigrate_ReportsEveryVerdict(t *testing.T) {
	run := func(t *testing.T, dsn string, args ...string) (int, string) {
		t.Helper()
		t.Setenv("DB_DSN", dsn)
		t.Setenv("DB_PATH", "")
		var out, errOut strings.Builder
		code := runMigrate(append([]string{migrateCommand}, args...), &out, &errOut)
		return code, out.String() + errOut.String()
	}

	t.Run("a database that does not exist is fresh", func(t *testing.T) {
		code, out := run(t, "sqlite://"+filepath.Join(t.TempDir(), "none.db"), "--plan", "--json")
		var report planReport
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if code != planExitNothingToDo || report.Plan.Verdict != "fresh" || report.Controllers != nil || report.SchemaVersion != planReportVersion {
			t.Errorf("exit %d, %+v; want 0, fresh, controllers null", code, report)
		}
	})

	client, dsn := openMigrated(t)
	t.Run("a migrated database is current, and lists its controllers", func(t *testing.T) {
		if err := client.Heartbeat(context.Background(), ent.InstanceBeat{InstanceID: "abc", Version: "1.0.0", MigrationHead: "h", Host: "pod\x1b[2J"}); err != nil {
			t.Fatal(err)
		}
		code, out := run(t, dsn, "--plan")
		if code != planExitNothingToDo || !strings.Contains(out, "current") || !strings.Contains(out, "abc") {
			t.Errorf("exit %d:\n%s\nwant 0, current, and the controller listed", code, out)
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("a controller's host reached the terminal unescaped:\n%q", out)
		}
	})

	raw := rawSQLite(t, dsn)
	t.Run("a database missing this build's newest migration is pending", func(t *testing.T) {
		var newest string
		if err := raw.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&newest); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`DELETE FROM schema_migrations WHERE version = ?`, newest); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = raw.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, newest, time.Now().UTC())
		}()
		if code, out := run(t, dsn, "--plan"); code != planExitPending || !strings.Contains(out, "Take a backup") {
			t.Errorf("exit %d:\n%s\nwant 3 and the backup advice", code, out)
		}
	})

	t.Run("a newer database within the window, then past it", func(t *testing.T) {
		var head string
		if err := raw.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&head); err != nil {
			t.Fatal(err)
		}
		newer := nextMigration(t, client)
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version, applied_at, compatible_from) VALUES (?, ?, ?)`, newer, time.Now().UTC(), head); err != nil {
			t.Fatal(err)
		}
		if code, out := run(t, dsn, "--plan"); code != planExitNewer {
			t.Errorf("within the window: exit %d; want 4:\n%s", code, out)
		}
		if _, err := raw.Exec(`UPDATE schema_migrations SET compatible_from = version WHERE version = ?`, newer); err != nil {
			t.Fatal(err)
		}
		if code, out := run(t, dsn, "--plan"); code != planExitRefused || !strings.Contains(out, "refused") {
			t.Errorf("past the window: exit %d:\n%s\nwant 1 and refused", code, out)
		}
	})

	t.Run("there is no form that migrates", func(t *testing.T) {
		if code, _ := run(t, dsn); code != planExitUsage {
			t.Errorf("controller migrate with no --plan = %d; want a usage error", code)
		}
	})
}
