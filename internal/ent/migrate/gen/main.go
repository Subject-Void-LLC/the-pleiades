//go:build ignore

// Command gen produces the next numbered SQLite migration file for
// internal/ent's schema. It brings a fresh temp database up to date with
// every already-committed migration (the same internal/ent/migrate.Apply
// path OpenEmbedded uses at runtime), then diffs the current desired
// schema against that real prior state via ent's own already-generated
// Schema.WriteTo, capturing only the incremental DDL.
//
// Usage, after editing internal/ent/schema and running
// `go generate ./internal/ent`:
//
//	go run internal/ent/migrate/gen/main.go <name>
//
// <name> becomes the descriptive suffix of the new file, e.g. "initial"
// or "add_group_org". Prints "no schema changes to capture" and writes
// nothing if the schema already matches every committed migration.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	entmigrate "github.com/SubjectVoidLLC/the-pleiades/internal/ent/migrate"

	_ "github.com/mattn/go-sqlite3"
)

const migrationDir = "internal/ent/migrate/migrations/sqlite"

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: go run internal/ent/migrate/gen/main.go <name>")
		os.Exit(1)
	}
	name := os.Args[1]
	ctx := context.Background()

	// A real temp file, not :memory:, so the *sql.DB used to Apply prior
	// migrations and the *ent.Client used to diff the desired schema
	// below see the same durable state instead of two independent,
	// unrelated in-memory databases.
	tmp, err := os.CreateTemp("", "pleiades-migrate-gen-*.sqlite")
	if err != nil {
		fatal("creating temp database", err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		fatal("closing temp database file", err)
	}
	defer os.Remove(path)

	dsn := fmt.Sprintf("file:%s?_fk=1", path)

	rawDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		fatal("opening raw driver", err)
	}
	if err := entmigrate.Apply(ctx, "sqlite3", rawDB); err != nil {
		fatal("applying existing migrations", err)
	}
	if err := rawDB.Close(); err != nil {
		fatal("closing raw driver", err)
	}

	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		fatal("opening ent client", err)
	}
	defer client.Close()

	var buf bytes.Buffer
	if err := client.Schema.WriteTo(ctx, &buf); err != nil {
		fatal("diffing schema", err)
	}

	if buf.Len() == 0 {
		fmt.Println("no schema changes to capture; nothing written")
		return
	}

	next, err := nextVersion()
	if err != nil {
		fatal("determining next migration version", err)
	}
	filename := fmt.Sprintf("%04d_%s.sql", next, name)
	outPath := filepath.Join(migrationDir, filename)
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		fatal("writing migration file", err)
	}
	fmt.Println("wrote", outPath)
}

// nextVersion returns the next unused migration number, one past however
// many migration files already exist.
func nextVersion() (int, error) {
	entries, err := os.ReadDir(migrationDir)
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return len(names) + 1, nil
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "gen: %s: %v\n", step, err)
	os.Exit(1)
}
