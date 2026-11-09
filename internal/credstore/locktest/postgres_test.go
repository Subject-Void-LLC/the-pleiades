//go:build integration

// This file runs the type lock's two tests on PostgreSQL, the dialect where
// the lock is a row lock: read committed reads each statement afresh, so
// without it a type edit commits in the middle of a credential write.
package locktest_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/locktest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// The package's one PostgreSQL container, started on first use and
// terminated by TestMain, and the counter that names each test's own
// database on it.
var (
	postgresOnce      sync.Once
	postgresContainer *testpg.PostgresContainer
	postgresAdminDSN  string
	postgresErr       error
	databases         atomic.Int64
)

// TestMain runs the tests and then terminates the container, rather than
// leaving it to a reaper, which some container hosts run without.
func TestMain(m *testing.M) {
	code := m.Run()
	if postgresContainer != nil {
		_ = testcontainers.TerminateContainer(postgresContainer)
	}
	os.Exit(code)
}

// openPostgres opens a migrated database of the test's own on the package's
// container, the path the Controller takes (ent.OpenDatabase), so a test run
// twice (go test -count=2) or beside another finds nothing of theirs.
func openPostgres(t *testing.T) *ent.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the postgres backend in short mode")
	}
	ctx := context.Background()
	postgresOnce.Do(func() {
		postgresContainer, postgresErr = testpg.Run(ctx, testsupport.PostgresImage,
			testpg.WithDatabase("locktest"), testpg.WithUsername("pleiades"), testpg.WithPassword("pleiades"),
			testsupport.PostgresReady())
		if postgresErr != nil {
			// A container that started but failed its wait is still running;
			// terminate it here, since TestMain only stops one that started.
			_ = testcontainers.TerminateContainer(postgresContainer)
			postgresContainer = nil
			return
		}
		postgresAdminDSN, postgresErr = postgresContainer.ConnectionString(ctx, "sslmode=disable")
	})
	if postgresErr != nil {
		t.Fatalf("starting postgres: %v", postgresErr)
	}

	admin, err := sql.Open("postgres", postgresAdminDSN)
	if err != nil {
		t.Fatalf("opening the admin connection: %v", err)
	}
	defer admin.Close()
	// The name comes from this file's counter, never from input.
	name := fmt.Sprintf("locktest_%d", databases.Add(1))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating database %s: %v", name, err)
	}
	dsn, err := url.Parse(postgresAdminDSN)
	if err != nil {
		t.Fatalf("parsing the postgres DSN: %v", err)
	}
	dsn.Path = "/" + name

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn.String()})
	if err != nil {
		t.Fatalf("OpenDatabase() = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestTypeEditsRacingCredentialWritesNeverShowASecret_PostgreSQL races them.
func TestTypeEditsRacingCredentialWritesNeverShowASecret_PostgreSQL(t *testing.T) {
	store, orgID := locktest.HookedStore(t, openPostgres(t))
	locktest.RaceTypeEditsAgainstWrites(t, store, orgID, 3*time.Second)
}

// TestACredentialWriteHoldsTypeEditsOff_PostgreSQL lands one inside the
// other.
func TestACredentialWriteHoldsTypeEditsOff_PostgreSQL(t *testing.T) {
	store, orgID := locktest.HookedStore(t, openPostgres(t))
	locktest.HoldTypeEditsOff(t, store, orgID)
}

// TestReadsTakeOneSnapshot_PostgreSQL lands a type edit between a reader's
// two reads, which read committed sees whenever the two are not one
// snapshot.
func TestReadsTakeOneSnapshot_PostgreSQL(t *testing.T) {
	client := openPostgres(t)
	locktest.InterceptTypeReads(client)
	store, orgID := locktest.HookedStore(t, client)
	locktest.ReadsTakeOneSnapshot(t, store, orgID, locktest.Readers(client, store, orgID, resolve.NewEntResolver(client)))
}
