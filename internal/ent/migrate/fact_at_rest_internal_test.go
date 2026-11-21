// The facts-at-rest migration (0052 on SQLite, 0049 on PostgreSQL): what a
// build before it stored in plaintext is gone from the tables on both
// dialects, and on SQLite from the file itself.
package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// factAtRest names the migration and the one before it, by dialect.
var factAtRest = map[string][2]string{
	"sqlite3":  {"0051_mesh_runner_seed.sql", "0052_fact_at_rest.sql"},
	"postgres": {"0048_mesh_runner_seed.sql", "0049_fact_at_rest.sql"},
}

// seedPlaintextFact stores one device and one fact as the build before the
// migration did: its key and value in plaintext, a plain hash, a coverage
// naming the key and an observation. placeholder writes the dialect's nth
// bind parameter.
func seedPlaintextFact(t *testing.T, db *sql.DB, placeholder func(n int) string) {
	t.Helper()
	now := time.Now().UTC()
	p := placeholder
	exec(t, db, `INSERT INTO devices (created_at, updated_at, device_id, name, type, properties, version, state) VALUES (`+p(1)+`, `+p(2)+`, 'dev-zq9', 'web1', 'linux_server', '{}', 1, 'discovered')`, now, now)
	var dev int64
	if err := db.QueryRow(`SELECT id FROM devices WHERE device_id = 'dev-zq9'`).Scan(&dev); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO facts (created_at, updated_at, key, payload, hash, kind, observation_seq, device_facts) VALUES (`+p(1)+`, `+p(2)+`, 'package.zq9plainkey', '{"json":"\"1.2.3-zq9plainvalue\""}', 'zq9plainhash', 'stable', 1, `+p(3)+`)`, now, now, dev)
	exec(t, db, `INSERT INTO fact_coverages (digest, stable, device_fact_coverages) VALUES ('zq9digest', '["package.zq9plainkey"]', `+p(1)+`)`, dev)
	exec(t, db, `INSERT INTO fact_observations (seq, observed_at, provenance, coverage, device_id) VALUES (1, `+p(1)+`, '{}', 'zq9digest', `+p(2)+`)`, now, dev)
}

// TestApply_TheFactAtRestMigrationRemovesEveryPlaintextFact: on both
// dialects, a database holding plaintext facts comes out of the migration
// with none, the device kept, the new columns there and the key column gone.
func TestApply_TheFactAtRestMigrationRemovesEveryPlaintextFact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		src := migrationSources[dialectName]
		names := namesOf(t, dialectName)
		steps := factAtRest[dialectName]
		if _, err := applyPending(ctx, db, src, names, steps[0], gateStrict); err != nil {
			t.Fatal(err)
		}
		placeholder := func(int) string { return "?" }
		if dialectName == "postgres" {
			placeholder = func(n int) string { return "$" + string(rune('0'+n)) }
		}
		seedPlaintextFact(t, db, placeholder)
		out, err := applyPending(ctx, db, src, names, steps[1], gateStrict)
		if err != nil || len(out.Applied) != 1 || len(out.NotReclaimed) != 0 {
			t.Fatalf("applying %s: %+v, %v", steps[1], out, err)
		}
		for _, table := range []string{"facts", "fact_coverages", "fact_observations"} {
			if n := countRows(t, db, table); n != 0 {
				t.Errorf("%s holds %d row(s) after the migration, want none", table, n)
			}
		}
		if n := countRows(t, db, "devices"); n != 1 {
			t.Errorf("devices holds %d row(s), want the one device kept", n)
		}
		// The new columns are there, and the plaintext key column is gone
		// with its index (what that leaves an older build is
		// fact_at_rest_refusal_internal_test.go's).
		if _, err := db.Exec(`SELECT key_token, keyed_by FROM facts`); err != nil {
			t.Errorf("the new columns: %v", err)
		}
		model := readModel(t, dialectName, db)
		if _, ok := model.tables["facts"].columns["key"]; ok {
			t.Error("facts.key is still there, so the build before facts at rest can still store a fact's key in plaintext")
		}
	})
}

// TestApply_TheFactAtRestMigrationLeavesNoPlaintextInTheSQLiteFile: SQLite
// keeps a deleted row's bytes in the file until its pages are reused, so the
// migration is followed by a VACUUM and a checkpoint. The file and its
// write-ahead log are searched byte for byte: before the migration they hold
// the plaintext fact (the control), after it they do not.
func TestApply_TheFactAtRestMigrationLeavesNoPlaintextInTheSQLiteFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	db := openDSN(t, "sqlite3", sqliteDSN(path))
	src := migrationSources["sqlite3"]
	names := namesOf(t, "sqlite3")
	steps := factAtRest["sqlite3"]
	if _, err := applyPending(ctx, db, src, names, steps[0], gateStrict); err != nil {
		t.Fatal(err)
	}
	seedPlaintextFact(t, db, func(int) string { return "?" })
	holds := plaintextIn(t, path)
	if before := holds(); len(before) == 0 {
		t.Fatal("the control failed: the seeded plaintext fact is not in the database's files to find")
	}
	out, err := applyPending(ctx, db, src, names, steps[1], gateStrict)
	if err != nil || len(out.NotReclaimed) != 0 {
		t.Fatalf("applying %s: %+v, %v", steps[1], out, err)
	}
	if after := holds(); len(after) != 0 {
		t.Errorf("after the migration: %s", strings.Join(after, "; "))
	}
}

// TestReclaim_AFailureIsReportedAndNeverFailsTheStart: an after-commit
// statement the database refuses is named in the outcome, with the migration
// applied and recorded all the same.
func TestReclaim_AFailureIsReportedAndNeverFailsTheStart(t *testing.T) {
	ctx := context.Background()
	db := freshDB(t, "sqlite3")
	src := migrationSources["sqlite3"]
	names := namesOf(t, "sqlite3")
	steps := factAtRest["sqlite3"]
	if _, err := applyPending(ctx, db, src, names, steps[0], gateStrict); err != nil {
		t.Fatal(err)
	}
	src.afterCommit = map[string][]string{steps[1]: {"NOT A STATEMENT"}}
	out, err := applyPending(ctx, db, src, names, steps[1], gateStrict)
	if err != nil || len(out.Applied) != 1 {
		t.Fatalf("applying %s: %+v, %v", steps[1], out, err)
	}
	if len(out.NotReclaimed) != 1 || !strings.HasPrefix(out.NotReclaimed[0], steps[1]+": NOT A STATEMENT") {
		t.Errorf("NotReclaimed = %q, want the migration and its statement named", out.NotReclaimed)
	}
	if !recorded(historyOf(t, db, "sqlite3"), steps[1]) {
		t.Error("the migration is not recorded")
	}
	// A database that is closed cannot give the connection either, and says
	// so rather than failing anything.
	closed := freshDB(t, "sqlite3")
	_ = closed.Close()
	var shut Outcome
	reclaimOwed(ctx, closed, src, []appliedRow{{version: steps[1]}}, &shut)
	if len(shut.NotReclaimed) != 1 || !strings.Contains(shut.NotReclaimed[0], "taking a connection") || len(shut.Reclaimed) != 0 {
		t.Errorf("reclaiming on a closed database: %+v, want the connection's failure named", shut)
	}
}

// reclaimedMark is the number SQLite's header records as reclaimed through.
func reclaimedMark(t *testing.T, db *sql.DB) int {
	t.Helper()
	var mark int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&mark); err != nil {
		t.Fatal(err)
	}
	return mark
}

// TestReclaim_AStartThatFindsItOwedRunsItAndOneThatFindsItDoneDoesNot: a
// start that died between the migration's commit and its VACUUM (here, one
// whose VACUUM failed) leaves the plaintext in the file and the reclamation
// owed. The next start, with nothing left to apply, runs it: the file holds
// no plaintext afterwards, the outcome says so, and the record says it is
// done. A start after that finds it done and runs nothing, which a statement
// that would fail shows, since it is never tried.
func TestReclaim_AStartThatFindsItOwedRunsItAndOneThatFindsItDoneDoesNot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	db := openDSN(t, "sqlite3", sqliteDSN(path))
	src := migrationSources["sqlite3"]
	names := namesOf(t, "sqlite3")
	steps := factAtRest["sqlite3"]
	if _, err := applyPending(ctx, db, src, names, steps[0], gateStrict); err != nil {
		t.Fatal(err)
	}
	seedPlaintextFact(t, db, func(int) string { return "?" })
	holds := plaintextIn(t, path)
	// The start that dies: the migration commits and its reclamation fails.
	failing := src
	failing.afterCommit = map[string][]string{steps[1]: {"NOT A STATEMENT"}}
	out, err := applyPending(ctx, db, failing, names, steps[1], gateStrict)
	if err != nil || len(out.Applied) != 1 || len(out.NotReclaimed) != 1 || len(out.Reclaimed) != 0 {
		t.Fatalf("the start whose reclamation failed: %+v, %v", out, err)
	}
	if len(holds()) == 0 {
		t.Fatal("the control failed: the plaintext is not in the file after a migration whose VACUUM never ran")
	}
	if mark := reclaimedMark(t, db); mark != 0 {
		t.Fatalf("the record says reclaimed through %d, though nothing was", mark)
	}
	// The next start: nothing to apply, the reclamation owed.
	out, err = applyPending(ctx, db, src, names, steps[1], gateStrict)
	if err != nil || len(out.Applied) != 0 || len(out.NotReclaimed) != 0 || len(out.Reclaimed) != 1 || out.Reclaimed[0] != steps[1] {
		t.Fatalf("the start that finds it owed: %+v, %v; want %s reclaimed", out, err, steps[1])
	}
	if after := holds(); len(after) != 0 {
		t.Errorf("after the owed reclamation: %s", strings.Join(after, "; "))
	}
	if mark := reclaimedMark(t, db); mark != 52 {
		t.Errorf("the record says reclaimed through %d, want 52", mark)
	}
	// The start after: done, so not tried, even with a statement that fails.
	out, err = applyPending(ctx, db, failing, names, steps[1], gateStrict)
	if err != nil || len(out.Reclaimed) != 0 || len(out.NotReclaimed) != 0 {
		t.Errorf("the start that finds it done: %+v, %v; want nothing run", out, err)
	}
	// And through the public entry, migrating the rest of the way.
	whole, err := ApplyWith(ctx, "sqlite3", db, Options{})
	if err != nil || len(whole.Reclaimed) != 0 || len(whole.NotReclaimed) != 0 {
		t.Errorf("ApplyWith after it was done: %+v, %v", whole, err)
	}
}

// plaintextIn is a check that lists what of seedPlaintextFact's plaintext
// the database file at path and its write-ahead log hold.
func plaintextIn(t *testing.T, path string) func() []string {
	return func() []string {
		t.Helper()
		var found []string
		for _, file := range []string{path, path + "-wal"} {
			raw, err := os.ReadFile(file) // #nosec G304 -- the test's own database
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, plain := range []string{"zq9plainkey", "zq9plainvalue", "zq9plainhash"} {
				if bytes.Contains(raw, []byte(plain)) {
					found = append(found, filepath.Base(file)+" holds "+plain)
				}
			}
		}
		return found
	}
}
