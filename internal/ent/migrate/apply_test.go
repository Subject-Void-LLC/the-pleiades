package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entmigrate "github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	_ "github.com/mattn/go-sqlite3"
)

// openRawTestDB opens a real, on-disk (not :memory:) SQLite database for
// exercising Apply directly against a *sql.DB, the same shape
// internal/ent's own OpenEmbedded uses in production. A real file, not
// :memory:, because one test below needs the connection to survive being
// reopened conceptually via a second raw statement after Apply already
// ran, and SQLite's :memory: databases do not share state across
// connections without WAL, which :memory: does not support.
func openRawTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := fmt.Sprintf("file:%s/apply-test.sqlite?_fk=1&_journal_mode=WAL&_busy_timeout=5000", dir)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("opening raw sqlite connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestApply_AppliesAndIsIdempotent proves the whole real mechanism: a
// fresh database migrates successfully, a second Apply against the same
// database is a clean no-op (not an error, not a duplicate schema), and
// the resulting schema is genuinely usable through a real ent client
// afterward.
func TestApply_AppliesAndIsIdempotent(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()

	if err := entmigrate.Apply(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := entmigrate.Apply(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("second Apply (should be a no-op): %v", err)
	}

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	if _, err := client.Device.Create().SetName("apply-test-device").SetType("linux_server").Save(ctx); err != nil {
		t.Fatalf("using the migrated schema: %v", err)
	}
}

// TestApply_UnsupportedDialectFailsClosed proves Apply refuses a dialect
// with no embedded migrations rather than silently doing nothing.
//
// The example dialect is MySQL, not Postgres. Postgres used to be the
// unsupported one, but it now has a real embedded migration set, and
// leaving this test pointed at it would have kept it green for the wrong
// reason: it runs against a SQLite database, so applying Postgres DDL
// would still error, at the DDL rather than at the dialect lookup this
// test exists to check. MySQL has no composition root and no migration
// set anywhere in this repository, so it is genuinely unsupported.
func TestApply_UnsupportedDialectFailsClosed(t *testing.T) {
	db := openRawTestDB(t)
	err := entmigrate.Apply(context.Background(), dialect.MySQL, db)
	if err == nil {
		t.Fatalf("expected an error for a dialect with no embedded migrations")
	}
	// Assert on the reason, not just on failure, so this cannot pass
	// because of an unrelated error the way the Postgres version would
	// have.
	if !strings.Contains(err.Error(), "no embedded migrations for dialect") {
		t.Fatalf("expected a no-embedded-migrations error, got: %v", err)
	}
}

// TestApply_FailsWhenDBIsAlreadyClosed proves Apply surfaces a real
// connection failure rather than panicking or silently succeeding.
func TestApply_FailsWhenDBIsAlreadyClosed(t *testing.T) {
	db := openRawTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("closing db: %v", err)
	}
	if err := entmigrate.Apply(context.Background(), dialect.SQLite, db); err == nil {
		t.Fatalf("expected an error when db is already closed")
	}
}

// TestApply_FailsWhenSchemaMigrationsTableIsMalformed forces a real
// failure in reading back recorded migrations: a schema_migrations table
// that already exists but without a version column makes
// ensureVersionTable's CREATE TABLE IF NOT EXISTS a no-op (the table is
// already there), so the real SELECT version FROM schema_migrations
// genuinely fails instead of being simulated.
func TestApply_FailsWhenSchemaMigrationsTableIsMalformed(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (bogus INTEGER)`); err != nil {
		t.Fatalf("seeding a malformed schema_migrations table: %v", err)
	}

	if err := entmigrate.Apply(ctx, dialect.SQLite, db); err == nil {
		t.Fatalf("expected Apply to fail reading a malformed schema_migrations table")
	}
}

// TestApply_FailsClosedOnUnrecognizedAppliedVersion is the startup
// schema-version gate, proven for real: a database with a migration this
// binary's embedded set does not recognize (simulating a newer binary
// having already migrated it further) must refuse to start, not silently
// proceed against a schema shape this binary was never verified against.
func TestApply_FailsClosedOnUnrecognizedAppliedVersion(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()

	if err := entmigrate.Apply(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		"9999_from_the_future.sql", time.Now().UTC(),
	); err != nil {
		t.Fatalf("seeding an unrecognized applied version: %v", err)
	}

	err := entmigrate.Apply(ctx, dialect.SQLite, db)
	if err == nil {
		t.Fatalf("expected Apply to refuse to start against an unrecognized applied migration")
	}
	t.Logf("refused as expected: %v", err)
}

// TestApply_FailsWhenMigrationScriptConflictsWithExistingSchema forces a
// real mid-script failure (RULE 0, not simulated): a devices table with
// an incompatible shape is created before Apply runs, so the embedded
// migration's own CREATE TABLE statement genuinely fails. It also proves
// applyOne's per-migration transaction is atomic: the failed migration
// must not be recorded as applied despite failing partway through its
// own script.
func TestApply_FailsWhenMigrationScriptConflictsWithExistingSchema(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE devices (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("seeding a conflicting devices table: %v", err)
	}

	if err := entmigrate.Apply(ctx, dialect.SQLite, db); err == nil {
		t.Fatalf("expected Apply to fail when its own migration's CREATE TABLE conflicts with existing schema")
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, "0001_initial.sql",
	).Scan(&count); err != nil {
		t.Fatalf("checking schema_migrations: %v", err)
	}
	if count != 0 {
		t.Fatalf("migration was recorded as applied despite failing mid-script, count = %d", count)
	}
}
