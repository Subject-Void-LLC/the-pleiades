package inventory_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// mockIterator implements inventory.Iterator for testing.
type mockIterator struct {
	items []inventory.InventoryItem
	index int
}

func (m *mockIterator) Next(ctx context.Context) bool {
	if m.index >= len(m.items) {
		return false
	}
	m.index++
	return true
}

func (m *mockIterator) Item() inventory.InventoryItem {
	return m.items[m.index-1]
}

func (m *mockIterator) Error() error {
	return nil
}

func (m *mockIterator) Close() error {
	return nil
}

// mockRepository implements inventory.Repository for testing.
type mockRepository struct{}

func (m *mockRepository) GetGroup(ctx context.Context, groupName string) (inventory.Iterator, error) {
	return &mockIterator{
		items: []inventory.InventoryItem{
			&mockDevice{id: "dev-1"},
			&mockDevice{id: "dev-2"},
		},
	}, nil
}

func TestIteratorCompliance(t *testing.T) {
	var repo inventory.Repository = &mockRepository{}

	iter, err := repo.GetGroup(context.Background(), "all_devices")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer iter.Close()

	count := 0
	for iter.Next(context.Background()) {
		item := iter.Item()
		if item == nil {
			t.Fatal("expected item to not be nil")
		}
		count++
	}

	if err := iter.Error(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 items, got %d", count)
	}
}
