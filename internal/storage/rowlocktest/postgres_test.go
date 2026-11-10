//go:build integration

// This file runs the row-lock scenario (SecondFoldWaits) on PostgreSQL for
// the two stores whose PATCH routes fold under a row lock: inventories and
// announcements. PostgreSQL is the dialect where the lock is a row lock and
// read committed reads each statement afresh, so without the lock the
// second update reads the row before the first commits.
package rowlocktest_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage/rowlocktest"
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
			testpg.WithDatabase("rowlocktest"), testpg.WithUsername("pleiades"), testpg.WithPassword("pleiades"),
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
	name := fmt.Sprintf("rowlocktest_%d", databases.Add(1))
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

// TestInventoryUpdatesNeverInterleave_PostgreSQL holds a rename inside its
// fold while a second update removes a device, and requires the second to
// fold over the rename, so the result keeps both changes.
func TestInventoryUpdatesNeverInterleave_PostgreSQL(t *testing.T) {
	client := openPostgres(t)
	ctx := context.Background()
	store := inventory.NewEntSetStore(client)
	org := client.Organization.Create().SetName("acme").SaveX(ctx).ID
	keep := client.Device.Create().SetName("keep").SetType("linux_server").SetOrganizationID(org).SaveX(ctx).ID
	revoke := client.Device.Create().SetName("revoke").SetType("linux_server").SetOrganizationID(org).SaveX(ctx).ID
	created, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org, DeviceIDs: []int{keep, revoke}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var sawName string
	rowlocktest.SecondFoldWaits(t,
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
				onFold()
				s.Name = "edge-routers"
				return s, nil
			})
			return err
		},
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
				onFold()
				sawName = s.Name
				s.DeviceIDs = []int{keep}
				return s, nil
			})
			return err
		})

	if sawName != "edge-routers" {
		t.Errorf("the second fold saw name %q, want the first update's edge-routers", sawName)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "edge-routers" || !slices.Equal(got.DeviceIDs, []int{keep}) {
		t.Errorf("after both updates: name %q devices %v, want edge-routers and [%d]", got.Name, got.DeviceIDs, keep)
	}
}

// TestAnnouncementUpdatesNeverInterleave_PostgreSQL holds a title change
// inside its fold while a second update raises the level, and requires the
// second to fold over the new title, so both changes land.
func TestAnnouncementUpdatesNeverInterleave_PostgreSQL(t *testing.T) {
	client := openPostgres(t)
	ctx := context.Background()
	store := announce.NewEntStore(client)
	created, err := store.Create(ctx, announce.Announcement{Title: "Freeze", Body: "No dispatches.", Author: "alice"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var sawTitle string
	rowlocktest.SecondFoldWaits(t,
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
				onFold()
				a.Title = "Freeze, extended"
				return a, nil
			})
			return err
		},
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(a announce.Announcement) (announce.Announcement, error) {
				onFold()
				sawTitle = a.Title
				a.Level = announce.LevelCritical
				return a, nil
			})
			return err
		})

	if sawTitle != "Freeze, extended" {
		t.Errorf("the second fold saw title %q, want the first update's", sawTitle)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "Freeze, extended" || got.Level != announce.LevelCritical {
		t.Errorf("after both updates: %+v, want the first's title and the second's level", got)
	}
}
