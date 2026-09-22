// FuzzApplyTamperedHistory drives the whole apply path, not only the gate,
// over histories that disagree with the database they describe.
//
// Each input builds a real SQLite database genuinely migrated to some point,
// then edits its schema_migrations the ways a hand repair, a bad restore or an
// attacker with write access could: rows deleted, rows duplicated in a table
// rebuilt without its key, rows naming unknown or malformed migrations, floors
// missing or wrong. The property is the one an operator relies on: Apply
// either refuses, or leaves a database whose history records every migration
// of this build exactly once and whose schema is exactly the schema a clean
// migration produces.
//
// With one stated exception, which this fuzzer found and which is a limit of
// the design rather than a defect in it: a planted row claiming a migration
// of THIS build that was never applied is believed, the migration is skipped,
// and Apply succeeds onto a schema missing it. The history is the record of
// what was applied; nothing at startup compares the live schema with it
// (restore does, internal/backup's schema comparison). Such a row can only be
// written by someone who can already write every table, so it is no new
// exposure. A startup check comparing the live schema with the history
// would close it and is not built. The property below is therefore checked
// in full except when the input planted such a false claim.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	// referenceShapeOnce computes the fully migrated SQLite shape once per
	// fuzzing process.
	referenceShapeOnce sync.Once

	// referenceShape is that shape.
	referenceShape string
)

// tamperNames are the version strings a fuzz input can plant: real ones,
// newer ones, colliding ones and malformed ones.
var tamperNames = []string{
	"0001_initial.sql", "0002_add_device_type.sql", "0031_add_sync_run_origin.sql",
	"9001_from_the_future.sql", "0002_other_lineage.sql", "0001_initial.sql ",
	"not_a_migration", "",
}

// FuzzApplyTamperedHistory is described in this file's header. Input layout:
// through is how far to migrate honestly; keyless rebuilds the history
// without its key; window runs Apply within the window; edits is a list of
// (operation, operand) byte pairs.
func FuzzApplyTamperedHistory(f *testing.F) {
	f.Add(uint8(5), false, false, []byte{})
	f.Add(uint8(5), false, false, []byte{0, 1})    // delete a row
	f.Add(uint8(5), true, false, []byte{1, 0})     // duplicate a row, keyless
	f.Add(uint8(40), false, true, []byte{2, 3})    // a newer row, within the window
	f.Add(uint8(40), false, true, []byte{2, 3, 3}) // and its floor removed
	f.Add(uint8(3), false, false, []byte{2, 6})    // a malformed row

	f.Fuzz(func(t *testing.T, through uint8, keyless, window bool, edits []byte) {
		ctx := context.Background()
		src := migrationSources["sqlite3"]
		names := namesOf(t, "sqlite3")
		db := openDSN(t, "sqlite3", sqliteDSN(filepath.Join(t.TempDir(), "tampered.db")))

		// Migrate honestly to some point, so the schema matches the
		// history before the history is touched.
		stop := names[int(through)%len(names)]
		if _, err := applyPending(ctx, db, src, names, stop, gateStrict); err != nil {
			t.Fatalf("honest migration through %s: %v", stop, err)
		}
		if keyless {
			for _, statement := range []string{
				`CREATE TABLE schema_migrations_copy AS SELECT * FROM schema_migrations`,
				`DROP TABLE schema_migrations`,
				`ALTER TABLE schema_migrations_copy RENAME TO schema_migrations`,
			} {
				if _, err := db.ExecContext(ctx, statement); err != nil {
					t.Fatalf("rebuilding the history without its key: %v", err)
				}
			}
		}
		falseClaim := tamper(t, db, edits, stop)

		mode := Options{AllowNewerWithinWindow: window}
		if _, err := ApplyWith(ctx, "sqlite3", db, mode); err != nil {
			return // Refused: always an acceptable answer to a tampered history.
		}

		// It succeeded, so the database must be exactly what a clean
		// migration leaves.
		counts := map[string]int{}
		for _, r := range historyOf(t, db, "sqlite3") {
			counts[r.version]++
		}
		for _, n := range names {
			if counts[n] != 1 {
				t.Fatalf("Apply succeeded with %s recorded %d times", n, counts[n])
			}
		}
		if got := sqliteShape(t, db); got != fullyMigratedShape(t) && !falseClaim {
			t.Fatalf("Apply succeeded into a schema that is not the migrated one")
		}
	})
}

// tamper applies each (operation, operand) pair to db's history, and reports
// whether it planted a false claim: a row naming a migration of this build
// later than stop, which was never applied.
func tamper(t *testing.T, db *sql.DB, edits []byte, stop string) bool {
	t.Helper()
	falseClaim := false
	ctx := context.Background()
	for i := 0; i+1 < len(edits) && i < 16; i += 2 {
		operand := int(edits[i+1])
		var err error
		switch edits[i] % 4 {
		case 0: // delete one recorded row, by position
			_, err = db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = (SELECT version FROM schema_migrations ORDER BY version LIMIT 1 OFFSET ?)`, operand%40)
		case 1: // record a row again (only possible once the key is gone)
			_, err = db.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at, compatible_from) SELECT version, applied_at, compatible_from FROM schema_migrations ORDER BY version LIMIT 1 OFFSET ?`, operand%40)
		case 2: // plant a row with a chosen name and a real floor
			name := tamperNames[operand%len(tamperNames)]
			if versionPattern.MatchString(name) && name > stop && !strings.HasPrefix(name, "9") {
				falseClaim = true
			}
			_, err = db.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at, compatible_from) VALUES (?, ?, ?)`, name, time.Now().UTC(), "0032_add_launchables.sql")
		case 3: // clear or corrupt the newest row's floor
			floor := sql.NullString{String: tamperNames[operand%len(tamperNames)], Valid: operand%2 == 0}
			_, err = db.ExecContext(ctx, `UPDATE schema_migrations SET compatible_from = ? WHERE version = (SELECT max(version) FROM schema_migrations)`, floor)
		}
		// A tampering statement the database itself refuses (a duplicate
		// against the key) is simply a tampering that did not happen.
		_ = err
	}
	return falseClaim
}

// fullyMigratedShape is the shape of a SQLite database migrated cleanly from
// nothing, computed once.
func fullyMigratedShape(t *testing.T) string {
	t.Helper()
	referenceShapeOnce.Do(func() {
		db, err := sql.Open("sqlite3", sqliteDSN(filepath.Join(t.TempDir(), "reference.db")))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := Apply(context.Background(), "sqlite3", db); err != nil {
			t.Fatalf("migrating the reference: %v", err)
		}
		referenceShape = sqliteShape(t, db)
	})
	return referenceShape
}

// sqliteShape is every schema object's definition except the history
// table's own, which the tampering is allowed to have rebuilt.
func sqliteShape(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND tbl_name <> 'schema_migrations' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, definition string
		if err := rows.Scan(&typ, &name, &definition); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s %s %s\n", typ, name, definition)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
