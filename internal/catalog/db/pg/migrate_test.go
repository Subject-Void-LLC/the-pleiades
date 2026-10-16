// Tests for db.pg.migrate against a real PostgreSQL, its files read from
// a real directory through the device.
package pg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
)

// writeMigrations writes files into a fresh directory, returning it.
func writeMigrations(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// migrate runs db.pg.migrate (or its check) with extra params.
func migrate(h *harness, check bool, extra map[string]any) (map[string]any, error) {
	rc := h.fresh()
	p := params(map[string]any{"connection": "admin", "database": "app"})
	for k, v := range extra {
		p[k] = v
	}
	var err error
	if check {
		_, err = pg.CheckMigrate(context.Background(), rc, h.device, p)
	} else {
		_, err = pg.Migrate(context.Background(), rc, h.device, p)
	}
	return rc.stats, err
}

// TestMigrate_AppliesInOrderOnceAsTheRole: a check reports the pending
// files and applies nothing; a run applies them in numeric order as the
// role, each recorded; a second run applies nothing; a file that cannot
// run in a transaction runs alone; a failing file rolls back and stops,
// leaving the files before it applied.
func TestMigrate_AppliesInOrderOnceAsTheRole(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app", "CREATE ROLE app_owner", "GRANT CREATE ON SCHEMA public TO app_owner")
	dir := writeMigrations(t, map[string]string{
		"10_add_index.sql": "-- pleiades: no-transaction\nCREATE INDEX CONCURRENTLY orders_note ON orders (note);\n",
		"2_orders.sql":     "CREATE TABLE orders (id int PRIMARY KEY, note text);\nINSERT INTO orders VALUES (1, 'a;b');\n",
		"9_backfill.sql":   "/* backfill */ UPDATE orders SET note = 'x' WHERE note IS NULL;",
		"README.md":        "not a migration",
	})
	base := map[string]any{"dir": dir, "role": "app_owner"}

	stats, err := migrate(h, true, base)
	if err != nil {
		t.Fatalf("the check: %v", err)
	}
	if pending := stats["pending"].([]any); len(pending) != 3 || pending[0].(map[string]any)["version"] != int64(2) {
		t.Errorf("pending = %v", pending)
	}
	if got := h.value(t, "app", "SELECT to_regclass('public.pleiades_migrations') IS NULL"); got != "true" {
		t.Fatal("a check created the version table")
	}

	stats, err = migrate(h, false, base)
	if err != nil {
		t.Fatalf("the run: %v", err)
	}
	if applied := stats["applied"].([]any); len(applied) != 3 {
		t.Fatalf("applied = %v", applied)
	}
	if got := h.value(t, "app", "SELECT string_agg(version::text || ':' || name, ',' ORDER BY version) FROM pleiades_migrations"); got != "2:orders,9:backfill,10:add_index" {
		t.Errorf("recorded = %s", got)
	}
	if got := h.value(t, "app", "SELECT tableowner FROM pg_tables WHERE tablename = 'orders'"); got != "app_owner" {
		t.Errorf("orders is owned by %s, want the role", got)
	}
	if got := h.value(t, "app", "SELECT count(*) FROM pg_indexes WHERE indexname = 'orders_note'"); got != "1" {
		t.Error("the no-transaction file did not run")
	}
	if stats, err := migrate(h, false, base); err != nil || len(stats["applied"].([]any)) != 0 {
		t.Errorf("a second run = %v, %v", stats, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "11_ok.sql"), []byte("CREATE TABLE ok (id int);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "12_broken.sql"), []byte("CREATE TABLE half (id int);\nSELECT * FROM missing_table;"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = migrate(h, false, base)
	if err == nil || !strings.Contains(err.Error(), "12_broken.sql failed") || !strings.Contains(err.Error(), "missing_table") || !strings.Contains(err.Error(), "the 1 file(s) before it stay applied") {
		t.Fatalf("a failing file = %v", err)
	}
	if h.value(t, "app", "SELECT to_regclass('public.half') IS NULL") != "true" || h.value(t, "app", "SELECT to_regclass('public.ok') IS NULL") != "false" {
		t.Error("the failing file was not rolled back whole, or the one before it was")
	}
}

// TestMigrate_RefusesBeforeAnythingRuns: every problem in the directory
// is found before any file runs.
func TestMigrate_RefusesBeforeAnythingRuns(t *testing.T) {
	h := newHarness(t)
	for name, tc := range map[string]struct {
		files map[string]string
		extra map[string]any
		want  string
	}{
		"a misnamed file":        {map[string]string{"1_a.sql": "SELECT 1;", "2-b.sql": "SELECT 1;"}, nil, "2-b.sql is not named"},
		"an upper-case .SQL":     {map[string]string{"1_a.sql": "SELECT 1;", "2_b.SQL": "SELECT 1;"}, nil, "2_b.SQL is not named"},
		"a down migration":       {map[string]string{"1_a.sql": "SELECT 1;", "1_a.down.sql": "SELECT 1;"}, nil, "is a down migration"},
		"a shared version":       {map[string]string{"1_a.sql": "SELECT 1;", "01_b.sql": "SELECT 1;"}, nil, "are both version 1"},
		"its own commit":         {map[string]string{"1_a.sql": "CREATE TABLE a (id int);\nCOMMIT;"}, nil, "1_a.sql:2: it ends the transaction"},
		"concurrently inside":    {map[string]string{"1_a.sql": "CREATE INDEX CONCURRENTLY i ON t (c);"}, nil, "cannot run in a transaction"},
		"two outside":            {map[string]string{"1_a.sql": "-- pleiades: no-transaction\nVACUUM;\nVACUUM;"}, nil, "must hold exactly one statement"},
		"destructive":            {map[string]string{"1_a.sql": "CREATE TABLE a (id int);", "2_b.sql": "ALTER TABLE a DROP COLUMN id;"}, map[string]any{"additive_only": true}, "2_b.sql:1: it drops a column (additive_only)"},
		"a misspelled directive": {map[string]string{"1_a.sql": "-- pleiades: no-transation\nSELECT 1;"}, nil, `"no-transation" is not a directive`},
		"an empty file":          {map[string]string{"1_a.sql": "-- nothing\n"}, nil, "holds no statement"},
	} {
		t.Run(name, func(t *testing.T) {
			extra := map[string]any{"dir": writeMigrations(t, tc.files)}
			for k, v := range tc.extra {
				extra[k] = v
			}
			if _, err := migrate(h, false, extra); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("migrate = %v, want %q", err, tc.want)
			}
			if got := h.value(t, "app", "SELECT to_regclass('public.pleiades_migrations') IS NULL"); got != "true" {
				t.Fatal("a refused directory created the version table")
			}
		})
	}
}

// TestMigrate_UpToZeroAppliesNothingPastIt: up_to is a version, and 0
// is one, so it applies nothing above it rather than everything.
func TestMigrate_UpToZeroAppliesNothingPastIt(t *testing.T) {
	h := newHarness(t)
	dir := writeMigrations(t, map[string]string{"0_base.sql": "CREATE TABLE base (id int);", "1_a.sql": "CREATE TABLE a (id int);"})
	stats, err := migrate(h, false, map[string]any{"dir": dir, "up_to": 0})
	if err != nil {
		t.Fatalf("up_to 0: %v", err)
	}
	if applied := stats["applied"].([]any); len(applied) != 1 {
		t.Errorf("up_to 0 applied %v, want version 0 alone", applied)
	}
	if got := h.value(t, "app", "SELECT to_regclass('public.a') IS NULL"); got != "true" {
		t.Error("up_to 0 applied version 1")
	}
}

// TestMigrate_RefusesASessionReadingBackslashesAsEscapes: with
// standard_conforming_strings off, the server reads a backslash before a
// quote as an escape, so the second file below is an INSERT and a DROP,
// where the additive-only lint, which reads backslashes as the standard
// says, saw one INSERT. A file that turned the setting off stops the run
// before the next file is sent.
func TestMigrate_RefusesASessionReadingBackslashesAsEscapes(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app", "CREATE TABLE n (t text)", "CREATE TABLE users (id int)")
	dir := writeMigrations(t, map[string]string{
		"1_off.sql":   "SET standard_conforming_strings = off;",
		"2_sneak.sql": `INSERT INTO n VALUES ('it\''); DROP TABLE users; --');`,
	})
	_, err := migrate(h, false, map[string]any{"dir": dir, "additive_only": true})
	if err == nil || !strings.Contains(err.Error(), "standard_conforming_strings is off") {
		t.Errorf("migrate = %v, want the session refused", err)
	}
	if got := h.value(t, "app", "SELECT to_regclass('public.users') IS NOT NULL"); got != "true" {
		t.Fatal("the hidden DROP ran")
	}
}

// TestMigrate_HoldsAppliedHistory: a destructive file that says why runs
// and is listed; an applied file that changed is refused; a pending
// version below the newest applied one is refused unless allowed.
func TestMigrate_HoldsAppliedHistory(t *testing.T) {
	h := newHarness(t)
	dir := writeMigrations(t, map[string]string{
		"1_a.sql": "CREATE TABLE a (id int, old text);",
		"3_c.sql": "-- pleiades: destructive: old was never read after 2026-01\nALTER TABLE a DROP COLUMN old;",
	})
	stats, err := migrate(h, false, map[string]any{"dir": dir, "additive_only": true})
	if err != nil {
		t.Fatalf("a destructive file that says why: %v", err)
	}
	if d := stats["destructive"].([]any); len(d) != 1 || d[0].(map[string]any)["reason"] != "old was never read after 2026-01" {
		t.Errorf("destructive = %v", d)
	}

	if err := os.WriteFile(filepath.Join(dir, "2_b.sql"), []byte("CREATE TABLE b (id int);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(h, false, map[string]any{"dir": dir}); err == nil || !strings.Contains(err.Error(), "come before version 3") {
		t.Fatalf("an early pending version = %v", err)
	}
	if stats, err := migrate(h, false, map[string]any{"dir": dir, "allow_out_of_order": true}); err != nil || len(stats["applied"].([]any)) != 1 {
		t.Fatalf("allowed out of order = %v, %v", stats, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "1_a.sql"), []byte("CREATE TABLE a (id bigint, old text);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(h, true, map[string]any{"dir": dir}); err == nil || !strings.Contains(err.Error(), "1_a.sql was applied with checksum") {
		t.Fatalf("a changed applied file = %v", err)
	}
}

// TestMigrate_TwoRunsAtOnceApplyEachFileOnce: two runs started together
// against one directory apply each file exactly once, with no lock: the
// second waits on the first's claim and then reads its record.
func TestMigrate_TwoRunsAtOnceApplyEachFileOnce(t *testing.T) {
	h := newHarness(t)
	dir := writeMigrations(t, map[string]string{
		"1_slow.sql":  "CREATE TABLE once (id int);\nSELECT pg_sleep(1);",
		"2_after.sql": "INSERT INTO once VALUES (1);",
	})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		rc := &stubContext{secrets: h.rc.secrets, stats: map[string]any{}, connections: h.rc.connections}
		go func() {
			_, err := pg.Migrate(context.Background(), rc, h.device, params(map[string]any{"connection": "admin", "database": "app", "dir": dir}))
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("a run: %v", err)
		}
	}
	if got := h.value(t, "app", "SELECT count(*) FROM once"); got != "1" {
		t.Errorf("2_after.sql ran %s times", got)
	}
	if got := h.value(t, "app", "SELECT count(*) FROM pleiades_migrations"); got != "2" {
		t.Errorf("the version table holds %s rows", got)
	}
}

// TestMigrate_AnUnfinishedClaimIsNamed: a no-transaction file's claim
// left unfinished (its run died) stops a later run after claim_wait,
// naming the row to delete; a no-transaction file that fails removes its
// own claim, so a later run can apply it.
func TestMigrate_AnUnfinishedClaimIsNamed(t *testing.T) {
	h := newHarness(t)
	dir := writeMigrations(t, map[string]string{"1_a.sql": "CREATE TABLE a (id int);"})
	if _, err := migrate(h, false, map[string]any{"dir": dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2_idx.sql"), []byte("-- pleiades: no-transaction\nCREATE INDEX CONCURRENTLY a_id ON missing_table (id);"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(h, false, map[string]any{"dir": dir}); err == nil || !strings.Contains(err.Error(), "missing_table") {
		t.Fatalf("a failing no-transaction file = %v", err)
	}
	if got := h.value(t, "app", "SELECT count(*) FROM pleiades_migrations WHERE version = 2"); got != "0" {
		t.Fatal("a failed no-transaction file left its claim")
	}

	h.sql(t, "app", "INSERT INTO pleiades_migrations (version, name, checksum, finished) VALUES (2, 'idx', 'x', false)")
	_, err := migrate(h, false, map[string]any{"dir": dir, "claim_wait": "1s"})
	if err == nil || !strings.Contains(err.Error(), "claimed by a run that has not finished after 1s") || !strings.Contains(err.Error(), "delete its row") {
		t.Fatalf("an abandoned claim = %v", err)
	}
	stats, err := migrate(h, true, map[string]any{"dir": dir})
	if err != nil || !strings.Contains(strings.Join(stats["warnings"].([]string), "\n"), "2_idx.sql is claimed by a run that has not finished") {
		t.Errorf("a check over an abandoned claim = %v, %v", stats["warnings"], err)
	}
}

// TestMigrate_TheVersionTableNameIsAnIdentifier: a table name holding a
// quote is refused before any SQL is built, and one with capitals, a space
// and a semicolon is an identifier, quoted, never SQL.
func TestMigrate_TheVersionTableNameIsAnIdentifier(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app", "CREATE SCHEMA ops")
	dir := writeMigrations(t, map[string]string{"1_a.sql": "CREATE TABLE a (id int);"})
	for _, hostile := range []string{`ops.x"; DROP TABLE a; --`, "ops.a.b", "ops.\x01x"} {
		if _, err := migrate(h, false, map[string]any{"dir": dir, "table": hostile}); err == nil || !strings.Contains(err.Error(), "is not schema.name") {
			t.Errorf("table %q = %v", hostile, err)
		}
	}
	if _, err := migrate(h, false, map[string]any{"dir": dir, "table": "ops.Migrations; Log"}); err != nil {
		t.Fatal(err)
	}
	if got := h.value(t, "app", `SELECT count(*) FROM ops."Migrations; Log"`); got != "1" {
		t.Errorf("the version table holds %s rows", got)
	}
}
