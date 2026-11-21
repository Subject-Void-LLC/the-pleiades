// The facts at rest migration makes the schema itself refuse the build before
// it (SQLite 0052, PostgreSQL 0049): whatever that build sends to store a
// gather, on a database it was not stopped from serving, stores nothing.
//
// The statements below are that build's own, as its generated ent client sends
// them: captured, with an ent debug driver, from the real Store.Record of the
// build at the migration before facts at rest (f49b70bd), for one gather of a
// device with no facts yet, one stable key and one volatile one. Only the bound
// values are this test's. Each runs on a real database of each dialect,
// through the migration before facts at rest (where it must store the
// plaintext, the control) and through facts at rest (where it must not).
package migrate

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

// preSealingStatement is one statement of the build before facts at rest's
// Store.Record, in SQLite's spelling (postgresSpelling makes PostgreSQL's).
type preSealingStatement struct {
	// what says what that build was doing when it sent it.
	what string

	// query is the statement, verbatim from that build's client.
	query string

	// args are its values, for the device whose database ID is dev.
	args func(dev int64) []any
}

// preSealingStable and preSealingVolatile are the gather's two keys.
const (
	preSealingStable   = "package.zq8plainkey"
	preSealingVolatile = "uptime.zq8plain"
)

// preSealingAt is when the gather was observed.
var preSealingAt = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// factColumns is every column the build before facts at rest reads from facts.
const factColumns = "`facts`.`id`, `facts`.`created_at`, `facts`.`updated_at`, `facts`.`key`, `facts`.`payload`, `facts`.`hash`, `facts`.`kind`, `facts`.`provenance`, `facts`.`last_seen_at`, `facts`.`last_provenance`, `facts`.`observation_seq`, `facts`.`chain_prev`, `facts`.`chain`"

// The build's two fact inserts, a stable row and a volatile one.
const (
	preSealingInsertStable   = "INSERT INTO `facts` (`created_at`, `updated_at`, `key`, `payload`, `hash`, `kind`, `provenance`, `observation_seq`, `chain_prev`, `chain`, `device_facts`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `id`"
	preSealingInsertVolatile = "INSERT INTO `facts` (`created_at`, `updated_at`, `key`, `payload`, `hash`, `kind`, `provenance`, `observation_seq`, `device_facts`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING `id`"
)

// preSealingStableArgs and preSealingVolatileArgs are the two inserts' values:
// the key in plaintext, the value as {"json": ...}, a plain SHA-256.
func preSealingStableArgs(dev int64) []any {
	return []any{preSealingAt, preSealingAt, preSealingStable, `{"json":"\"1.2.3-zq8plainvalue\""}`, "bca9a3ac098440a875846e09c2f192208b49cab14065ad1167b52656ac40c4a7", "stable", `{"json":"{}"}`, 1, "", "f4c194f9b4ca4aad5e6df7013f96580bfa75ea88ee36249358d8b7c6091ace57", dev}
}

// preSealingVolatileArgs is described at preSealingStableArgs.
func preSealingVolatileArgs(dev int64) []any {
	return []any{preSealingAt, preSealingAt, preSealingVolatile, `{"json":"42"}`, "870c6d79a2b40e67b79cb6cf6eb6de9d341e29a0ca4e64ae6810e23fd09488c9", "volatile", `{"json":"{}"}`, 1, dev}
}

// preSealingCoverage is the gather's coverage digest.
const preSealingCoverage = "c47d63d4a4b9104f3593c3eb397b571d606b4c3641529c167f8f3a3fb6db0d76"

// preSealingRecord is the build's whole Store.Record for the gather, in the
// order it sends it, inside the one transaction Record opens. Its coverage,
// which names both keys in plaintext, and its observation come last.
var preSealingRecord = []preSealingStatement{
	{"reading the device", "SELECT `devices`.`id`, `devices`.`created_at`, `devices`.`updated_at`, `devices`.`device_id`, `devices`.`name`, `devices`.`type`, `devices`.`properties`, `devices`.`secret_binding`, `devices`.`version`, `devices`.`state`, `devices`.`source`, `devices`.`source_synced_at`, `devices`.`tags` FROM `devices` WHERE `devices`.`device_id` = ? LIMIT 2",
		func(int64) []any { return []any{"dev-zq8"} }},
	{"reading the newest observation", "SELECT `fact_observations`.`id`, `fact_observations`.`seq` FROM `fact_observations` WHERE `fact_observations`.`device_id` = ? ORDER BY `fact_observations`.`seq` DESC LIMIT 1",
		func(dev int64) []any { return []any{dev} }},
	{"reading the chain's head", "SELECT " + factColumns + " FROM `facts` WHERE (EXISTS (SELECT `devices`.`id` FROM `devices` WHERE `facts`.`device_facts` = `devices`.`id` AND `devices`.`id` = ?) AND `facts`.`chain` IS NOT NULL) AND `facts`.`chain` <> ? ORDER BY `facts`.`id` DESC LIMIT 1",
		func(dev int64) []any { return []any{dev, ""} }},
	{"reading the keys' rows", "SELECT " + factColumns + " FROM `facts` WHERE `facts`.`key` IN (?, ?) AND EXISTS (SELECT `devices`.`id` FROM `devices` WHERE `facts`.`device_facts` = `devices`.`id` AND `devices`.`id` = ?) ORDER BY `facts`.`id` ASC",
		func(dev int64) []any { return []any{preSealingStable, preSealingVolatile, dev} }},
	{"storing the stable fact", preSealingInsertStable, preSealingStableArgs},
	{"storing the volatile fact", preSealingInsertVolatile, preSealingVolatileArgs},
	{"looking for the coverage", "SELECT `fact_coverages`.`id` FROM `fact_coverages` WHERE `fact_coverages`.`digest` = ? AND EXISTS (SELECT `devices`.`id` FROM `devices` WHERE `fact_coverages`.`device_fact_coverages` = `devices`.`id` AND `devices`.`id` = ?) LIMIT 1",
		func(dev int64) []any { return []any{preSealingCoverage, dev} }},
	{"storing the coverage", "INSERT INTO `fact_coverages` (`digest`, `stable`, `volatile`, `device_fact_coverages`) VALUES (?, ?, ?, ?) RETURNING `id`",
		func(dev int64) []any {
			return []any{preSealingCoverage, `["` + preSealingStable + `"]`, `["` + preSealingVolatile + `"]`, dev}
		}},
	{"storing the observation", "INSERT INTO `fact_observations` (`seq`, `observed_at`, `order_source`, `order_token`, `provenance`, `coverage`, `device_id`) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING `id`",
		func(dev int64) []any { return []any{1, preSealingAt, "", 0, `{"json":"{}"}`, preSealingCoverage, dev} }},
}

// postgresSpelling is query as the same ent client spells it for PostgreSQL:
// identifiers in double quotes and numbered placeholders, nothing else changed.
func postgresSpelling(query string) string {
	query = strings.ReplaceAll(query, "`", `"`)
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// spelled is query in the dialect's spelling.
func spelled(dialectName, query string) string {
	if dialectName == "postgres" {
		return postgresSpelling(query)
	}
	return query
}

// refusals are, by dialect, the database's own words for each refusal: a
// read naming the dropped column, a write naming it, and a row naming no keys.
var refusals = map[string]struct{ readsKey, writesKey, namesNoKeys string }{
	"sqlite3":  {"no such column: facts.key", "table facts has no column named key", "CHECK constraint failed: fact_keyed"},
	"postgres": {"column facts.key does not exist", `column "key" of relation "facts" does not exist`, `violates check constraint "fact_keyed"`},
}

// seedPreSealingDevice stores the device the gather is of and returns its
// database ID.
func seedPreSealingDevice(t *testing.T, dialectName string, db *sql.DB) int64 {
	t.Helper()
	p := placeholderFor(dialectName)
	now := time.Now().UTC()
	exec(t, db, `INSERT INTO devices (created_at, updated_at, device_id, name, type, properties, version, state) VALUES (`+p(1)+`, `+p(2)+`, 'dev-zq8', 'web8', 'linux_server', '{}', 1, 'discovered')`, now, now)
	var dev int64
	if err := db.QueryRow(`SELECT id FROM devices WHERE device_id = 'dev-zq8'`).Scan(&dev); err != nil {
		t.Fatal(err)
	}
	return dev
}

// runPreSealingRecord sends preSealingRecord as that build's Record does: in
// one transaction, stopping at the first error and rolling back, committing
// only when every statement succeeded. It returns the index of the statement
// refused, -1 when none was, and the database's error.
func runPreSealingRecord(t *testing.T, dialectName string, db *sql.DB, dev int64) (int, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range preSealingRecord {
		rows, err := tx.QueryContext(ctx, spelled(dialectName, s.query), s.args(dev)...)
		if err == nil {
			// A statement can also fail while its rows are read.
			for rows.Next() {
			}
			err = rows.Err()
			_ = rows.Close()
		}
		if err != nil {
			_ = tx.Rollback()
			return i, err
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("committing the record: %v", err)
	}
	return -1, nil
}

// TestApply_TheBuildBeforeFactsAtRestStoresNothingAfterIt: the build before
// facts at rest's whole record of a gather, sent to a database it is still
// serving. Through the migration before facts at rest it stores the gather in
// plaintext, every table of it (the control: these are the statements that
// write it). Through facts at rest the first statement naming the dropped key
// column is refused, the transaction rolls back, and none of the three tables
// holds anything of it: the coverage and the observation that name its keys
// come after the refused statement in the same transaction.
func TestApply_TheBuildBeforeFactsAtRestStoresNothingAfterIt(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		src := migrationSources[dialectName]
		names := namesOf(t, dialectName)
		steps := factAtRest[dialectName]
		if _, err := applyPending(ctx, db, src, names, steps[0], gateStrict); err != nil {
			t.Fatal(err)
		}
		dev := seedPreSealingDevice(t, dialectName, db)

		// The control, on the schema that build serves.
		if at, err := runPreSealingRecord(t, dialectName, db, dev); at != -1 {
			t.Fatalf("the control failed: statement %d (%s) was refused before facts at rest: %v", at, preSealingRecord[at].what, err)
		}
		var key, covered string
		if err := db.QueryRow(`SELECT "key" FROM facts WHERE kind = 'stable'`).Scan(&key); err != nil || key != preSealingStable {
			t.Fatalf("the control failed: the stable row's key is %q (%v), want it stored in plaintext", key, err)
		}
		if err := db.QueryRow(`SELECT CAST(stable AS TEXT) FROM fact_coverages`).Scan(&covered); err != nil || !strings.Contains(covered, preSealingStable) {
			t.Fatalf("the control failed: the coverage lists %q (%v), want the key in plaintext", covered, err)
		}
		if n := countRows(t, db, "fact_observations"); n != 1 {
			t.Fatalf("the control failed: fact_observations holds %d row(s), want the gather's one", n)
		}

		if _, err := applyPending(ctx, db, src, names, steps[1], gateStrict); err != nil {
			t.Fatal(err)
		}
		at, err := runPreSealingRecord(t, dialectName, db, dev)
		if at != 2 || err == nil || !strings.Contains(err.Error(), refusals[dialectName].readsKey) {
			what := "nothing"
			if at >= 0 {
				what = preSealingRecord[at].what
			}
			t.Errorf("after facts at rest the record was refused at statement %d (%s): %v; want statement 2, reading the chain's head, refused with %q", at, what, err, refusals[dialectName].readsKey)
		}
		for _, table := range []string{"facts", "fact_coverages", "fact_observations"} {
			if n := countRows(t, db, table); n != 0 {
				t.Errorf("%s holds %d row(s) after the refused record, want none", table, n)
			}
		}
	})
}

// TestApply_FactsAtRestRefusesEveryPlaintextFactRow: each of that build's
// fact inserts, sent alone, is refused for naming the dropped key column. The
// same insert without that column, which is what a writer knowing neither key
// nor the two columns that replaced it sends, is refused by the fact_keyed
// check. The control is the same row as this build writes it, with a token
// and keys named, which is stored.
func TestApply_FactsAtRestRefusesEveryPlaintextFactRow(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		if _, err := applyPending(ctx, db, migrationSources[dialectName], namesOf(t, dialectName), factAtRest[dialectName][1], gateStrict); err != nil {
			t.Fatal(err)
		}
		dev := seedPreSealingDevice(t, dialectName, db)
		want := refusals[dialectName]
		send := func(query string, args []any) error {
			_, err := db.ExecContext(ctx, spelled(dialectName, query), args...)
			return err
		}

		for name, insert := range map[string]struct {
			query string
			args  []any
		}{
			"the stable fact":   {preSealingInsertStable, preSealingStableArgs(dev)},
			"the volatile fact": {preSealingInsertVolatile, preSealingVolatileArgs(dev)},
		} {
			if err := send(insert.query, insert.args); err == nil || !strings.Contains(err.Error(), want.writesKey) {
				t.Errorf("that build storing %s: %v; want it refused with %q", name, err, want.writesKey)
			}
			// Without the key column and its value: a row naming no keys.
			noKey := strings.Replace(strings.Replace(insert.query, "`key`, ", "", 1), "?, ", "", 1)
			args := append(append([]any{}, insert.args[:2]...), insert.args[3:]...)
			if err := send(noKey, args); err == nil || !strings.Contains(err.Error(), want.namesNoKeys) {
				t.Errorf("%s with no key column: %v; want it refused with %q", name, err, want.namesNoKeys)
			}
		}
		if n := countRows(t, db, "facts"); n != 0 {
			t.Fatalf("facts holds %d row(s) after every refused insert, want none", n)
		}

		// The control: the stable row with a token and keys, as this build
		// stores one (the sealed payload is the store's business, not the
		// schema's), is stored, so the refusals above are the protection's.
		sealed := strings.Replace(strings.Replace(preSealingInsertStable, "`key`, ", "`key_token`, `keyed_by`, ", 1), "?, ", "?, ?, ", 1)
		args := append(append([]any{}, preSealingStableArgs(dev)[:2]...), append([]any{strings.Repeat("a", 64), "zq8keys"}, preSealingStableArgs(dev)[3:]...)...)
		if err := send(sealed, args); err != nil {
			t.Fatalf("the control failed: a row naming its token and keys was refused: %v", err)
		}
		if n := countRows(t, db, "facts"); n != 1 {
			t.Errorf("facts holds %d row(s) after the control, want the one", n)
		}
	})
}
