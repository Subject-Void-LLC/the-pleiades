// This file provides the backend table the adapter conformance suite
// runs against, one entry per dialect OpenDatabase supports.
//
// Two adapters behind one port is only a claim until one suite drives
// both through identical call sequences, which is the shape
// internal/inventory's own repository conformance test and
// internal/event's bus conformance test already use.
//
// SQLite runs from a temp file and needs nothing external. PostgreSQL
// needs a real server, so it shares one container across the package and
// hands each test its own freshly created database inside it. A fresh
// database, rather than a shared one with a per-test schema, keeps the
// tests independent of search_path behavior entirely.
package ent_test

import (
	"context"
	stdsql "database/sql"
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
)

// TestMain guarantees the shared PostgreSQL container is torn down
// exactly once, after every test and benchmark in this package has run,
// instead of leaking it for the remainder of the process.
//
// The container is started lazily, at most once, by the first test that
// actually needs it (requireSharedPostgres), so a short-mode run that
// skips every PostgreSQL backend never starts one at all.
func TestMain(m *testing.M) {
	code := m.Run()
	terminateSharedPostgres()
	os.Exit(code)
}

// storeBackend is one dialect the conformance suite runs against.
type storeBackend struct {
	// name labels the subtest.
	name string

	// newDSN returns a DSN for a fresh, empty database. Every call must
	// return a database no other test has touched, so a failure in one
	// subtest cannot cascade into another.
	newDSN func(t *testing.T) string
}

var (
	// sharedPostgresOnce guards the one container this package starts.
	sharedPostgresOnce sync.Once

	// sharedPostgresAdminDSN is a DSN for the container's own initial
	// database, used only to issue CREATE DATABASE for each test.
	sharedPostgresAdminDSN string

	// sharedPostgresErr records a container start failure so every later
	// caller fails with the same real reason rather than a nil-pointer
	// dereference.
	sharedPostgresErr error

	// postgresDatabaseCounter names each per-test database uniquely.
	postgresDatabaseCounter atomic.Int64
)

// conformanceBackends returns every backend the suite runs against.
//
// PostgreSQL is skipped in short mode, matching this repository's
// established convention for a test that dials a real container.
func conformanceBackends() []storeBackend {
	return []storeBackend{
		{name: "sqlite", newDSN: newSQLiteConformanceDSN},
		{name: "postgres", newDSN: newPostgresConformanceDSN},
	}
}

// newSQLiteConformanceDSN returns a DSN for a fresh SQLite file under the
// test's own temp directory.
func newSQLiteConformanceDSN(t *testing.T) string {
	t.Helper()
	return "sqlite://" + filepath.Join(t.TempDir(), "conformance.db")
}

// newPostgresConformanceDSN creates a brand new database inside the
// shared container and returns a DSN for it.
func newPostgresConformanceDSN(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping postgres conformance backend in short mode")
	}

	adminDSN := requireSharedPostgres(t)

	admin, err := stdsql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatalf("opening the postgres admin connection: %v", err)
	}
	defer admin.Close()

	// A per-test database, so no test can observe another's rows and a
	// failure leaves nothing behind for the next one to trip over.
	name := fmt.Sprintf("conformance_%d", postgresDatabaseCounter.Add(1))
	// The identifier is built from a counter this file owns, never from
	// test input, so there is nothing here an injection could reach.
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating the per-test database %q: %v", name, err)
	}

	return replacePostgresDatabase(t, adminDSN, name)
}

// requireSharedPostgres starts the package's one PostgreSQL container on
// first use and returns a DSN for its initial database.
func requireSharedPostgres(t *testing.T) string {
	t.Helper()
	sharedPostgresOnce.Do(func() {
		ctx := context.Background()
		container, err := testpg.Run(ctx,
			testsupport.PostgresImage,
			testpg.WithDatabase("conformance"),
			testpg.WithUsername("pleiades"),
			testpg.WithPassword("pleiades"),
			testpg.BasicWaitStrategies(),
		)
		if err != nil {
			sharedPostgresErr = fmt.Errorf("starting the shared postgres container: %w", err)
			return
		}
		// Terminated by TestMain, after every test and benchmark in this
		// package has finished with it.
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

// sharedPostgresContainer is torn down once by TestMain. It is a package
// variable rather than a t.Cleanup so that the container outlives the
// first test that happened to start it.
var sharedPostgresContainer testcontainers.Container

// terminateSharedPostgres stops the shared container if one was started.
// TestMain calls it after m.Run returns.
func terminateSharedPostgres() {
	if sharedPostgresContainer != nil {
		_ = testcontainers.TerminateContainer(sharedPostgresContainer)
		sharedPostgresContainer = nil
	}
}

// replacePostgresDatabase rewrites a DSN's database name, so a per-test
// database reuses the container's host, port and credentials without this
// file reassembling a connection string by hand.
func replacePostgresDatabase(t *testing.T, dsn, database string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parsing the postgres DSN: %v", err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}
