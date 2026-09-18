// Tests for reading an existing database without migrating or creating
// anything, on SQLite and PostgreSQL.
package ent_test

import (
	"context"
	stdsql "database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// TestOpenExisting_MissingSQLiteFileCreatesNothing proves the one property
// the setup command's census depends on most: asking whether a database
// holds anything must not answer by creating one. OpenDatabase would make
// the parent directory and the file; OpenExisting must make neither.
func TestOpenExisting_MissingSQLiteFileCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-yet")
	path := filepath.Join(dir, "controller.db")

	for _, dsn := range []string{"sqlite://" + path, path} {
		db, err := ent.OpenExisting(context.Background(), dsn)
		if !errors.Is(err, ent.ErrNoDatabase) {
			if db != nil {
				_ = db.Close()
			}
			t.Fatalf("OpenExisting(%q) error = %v, want ErrNoDatabase", dsn, err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("OpenExisting created %s (stat error = %v); a census must not create what it counts", dir, err)
	}
}

// TestOpenExisting_UnreachableServerFailsAtOpen proves a database that does
// not answer fails when it is opened, rather than opening and then reading
// as though it held nothing.
func TestOpenExisting_UnreachableServerFailsAtOpen(t *testing.T) {
	db, err := ent.OpenExisting(context.Background(), "postgres://pleiades:not-a-real-password@127.0.0.1:1/pleiades?sslmode=disable")
	if err == nil {
		_ = db.Close()
		t.Fatal("OpenExisting() against a closed port returned no error")
	}
	if errors.Is(err, ent.ErrNoDatabase) {
		t.Fatalf("OpenExisting() error = %v; an unreachable server is not the same answer as no database", err)
	}
	if strings.Contains(err.Error(), "not-a-real-password") {
		t.Fatalf("OpenExisting() error %q contains the DSN's password", err)
	}
}

// TestOpenExisting_ReadsRawValuesAndNeverMigrates runs against both
// dialects. It seeds a migrated database through the ordinary path, then
// checks that OpenExisting reads exactly what is stored, treats a missing
// table as empty, refuses a name that is not a plain identifier, and leaves
// a never-migrated database unmigrated.
func TestOpenExisting_ReadsRawValuesAndNeverMigrates(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()

			// A database nothing has migrated yet: reading it must leave it
			// that way. On SQLite that means a file that exists and is empty.
			emptyDSN := backend.newDSN(t)
			if strings.HasPrefix(emptyDSN, "sqlite://") {
				if err := os.WriteFile(strings.TrimPrefix(emptyDSN, "sqlite://"), nil, 0o600); err != nil {
					t.Fatalf("creating an empty SQLite file: %v", err)
				}
			}
			empty, err := ent.OpenExisting(ctx, emptyDSN)
			if err != nil {
				t.Fatalf("OpenExisting(empty) error = %v", err)
			}
			rows, err := empty.StoredValues(ctx, "devices", "properties")
			if err != nil || rows != nil {
				t.Fatalf("StoredValues on a never-migrated database = %v, %v; want no rows and no error", rows, err)
			}
			if rows, err := empty.StoredValues(ctx, "schema_migrations", "version"); err != nil || rows != nil {
				t.Fatalf("schema_migrations exists after OpenExisting (%v, %v): it migrated", rows, err)
			}
			_ = empty.Close()

			// A migrated database with one row in it.
			dsn := backend.newDSN(t)
			client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
			if err != nil {
				t.Fatalf("OpenDatabase() error = %v", err)
			}
			seeded := client.Device.Create().
				SetName("router-1").SetType("cisco_router").
				SetProperties(map[string]any{"_encrypted": "stored-as-is"}).
				SaveX(ctx)
			client.Device.Create().SetName("router-2").SetType("cisco_router").SaveX(ctx)
			_ = client.Close()

			db, err := ent.OpenExisting(ctx, dsn)
			if err != nil {
				t.Fatalf("OpenExisting() error = %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			got, err := db.StoredValues(ctx, "devices", "properties")
			if err != nil {
				t.Fatalf("StoredValues() error = %v", err)
			}
			if len(got) != 1 || got[0].ID != seeded.ID || !strings.Contains(got[0].Value, "stored-as-is") {
				t.Fatalf("StoredValues() = %+v, want the one device with properties, as stored", got)
			}

			if rows, err := db.StoredValues(ctx, "no_such_table", "anything"); err != nil || rows != nil {
				t.Fatalf("StoredValues(missing table) = %v, %v; want no rows and no error", rows, err)
			}
			if _, err := db.StoredValues(ctx, "devices; DROP TABLE devices", "properties"); err == nil {
				t.Fatal("StoredValues accepted a table name that is not a plain identifier")
			}
			// The postgres backend's password is "pleiades"; the description
			// must name the database without it.
			if db.Describe() == "" || strings.Contains(db.Describe(), ":pleiades@") {
				t.Fatalf("Describe() = %q, want a name without the password", db.Describe())
			}
		})
	}
}

// TestOpenExisting_DoesNotChangeTheSchemaVersion proves reading a migrated
// database leaves its recorded migration history exactly as it was.
func TestOpenExisting_DoesNotChangeTheSchemaVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "versioned.db")
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: "sqlite://" + path})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	_ = client.Close()

	count := func() int {
		raw, err := stdsql.Open("sqlite3", path)
		if err != nil {
			t.Fatalf("sql.Open() error = %v", err)
		}
		defer func() { _ = raw.Close() }()
		var n int
		if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil {
			t.Fatalf("counting schema_migrations: %v", err)
		}
		return n
	}
	before := count()

	db, err := ent.OpenExisting(ctx, "sqlite://"+path)
	if err != nil {
		t.Fatalf("OpenExisting() error = %v", err)
	}
	if _, err := db.StoredValues(ctx, "credentials", "inputs"); err != nil {
		t.Fatalf("StoredValues() error = %v", err)
	}
	_ = db.Close()

	if after := count(); after != before {
		t.Fatalf("schema_migrations went from %d to %d rows across a read", before, after)
	}
}
