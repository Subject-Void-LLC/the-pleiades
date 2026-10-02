//go:build ignore

// Command gen produces the next numbered migration file for one dialect
// of internal/ent's schema. It brings a fresh throwaway database up to
// date with every already-committed migration for that dialect (the same
// internal/ent/migrate.Apply path ent.Open uses at runtime), then diffs
// the current desired schema against that real prior state via ent's own
// already-generated Schema.WriteTo, capturing only the incremental DDL.
//
// Usage, after editing internal/ent/schema and running
// `go generate ./internal/ent`:
//
//	go run internal/ent/migrate/gen/main.go sqlite   <name>
//	go run internal/ent/migrate/gen/main.go postgres <name>
//
// <name> becomes the descriptive suffix of the new file, e.g. "initial"
// or "add_group_org". Prints "no schema changes to capture" and writes
// nothing if the schema already matches every committed migration.
//
// A schema change is not complete until it has been generated for EVERY
// dialect in migrationSources. internal/ent/migrate/parity_test.go is
// what catches a dialect left behind.
//
// The two dialects differ in how the throwaway database is obtained.
// SQLite uses a temp file. Postgres needs a real server, so this tool
// starts an ephemeral container by default, which is both reproducible
// and incapable of damaging a database somebody cares about. Set
// PLEIADES_MIGRATE_GEN_DSN to point at your own empty Postgres instead;
// the tool then assumes that database is disposable and does not clean
// up after itself.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"entgo.io/ent/dialect/sql/schema"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entmigrate "github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// postgresDSNEnv names the environment variable that overrides the
// ephemeral container with a caller-supplied, disposable Postgres.
const postgresDSNEnv = "PLEIADES_MIGRATE_GEN_DSN"

// dialectTarget describes everything this tool needs to know about one
// dialect: the driver name shared with migrationSources, where its
// migration files live, and how to obtain an empty database to diff
// against.
type dialectTarget struct {
	// driver is the database/sql driver name, and the same key
	// internal/ent/migrate.migrationSources is indexed by.
	driver string

	// dir is the repo-relative directory holding this dialect's
	// committed migration files.
	dir string

	// newDatabase returns a DSN for a fresh, empty database plus a
	// cleanup function. The database must be genuinely empty: this tool
	// treats whatever it finds there as the prior migrated state.
	newDatabase func(ctx context.Context) (string, func(), error)
}

// targets maps the dialect name a caller types to its target. The keys
// are the short, human-facing names ("sqlite", not "sqlite3").
var targets = map[string]dialectTarget{
	"sqlite": {
		driver:      "sqlite3",
		dir:         "internal/ent/migrate/migrations/sqlite",
		newDatabase: newSQLiteDatabase,
	},
	"postgres": {
		driver:      "postgres",
		dir:         "internal/ent/migrate/migrations/postgres",
		newDatabase: newPostgresDatabase,
	},
}

func main() {
	if len(os.Args) != 3 || os.Args[1] == "" || os.Args[2] == "" {
		fmt.Fprintln(os.Stderr, "usage: go run internal/ent/migrate/gen/main.go <sqlite|postgres> <name>")
		os.Exit(1)
	}
	dialect, name := os.Args[1], os.Args[2]

	target, ok := targets[dialect]
	if !ok {
		fatal("resolving dialect", fmt.Errorf("unknown dialect %q, want one of sqlite, postgres", dialect))
	}
	ctx := context.Background()

	dsn, cleanup, err := target.newDatabase(ctx)
	if err != nil {
		fatal("provisioning a throwaway database", err)
	}
	defer cleanup()

	// Bring the throwaway database up to the committed state, so the diff
	// below captures only what this schema edit adds. Skipped entirely on
	// the bootstrap run, when a dialect has no committed migrations yet
	// and Apply would (correctly) refuse a dialect it has no embedded set
	// for. An empty database is already the right prior state there, and
	// the diff comes out as one squashed initial migration.
	committed, err := committedMigrationCount(target.dir)
	if err != nil {
		fatal("reading committed migrations", err)
	}
	if committed > 0 {
		rawDB, err := sql.Open(target.driver, dsn)
		if err != nil {
			fatal("opening raw driver", err)
		}
		if err := entmigrate.Apply(ctx, target.driver, rawDB); err != nil {
			fatal("applying existing migrations", err)
		}
		if err := rawDB.Close(); err != nil {
			fatal("closing raw driver", err)
		}
	}

	client, err := ent.Open(target.driver, dsn)
	if err != nil {
		fatal("opening ent client", err)
	}
	defer client.Close()

	var buf bytes.Buffer
	// WithDropColumn(true): ent's own default is to never emit a DROP
	// COLUMN, so a schema edit that removes a field (e.g. Phase 8 deleting
	// User.role) would otherwise diff clean while silently leaving the old
	// column behind. This tool exists to capture the real, full diff.
	if err := client.Schema.WriteTo(ctx, &buf, schema.WithDropColumn(true)); err != nil {
		fatal("diffing schema", err)
	}

	if buf.Len() == 0 {
		fmt.Println("no schema changes to capture; nothing written")
		return
	}

	filename := fmt.Sprintf("%04d_%s.sql", committed+1, name)
	outPath := filepath.Join(target.dir, filename)
	if err := os.MkdirAll(target.dir, 0o750); err != nil {
		fatal("creating migration directory", err)
	}
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		fatal("writing migration file", err)
	}
	fmt.Println("wrote", outPath)
}

// newSQLiteDatabase returns a DSN for a fresh temp SQLite file.
//
// A real temp file, not :memory:, so the *sql.DB used to Apply prior
// migrations and the *ent.Client used to diff the desired schema see the
// same durable state instead of two independent, unrelated in-memory
// databases.
func newSQLiteDatabase(context.Context) (string, func(), error) {
	tmp, err := os.CreateTemp("", "pleiades-migrate-gen-*.sqlite")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp database: %w", err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", nil, fmt.Errorf("closing temp database file: %w", err)
	}
	return fmt.Sprintf("file:%s?_fk=1", path), func() { _ = os.Remove(path) }, nil
}

// newPostgresDatabase returns a DSN for an empty Postgres database.
//
// It prefers PLEIADES_MIGRATE_GEN_DSN when set, and otherwise starts an
// ephemeral container pinned to the same image the test suite uses
// (internal/testsupport.PostgresImage), so the generated DDL is produced
// against the same server version the conformance and integration suites
// later verify it against.
func newPostgresDatabase(ctx context.Context) (string, func(), error) {
	if dsn := os.Getenv(postgresDSNEnv); dsn != "" {
		return dsn, func() {}, nil
	}

	container, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades_migrate_gen"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("pleiades"),
		testpg.BasicWaitStrategies(),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(container) // a failed start still returns its container
		return "", nil, fmt.Errorf("starting an ephemeral postgres container (set %s to use your own disposable database instead): %w", postgresDSNEnv, err)
	}
	cleanup := func() { _ = testcontainers.TerminateContainer(container) }

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("reading the container connection string: %w", err)
	}
	return dsn, cleanup, nil
}

// committedMigrationCount counts the .sql files already committed for a
// dialect. A missing directory counts as zero, which is the bootstrap
// case for a dialect being added for the first time.
func committedMigrationCount(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return len(names), nil
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "gen: %s: %v\n", step, err)
	os.Exit(1)
}
