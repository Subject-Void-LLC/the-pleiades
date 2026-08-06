package inventory

import (
	"context"
	"errors"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// ErrInventoryReadOnly is returned by Create and Save on a Repository
// wrapped by NewReadOnlyRepository.
//
// It is a typed refusal rather than a silent no-op, for the same reason
// every other guard in this package is: a read-only mode that quietly
// discards writes reports success for work it did not do, and the caller
// finds out later, from missing data, rather than immediately, from an
// error.
var ErrInventoryReadOnly = errors.New("inventory is open in read-only mode")

// readOnlyRepository wraps a Repository and refuses every write.
type readOnlyRepository struct {
	inner Repository
}

// NewReadOnlyRepository returns a Repository that reads through to inner
// and refuses every write with ErrInventoryReadOnly.
//
// This is the non-mutating run PLAN.md's simulate-first proof describes:
// point a real project at real inventory, plan and simulate against it, and
// know with certainty that nothing was changed. It is deliberately a
// wrapper rather than a flag threaded through both adapters, because a
// wrapper cannot be forgotten in one branch of one adapter: there is
// exactly one place the refusal lives, and the type system carries it
// everywhere the Repository goes.
//
// Reads pass through unchanged, including GetGroup's iterator. An item the
// caller mutates in memory is still mutable (nothing can stop AddInfo on a
// value the caller already holds); what cannot happen is that mutation
// reaching storage.
func NewReadOnlyRepository(inner Repository) Repository {
	return &readOnlyRepository{inner: inner}
}

// GetGroup reads through to the wrapped repository.
func (r *readOnlyRepository) GetGroup(ctx context.Context, sel inventory.Selector) (Iterator, error) {
	return r.inner.GetGroup(ctx, sel)
}

// GetByName reads through to the wrapped repository.
func (r *readOnlyRepository) GetByName(ctx context.Context, name string) (inventory.InventoryItem, error) {
	return r.inner.GetByName(ctx, name)
}

// Create refuses, naming the item so an operator can tell which write was
// blocked rather than only that one was.
func (r *readOnlyRepository) Create(_ context.Context, item inventory.InventoryItem) error {
	return fmt.Errorf("refusing to create %s: %w", item.Name(), ErrInventoryReadOnly)
}

// Save refuses, naming the item for the same reason Create does.
func (r *readOnlyRepository) Save(_ context.Context, item inventory.InventoryItem) error {
	return fmt.Errorf("refusing to save %s: %w", item.Name(), ErrInventoryReadOnly)
}
