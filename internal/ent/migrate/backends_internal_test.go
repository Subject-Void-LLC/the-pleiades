// This file gives this package's tests a fresh, real database of either
// dialect.
//
// The claims under test here (two starters racing one migration, a history
// table missing its key, a floor column added while another starter looks for
// it) are claims about what a real database engine does with concurrent
// transactions, so neither dialect is simulated. SQLite runs from a file in
// the test's own temp directory, opened with exactly the options production
// opens one with. PostgreSQL shares one container across the package and
// hands every test its own freshly created database inside it, the pattern
// internal/ent's conformance suite established.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// TestMain tears the shared PostgreSQL container down exactly once, after
// every test, benchmark and fuzz target in this package has finished with it.
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedPostgresContainer != nil {
		_ = testcontainers.TerminateContainer(sharedPostgresContainer)
	}
	os.Exit(code)
}

// dialects is every dialect this package migrates, in the driver-name form
// migrationSources is keyed by.
var dialects = []string{"sqlite3", "postgres"}

var (
	// sharedPostgresOnce guards the one container this package starts.
	sharedPostgresOnce sync.Once

	// sharedPostgresContainer is the container itself, torn down by TestMain.
	sharedPostgresContainer testcontainers.Container

	// sharedPostgresAdminDSN reaches the container's own initial database,
	// used only to create a database per test.
	sharedPostgresAdminDSN string

	// sharedPostgresErr records a failed start so every later caller fails
	// with the same real reason.
	sharedPostgresErr error

	// databaseCounter names each per-test database uniquely.
	databaseCounter atomic.Int64
)

// sqliteDSN returns the DSN production would build for a SQLite file at path
// (internal/ent/open_sqlite.go): foreign keys on, WAL, a five-second busy
// timeout. The race tests depend on those exact options, since the busy
// timeout is what a migration raises and restores.
func sqliteDSN(path string) string {
	escaped := (&url.URL{Path: path}).EscapedPath()
	return fmt.Sprintf("file:%s?_fk=1&_journal_mode=WAL&_busy_timeout=5000", escaped)
}

// freshDSN returns the DSN of a brand new, empty database of the named
// dialect. PostgreSQL is skipped in short mode, this repository's convention
// for a test that needs a container.
func freshDSN(t testing.TB, dialectName string) string {
	t.Helper()
	switch dialectName {
	case "sqlite3":
		return sqliteDSN(filepath.Join(t.TempDir(), "migrate.db"))
	case "postgres":
		if testing.Short() {
			t.Skip("skipping the postgres backend in short mode")
		}
		admin, err := sql.Open("postgres", requireSharedPostgres(t))
		if err != nil {
			t.Fatalf("opening the postgres admin connection: %v", err)
		}
		defer admin.Close()
		// The name comes from a counter this file owns, never from input.
		name := fmt.Sprintf("migrate_%d", databaseCounter.Add(1))
		if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
			t.Fatalf("creating the per-test database %q: %v", name, err)
		}
		parsed, err := url.Parse(sharedPostgresAdminDSN)
		if err != nil {
			t.Fatalf("parsing the postgres DSN: %v", err)
		}
		parsed.Path = "/" + name
		return parsed.String()
	default:
		t.Fatalf("no test backend for dialect %q", dialectName)
		return ""
	}
}

// openDSN opens a pool on dsn with the driver the dialect needs, closed when
// the test ends.
func openDSN(t testing.TB, dialectName, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(dialectName, dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dialectName, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// freshDB opens a brand new, empty database of the named dialect.
func freshDB(t testing.TB, dialectName string) *sql.DB {
	t.Helper()
	return openDSN(t, dialectName, freshDSN(t, dialectName))
}

// requireSharedPostgres starts the package's one PostgreSQL container on first
// use and returns a DSN for its initial database.
//
// max_connections is raised because the race tests start up to thirty-two
// processes against one server, each with its own small pool.
func requireSharedPostgres(t testing.TB) string {
	t.Helper()
	sharedPostgresOnce.Do(func() {
		ctx := context.Background()
		container, err := testpg.Run(ctx,
			testsupport.PostgresImage,
			testpg.WithDatabase("migrate"),
			testpg.WithUsername("pleiades"),
			testpg.WithPassword("pleiades"),
			testcontainers.WithCmdArgs("-c", "max_connections=300"),
			testsupport.PostgresReady(),
		)
		if err != nil {
			sharedPostgresErr = fmt.Errorf("starting the shared postgres container: %w", err)
			return
		}
		sharedPostgresContainer = container
		dsn, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedPostgresErr = fmt.Errorf("reading the shared postgres connection string: %w", err)
			return
		}
		sharedPostgresAdminDSN = dsn
	})
	if sharedPostgresErr != nil {
		t.Fatalf("%v", sharedPostgresErr)
	}
	return sharedPostgresAdminDSN
}

// forEachDialect runs fn once per dialect as a subtest.
func forEachDialect(t *testing.T, fn func(t *testing.T, dialectName string)) {
	t.Helper()
	for _, dialectName := range dialects {
		t.Run(dialectName, func(t *testing.T) { fn(t, dialectName) })
	}
}

// historyOf reads db's history, failing the test on error.
func historyOf(t testing.TB, db *sql.DB, dialectName string) []appliedRow {
	t.Helper()
	history, err := readHistory(context.Background(), db, migrationSources[dialectName])
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	return history
}

// namesOf lists a dialect's embedded migrations, failing the test on error.
func namesOf(t testing.TB, dialectName string) []string {
	t.Helper()
	names, err := migrationNames(migrationSources[dialectName])
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	return names
}
