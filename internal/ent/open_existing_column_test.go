// Package ent_test: a column an older schema lacks reads as empty, as a table
// it lacks does. The upgrade's backup reads the database before any migration
// runs, so a column added since is missing from every database it opens.
package ent_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

func TestOpenExisting_AColumnTheSchemaLacksReadsAsEmpty(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	// A table from before runner_seed existed: it has seed, and no runner_seed.
	if _, err := raw.ExecContext(ctx, `CREATE TABLE mesh_signing_keys (id integer PRIMARY KEY, seed text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO mesh_signing_keys (id, seed) VALUES (1, 'SAAAAA')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	db, err := ent.OpenExisting(ctx, "sqlite://"+path)
	if err != nil {
		t.Fatalf("OpenExisting error = %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.StoredValues(ctx, "mesh_signing_keys", "runner_seed")
	if err != nil {
		t.Fatalf("StoredValues on a column the schema lacks = %v; want no rows and no error", err)
	}
	if rows != nil {
		t.Fatalf("StoredValues on a column the schema lacks returned %v, want none", rows)
	}
	// The column that does exist still reads.
	seeds, err := db.StoredValues(ctx, "mesh_signing_keys", "seed")
	if err != nil || len(seeds) != 1 {
		t.Fatalf("StoredValues(seed) = %v, %v; want the one stored row", seeds, err)
	}
}
