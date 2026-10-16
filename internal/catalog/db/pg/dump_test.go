// Tests for db.pg.dump, db.pg.restore and db.pg.rowcounts against a real
// PostgreSQL, with the real pg_dump and pg_restore run on the device: the
// backup, restore and verify loop they exist for.
package pg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// dumpRestoreFixture is a source database with two tables and a
// partitioned one, and a directory on the device for archives.
func dumpRestoreFixture(t *testing.T) (*harness, string) {
	t.Helper()
	requireTools(t)
	h := newHarness(t)
	h.sql(t, "app",
		"CREATE TABLE orders (id int PRIMARY KEY, note text)",
		"INSERT INTO orders SELECT g, 'n' || g FROM generate_series(1, 25) g",
		"CREATE TABLE events (at date NOT NULL, what text) PARTITION BY RANGE (at)",
		"CREATE TABLE events_2026 PARTITION OF events FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
		"INSERT INTO events VALUES ('2026-03-01', 'a'), ('2026-04-01', 'b')",
		"CREATE SCHEMA audit",
		"CREATE TABLE audit.log (id int)",
		"INSERT INTO audit.log VALUES (1)")
	return h, t.TempDir()
}

// TestDumpRestoreRowcounts_TheLoop: a check of a dump writes nothing; the
// dump writes a new archive and records its removal; a check of a restore
// creates nothing; the restore creates the target and fills it in one
// transaction; rowcounts finds the two equal, counting the partitioned
// table once; and a changed source is caught.
func TestDumpRestoreRowcounts_TheLoop(t *testing.T) {
	h, dir := dumpRestoreFixture(t)
	ctx := context.Background()
	dest := filepath.Join(dir, "app.dump")
	dumpParams := params(map[string]any{"connection": "admin", "database": "app", "dest": dest})

	if _, err := pg.CheckDump(ctx, h.fresh(), h.device, dumpParams); err != nil {
		t.Fatalf("a check of the dump: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a check of the dump wrote %d file(s)", len(entries))
	}
	rc := h.fresh()
	if _, err := pg.Dump(ctx, rc, h.device, dumpParams); err != nil {
		t.Fatalf("the dump: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Size() == 0 || rc.stats["size"] != info.Size() || len(rc.stats["sha256"].(string)) != 64 {
		t.Fatalf("the archive: %v, %v; stats %v", info, err, rc.stats)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the dump left %d files beside the archive", len(entries)-1)
	}
	inverse := rc.stats["inverse"].(map[string]any)
	if inverse["fqcn"] != "file.remove" || inverse["params"].(map[string]any)["path"] != dest {
		t.Errorf("the undo = %v", inverse)
	}
	if _, err := pg.Dump(ctx, h.fresh(), h.device, dumpParams); err == nil || !strings.Contains(err.Error(), "never replaces an archive") {
		t.Errorf("a second dump to the same name = %v", err)
	}

	restoreParams := params(map[string]any{"connection": "admin", "database": "app_copy", "src": dest, "create": true})
	if _, err := pg.CheckRestore(ctx, h.fresh(), h.device, restoreParams); err != nil {
		t.Fatalf("a check of the restore: %v", err)
	}
	if got := h.value(t, "postgres", "SELECT count(*) FROM pg_database WHERE datname = 'app_copy'"); got != "0" {
		t.Fatal("a check of the restore created the database")
	}
	rc = h.fresh()
	if _, err := pg.Restore(ctx, rc, h.device, restoreParams); err != nil {
		t.Fatalf("the restore: %v", err)
	}
	if rc.stats["created"] != true || rc.stats["toc_entries"].(int) == 0 {
		t.Errorf("restore stats = %v", rc.stats)
	}

	rc = h.fresh()
	if _, err := pg.Rowcounts(ctx, rc, h.device, params(map[string]any{"connection": "admin", "database": "app_copy", "compare": map[string]any{"database": "app"}})); err != nil {
		t.Fatalf("rowcounts of the copy: %v", err)
	}
	tables := rc.stats["tables"].([]any)
	byName := map[string]any{}
	for _, e := range tables {
		m := e.(map[string]any)
		byName[m["table"].(string)] = m["rows"]
	}
	if len(tables) != 3 || byName["public.orders"] != int64(25) || byName["public.events"] != int64(2) || byName["audit.log"] != int64(1) {
		t.Errorf("tables = %v", tables)
	}

	h.sql(t, "app", "DELETE FROM orders WHERE id > 20", "CREATE TABLE extra (id int)")
	_, err = pg.Rowcounts(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app_copy", "compare": map[string]any{"database": "app"}}))
	if err == nil || !strings.Contains(err.Error(), "public.orders: 25 here, 20 in app") || !strings.Contains(err.Error(), "public.extra: only in app") {
		t.Errorf("rowcounts after the source changed = %v", err)
	}
	rc = h.fresh()
	if _, err := pg.Rowcounts(ctx, rc, h.device, params(map[string]any{"connection": "admin", "database": "app", "schemas": []any{"public"}, "exclude": []any{"public.extra"}})); err != nil || len(rc.stats["tables"].([]any)) != 2 {
		t.Errorf("rowcounts scoped to public without extra = %v, %v", rc.stats["tables"], err)
	}
	// A schema named but absent (a typo, or a case PostgreSQL folded) would
	// count no tables on either side and agree.
	_, err = pg.Rowcounts(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app_copy", "schemas": []any{"Public"}, "compare": map[string]any{"database": "app"}}))
	if err == nil || !strings.Contains(err.Error(), `schemas names "Public", which app_copy does not have`) {
		t.Errorf("rowcounts of a schema that does not exist = %v", err)
	}
}

// TestRowcounts_RefusesAnUnknownCompareKey: a misspelled key under compare
// (conection) would compare on the counted side's own connection instead.
func TestRowcounts_RefusesAnUnknownCompareKey(t *testing.T) {
	rc := &stubContext{connections: []wire.DatabaseConnection{{Name: "admin", Host: "127.0.0.1", User: "u", SSLMode: wire.SSLDisable}}, stats: map[string]any{}}
	_, err := pg.Rowcounts(context.Background(), rc, nil, params(map[string]any{"connection": "admin", "database": "app", "compare": map[string]any{"database": "app_copy", "conection": "other"}}))
	if err == nil || !strings.Contains(err.Error(), `compare: "conection" is not database or connection`) {
		t.Errorf("Rowcounts = %v", err)
	}
}

// TestDump_AFailedReadBackKeepsTheUndo: once the archive is under its
// name, reading back its size and hash failing is a warning, not a
// failure, since the engine drops a failed task's undo and the archive
// would then be left with nothing able to remove it.
func TestDump_AFailedReadBackKeepsTheUndo(t *testing.T) {
	h, dir := dumpRestoreFixture(t)
	t.Cleanup(pg.SetReadBackForTest(func(context.Context, *remoteexec.Conn, string) (int64, string, error) {
		return 0, "", errors.New("the session dropped")
	}))
	dest := filepath.Join(dir, "app.dump")
	rc := h.fresh()
	if _, err := pg.Dump(context.Background(), rc, h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": dest})); err != nil {
		t.Fatalf("the dump failed with its archive written: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the archive: %v", err)
	}
	inverse, _ := rc.stats["inverse"].(map[string]any)
	if inverse["fqcn"] != "file.remove" || inverse["params"].(map[string]any)["path"] != dest {
		t.Errorf("the undo = %v", rc.stats["inverse"])
	}
	if warnings, _ := rc.stats["warnings"].([]string); !strings.Contains(strings.Join(warnings, "\n"), "reading back its size and SHA-256 failed: the session dropped") {
		t.Errorf("warnings = %v", rc.stats["warnings"])
	}
}

// TestRestore_RefusesWhatWouldDestroySomething: a live database (by its
// connection or by its own setting) unless told why; a database holding
// tables unless clean; a database that does not exist unless create; a
// file that is not an archive. A failed restore into a database it
// created leaves no database behind. A name pg_restore would read as a
// connection string, were it handed over bare, is restored into exactly as
// written.
func TestRestore_RefusesWhatWouldDestroySomething(t *testing.T) {
	h, dir := dumpRestoreFixture(t)
	ctx := context.Background()
	dest := filepath.Join(dir, "app.dump")
	if _, err := pg.Dump(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": dest})); err != nil {
		t.Fatal(err)
	}
	h.connect("guarded", adminUser, adminPassword, "app")
	h.sql(t, "postgres", "CREATE DATABASE marked", "ALTER DATABASE marked SET pleiades.live = on", "CREATE DATABASE empty")
	notArchive := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notArchive, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"live by its connection":  {map[string]any{"connection": "guarded", "database": "app", "src": dest, "clean": true}, "connection guarded lists it as live"},
		"live by its own setting": {map[string]any{"connection": "admin", "database": "marked", "src": dest}, "pleiades.live = on"},
		"holding tables":          {map[string]any{"connection": "admin", "database": "app", "src": dest}, "already holds"},
		"absent without create":   {map[string]any{"connection": "admin", "database": "nowhere", "src": dest}, "set create"},
		"not an archive":          {map[string]any{"connection": "admin", "database": "empty", "src": notArchive}, "is not an archive"},
		"a reason without allow":  {map[string]any{"connection": "admin", "database": "empty", "src": dest, "live_reason": "x"}, "means nothing without allow_live"},
		"allow without a reason":  {map[string]any{"connection": "admin", "database": "marked", "src": dest, "allow_live": true}, "needs live_reason"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := pg.Restore(ctx, h.fresh(), h.device, params(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Restore = %v, want %q", err, tc.want)
			}
		})
	}

	rc := h.fresh()
	if _, err := pg.Restore(ctx, rc, h.device, params(map[string]any{"connection": "admin", "database": "marked", "src": dest, "allow_live": true, "live_reason": "refreshing the demo copy"})); err != nil {
		t.Fatalf("a live database, told why: %v", err)
	}
	if rc.stats["live_override"] != "refreshing the demo copy" || !strings.Contains(strings.Join(rc.stats["warnings"].([]string), "\n"), "restoring over live database marked") {
		t.Errorf("the override was not recorded: %v", rc.stats)
	}

	// pg_restore reads a --dbname holding "=" as a connection string, which
	// could name another host and send it the password; this one would
	// restore into app instead. It is only ever a name.
	const literal = `dbname=app o'b\x`
	if _, err := pg.Restore(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": literal, "src": dest, "create": true})); err != nil {
		t.Fatalf("restoring into %q: %v", literal, err)
	}
	if got := h.value(t, literal, "SELECT count(*) FROM orders"); got != "25" {
		t.Errorf("%q holds %s orders, want 25", literal, got)
	}

	// An archive whose objects belong to a role that is gone: restored with
	// its owners kept, it fails, and the database it created goes with it.
	h.sql(t, "app", "CREATE ROLE departed", "CREATE TABLE owned (id int)", "ALTER TABLE owned OWNER TO departed")
	owned := filepath.Join(dir, "owned.dump")
	if _, err := pg.Dump(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "app", "dest": owned})); err != nil {
		t.Fatal(err)
	}
	h.sql(t, "app", "DROP TABLE owned", "DROP ROLE departed")
	_, err := pg.Restore(ctx, h.fresh(), h.device, params(map[string]any{"connection": "admin", "database": "doomed", "src": owned, "create": true, "no_owner": false}))
	if err == nil || !strings.Contains(err.Error(), "the database this run created was dropped") {
		t.Fatalf("a failing restore = %v", err)
	}
	if got := h.value(t, "postgres", "SELECT count(*) FROM pg_database WHERE datname = 'doomed'"); got != "0" {
		t.Error("the failed restore left its database")
	}
}
