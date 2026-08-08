package ent_test

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entmigrate "github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	_ "github.com/mattn/go-sqlite3"
)

// openMigratedTestClient opens an in-memory SQLite database through the
// real production path this repository actually ships (raw driver, then
// internal/ent/migrate.Apply's committed migration files, then the ent
// client wrapping that same connection), instead of enttest.Open's
// auto-migration shortcut. Per this project's own RULE 0 ("a green unit
// test only counts when it runs the SAME config the platform runs"), any
// test whose own purpose is to prove the platform's real mechanism works
// -- this Release Gate included -- must exercise that mechanism, not a
// shortcut that happens to produce an equivalent schema.
func openMigratedTestClient(t *testing.T) *ent.Client {
	t.Helper()

	// A unique shared-cache name per test keeps parallel subtests from
	// colliding on one in-memory database.
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())

	db, err := stdsql.Open(dialect.SQLite, dsn)
	if err != nil {
		t.Fatalf("opening raw sqlite connection: %v", err)
	}

	if err := entmigrate.Apply(context.Background(), dialect.SQLite, db); err != nil {
		_ = db.Close()
		t.Fatalf("applying versioned migrations: %v", err)
	}

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := ent.NewClient(ent.Driver(drv))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestGraphTraversal(t *testing.T) {
	// Spin up an in-memory SQLite database, migrated through the real
	// production path (openMigratedTestClient), for the Release Gate.
	client := openMigratedTestClient(t)

	ctx := context.Background()

	// 1. Create a parent (e.g. Core Switch)
	coreSwitch, err := client.Device.Create().
		SetName("core-sw-01").
		SetType("network_device").
		SetProperties(map[string]interface{}{"host": "10.0.0.1", "role": "core"}).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed creating core switch: %v", err)
	}

	// 2. Create a child (e.g. Access Switch) and attach it to the parent
	accessSwitch, err := client.Device.Create().
		SetName("access-sw-01").
		SetType("network_device").
		SetProperties(map[string]interface{}{"host": "10.0.1.1", "role": "access"}).
		SetParent(coreSwitch).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed creating access switch: %v", err)
	}

	// 3. Create an immutable Fact for the child device
	_, err = client.Fact.Create().
		SetPayload(map[string]interface{}{"firmware": "v1.2", "drift_detected": false}).
		SetHash("sha256:abcd1234efgh5678").
		SetDevice(accessSwitch).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed creating fact: %v", err)
	}

	// 4. Release Gate Check: Traverse the graph!
	// We start at the Core Switch, traverse down to its Children,
	// and then traverse from those Children into their Facts.
	queriedCore, err := client.Device.Query().
		Where(device.NameEQ("core-sw-01")).
		WithChildren(func(q *ent.DeviceQuery) {
			q.WithFacts() // Load the historical facts for the children
		}).
		Only(ctx)
	if err != nil {
		t.Fatalf("failed querying graph: %v", err)
	}

	// Verify the graph relationships
	if len(queriedCore.Edges.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(queriedCore.Edges.Children))
	}

	child := queriedCore.Edges.Children[0]
	if child.Name != "access-sw-01" {
		t.Errorf("expected child name 'access-sw-01', got '%s'", child.Name)
	}

	if len(child.Edges.Facts) != 1 {
		t.Fatalf("expected 1 fact for child, got %d", len(child.Edges.Facts))
	}

	fact := child.Edges.Facts[0]
	if fact.Hash != "sha256:abcd1234efgh5678" {
		t.Errorf("expected fact hash 'sha256:abcd1234efgh5678', got '%s'", fact.Hash)
	}

	// Output success
	t.Log("Graph Traversal Successful! The Release Gate is passed.")
}
