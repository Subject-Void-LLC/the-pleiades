// The Controller's fact store on PostgreSQL, its production dialect, through
// the versioned migrations a real deployment applies (LESSONS 250): the
// conformance suite every store runs, and a chain built from times finer
// than PostgreSQL keeps (the reviewer's F2: timestamptz drops nanoseconds,
// and the chain hashes the time).
package entstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// The package's one PostgreSQL container, started on first use.
var (
	pgOnce  sync.Once
	pgAdmin string
	pgErr   error
	pgCount int
	pgMu    sync.Mutex
)

// postgresStore returns a store over a fresh database migrated the way the
// Controller migrates one, and one device in it.
func postgresStore(t *testing.T) (facts.Store, string) {
	t.Helper()
	client, device := postgresClient(t)
	return entstore.New(client), device
}

// postgresClient returns a client over a fresh database migrated the way the
// Controller migrates one, and the ID of one device in it.
func postgresClient(t *testing.T) (*ent.Client, string) {
	t.Helper()
	client, device, _ := postgresClientDB(t)
	return client, device
}

// postgresClientDB is postgresClient with the database handle under the
// client, for a test that changes the database itself.
func postgresClientDB(t *testing.T) (*ent.Client, string, *sql.DB) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a PostgreSQL container")
	}
	pgOnce.Do(func() {
		ctx := context.Background()
		container, err := testpg.Run(ctx, testsupport.PostgresImage,
			testpg.WithDatabase("facts"), testpg.WithUsername("pleiades"), testpg.WithPassword("pleiades"),
			testsupport.PostgresReady())
		if err != nil {
			_ = testcontainers.TerminateContainer(container)
			pgErr = fmt.Errorf("starting postgres: %w", err)
			return
		}
		pgAdmin, pgErr = container.ConnectionString(ctx, "sslmode=disable")
	})
	if pgErr != nil {
		t.Fatal(pgErr)
	}
	pgMu.Lock()
	pgCount++
	name := fmt.Sprintf("facts_%d", pgCount)
	pgMu.Unlock()
	admin, err := sql.Open("postgres", pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	dsn := strings.Replace(pgAdmin, "/facts?", "/"+name+"?", 1)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.Apply(context.Background(), "postgres", db); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", db)))
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
	return client, dev.DeviceID, db
}

// TestStore_ConformanceOnPostgres holds the Controller's store to the rules
// both stores keep, on PostgreSQL, including a chain built from a time with
// nanoseconds, which verifies only because both stores keep a time to the
// microsecond before hashing it.
func TestStore_ConformanceOnPostgres(t *testing.T) {
	factstest.Run(t, postgresStore)
}
