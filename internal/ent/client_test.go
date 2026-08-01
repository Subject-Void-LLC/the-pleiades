package ent_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/device"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

func TestGraphTraversal(t *testing.T) {
	// Spin up an in-memory SQLite database for the Release Gate
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	// 1. Create a parent (e.g. Core Switch)
	coreSwitch, err := client.Device.Create().
		SetName("core-sw-01").
		SetProperties(map[string]interface{}{"ip": "10.0.0.1", "role": "core"}).
		Save(ctx)
	if err != nil {
		t.Fatalf("failed creating core switch: %v", err)
	}

	// 2. Create a child (e.g. Access Switch) and attach it to the parent
	accessSwitch, err := client.Device.Create().
		SetName("access-sw-01").
		SetProperties(map[string]interface{}{"ip": "10.0.1.1", "role": "access"}).
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
