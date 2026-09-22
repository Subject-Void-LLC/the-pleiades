// These tests cover schema_migrations itself, against real databases of both
// dialects: a table without the key concurrency depends on, a row without a
// version, a table written by a build that predates the compatibility floor,
// and the floor this build records.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// preFloorInsert records a version the way every build before the floor
// column did, with a dialect's own placeholder spelling.
var preFloorInsert = map[string]string{
	"sqlite3":  `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
	"postgres": `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`,
}

// TestApply_RefusesAHistoryTableWithoutItsKey proves a schema_migrations whose
// version is not its primary key is refused before anything is applied.
//
// The key is what makes a second starter's claim on a migration wait and then
// fail. A table rebuilt without it would let two controllers run one
// migration twice, and nothing else would notice until they had.
func TestApply_RefusesAHistoryTableWithoutItsKey(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db := freshDB(t, dialectName)
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT, applied_at TIMESTAMP NOT NULL)`); err != nil {
			t.Fatalf("creating a keyless history table: %v", err)
		}

		err := Apply(ctx, dialectName, db)
		if err == nil || !strings.Contains(err.Error(), "primary key") {
			t.Fatalf("Apply() on a keyless history = %v; want a refusal naming the primary key", err)
		}
		if tableExists(t, db, "devices") {
			t.Error("a migration ran against a history table that cannot keep two starters apart")
		}
	})
}

// TestApply_RefusesARowWithoutAVersion covers a quirk of SQLite that makes
// the refusal reachable at all: a TEXT PRIMARY KEY column accepts NULL there,
// for backward compatibility with its own early versions. A history row with
// no version cannot come from any migration runner.
func TestApply_RefusesARowWithoutAVersion(t *testing.T) {
	db := freshDB(t, "sqlite3")
	ctx := context.Background()
	if err := Apply(ctx, "sqlite3", db); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (NULL, ?)`, time.Now().UTC()); err != nil {
		t.Fatalf("planting a row with no version: %v", err)
	}

	err := Apply(ctx, "sqlite3", db)
	if err == nil || !strings.Contains(err.Error(), "no version") {
		t.Fatalf("Apply() with a versionless row = %v; want a refusal", err)
	}
}

// TestApply_AddsTheFloorToAHistoryAnOlderBuildWrote proves the upgrade path
// for schema_migrations itself: a table created and filled by a build from
// before the floor column gains the column, keeps every row, and still
// accepts the older build's own two-column insert afterwards, which is what a
// previous build still running during a rolling upgrade sends.
func TestApply_AddsTheFloorToAHistoryAnOlderBuildWrote(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db := freshDB(t, dialectName)
		ctx := context.Background()
		names := namesOf(t, dialectName)

		// Exactly what an older build did: the same CREATE, and two-column
		// rows for the first migration, applied by hand from its own file.
		if _, err := db.ExecContext(ctx, createVersionTable); err != nil {
			t.Fatalf("creating the history as an older build did: %v", err)
		}
		src := migrationSources[dialectName]
		script := readMigration(t, src, names[0])
		if _, err := db.ExecContext(ctx, script); err != nil {
			t.Fatalf("applying %s as an older build did: %v", names[0], err)
		}
		if _, err := db.ExecContext(ctx, preFloorInsert[dialectName], names[0], time.Now().UTC()); err != nil {
			t.Fatalf("recording %s as an older build did: %v", names[0], err)
		}

		out, err := ApplyWith(ctx, dialectName, db, Options{})
		if err != nil {
			t.Fatalf("ApplyWith(): %v", err)
		}
		if len(out.Applied) != len(names)-1 {
			t.Fatalf("applied %d migrations over the older history; want %d", len(out.Applied), len(names)-1)
		}

		history := historyOf(t, db, dialectName)
		if len(history) != len(names) {
			t.Fatalf("history holds %d rows; want %d", len(history), len(names))
		}
		for _, r := range history {
			switch {
			case r.version == names[0] && r.floor.Valid:
				t.Errorf("the older build's row gained a floor %q; nothing may be invented for it", r.floor.String)
			case r.version != names[0] && r.floor.String != floorAfter(dialectName, names, r.version):
				t.Errorf("row %s records floor %q; want %q", r.version, r.floor.String, floorAfter(dialectName, names, r.version))
			}
		}

		// The older build's own insert still works against the new table.
		if _, err := db.ExecContext(ctx, preFloorInsert[dialectName], "9998_older_build_probe.sql", time.Now().UTC()); err != nil {
			t.Fatalf("an older build's two-column insert failed after the floor was added: %v", err)
		}
	})
}

// TestApplyWith_ServesANewerDatabaseOnlyWithinTheWindow drives the relaxed
// gate end to end against real databases: the same history is refused
// strictly, served within the window, and refused within the window once its
// floor passes this build.
func TestApplyWith_ServesANewerDatabaseOnlyWithinTheWindow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db := freshDB(t, dialectName)
		ctx := context.Background()
		names := namesOf(t, dialectName)
		if err := Apply(ctx, dialectName, db); err != nil {
			t.Fatalf("Apply(): %v", err)
		}
		head := names[len(names)-1]
		next := nextName(t, head, "from_a_newer_build")
		insert := migrationSources[dialectName].insertVersion
		if _, err := db.ExecContext(ctx, insert, next, time.Now().UTC(), head); err != nil {
			t.Fatalf("recording a newer build's migration: %v", err)
		}

		if err := Apply(ctx, dialectName, db); err == nil {
			t.Error("the strict gate served a database a newer build migrated")
		}
		out, err := ApplyWith(ctx, dialectName, db, Options{AllowNewerWithinWindow: true})
		if err != nil {
			t.Fatalf("ApplyWith(within window) = %v; want the newer database served", err)
		}
		if len(out.Newer) != 1 || out.Newer[0] != next || out.Floor != head || len(out.Applied) != 0 {
			t.Errorf("outcome = %+v; want only %s reported as newer, floor %s, nothing applied", out, next, head)
		}

		// A contract past this build: the newer build raised the floor to
		// its own migration.
		if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET compatible_from = version WHERE version = '`+next+`'`); err != nil {
			t.Fatalf("raising the floor: %v", err)
		}
		if _, err := ApplyWith(ctx, dialectName, db, Options{AllowNewerWithinWindow: true}); err == nil {
			t.Error("a database whose floor is past this build was served")
		}
	})
}

// TestApply_RecordsTheFloorOnEveryMigration proves a fresh database records,
// on every row, the floor compat.go computes for it.
func TestApply_RecordsTheFloorOnEveryMigration(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db := freshDB(t, dialectName)
		names := namesOf(t, dialectName)
		if err := Apply(context.Background(), dialectName, db); err != nil {
			t.Fatalf("Apply(): %v", err)
		}
		for _, r := range historyOf(t, db, dialectName) {
			if want := floorAfter(dialectName, names, r.version); r.floor.String != want {
				t.Errorf("%s records floor %q; want %q", r.version, r.floor.String, want)
			}
		}
	})
}

// readMigration reads one embedded migration's script.
func readMigration(t testing.TB, src migrationSource, name string) string {
	t.Helper()
	script, err := src.fsys.ReadFile(src.dir + "/" + name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(script)
}

// nextName returns a migration name numbered one past name.
func nextName(t testing.TB, name, suffix string) string {
	t.Helper()
	n, err := versionNumber(name)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return fmt.Sprintf("%04d_%s.sql", n+1, suffix)
}

// tableExists reports whether a table of that name can be read in db.
func tableExists(t testing.TB, db *sql.DB, table string) bool {
	t.Helper()
	// The name is a constant in every caller.
	rows, err := db.QueryContext(context.Background(), "SELECT 1 FROM "+table+" WHERE 1 = 0")
	if err != nil {
		return false
	}
	_ = rows.Close()
	return true
}
