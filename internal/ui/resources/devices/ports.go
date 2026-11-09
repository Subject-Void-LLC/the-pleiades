// Package devices: the adapters between the view and the inventory
// repository port, and the one value both halves carry.
package devices

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// device is the value this view reads, renders and writes.
//
// A read carries the stored item. A create carries the item Bind built from
// the form. An edit carries the submitted tags alone, and that difference is
// the point of the type. An edit used to carry an item built afresh from the
// form, with a new random id and version zero, and the repository's Save
// answers an item whose version equals the one it was loaded at as "nothing
// changed": every edit redirected as a success and stored nothing
// (FAILURE_PATTERNS 1044). A device's name and type are fixed once it
// exists, so an edit is a change applied to the stored device, never a
// device rebuilt from a form.
type device struct {
	// item is the stored device on a read, and the device to insert on a
	// create. It is nil on an edit.
	item pkginventory.InventoryItem

	// tags is what an edit form submitted. It is unused on a read and on a
	// create, where the tags are the item's own.
	tags []pkginventory.Tag
}

// tagChanger is the structural check for an item that can record a tag
// change as a revision. Every item the factory builds embeds record.Base,
// which provides it; the check is structural so pkg/inventory's read-only
// item contract gains no setter.
type tagChanger interface {
	ChangeTags([]pkginventory.Tag) bool
}

// errConflictMessage is what an edit that lost a race to another writer
// tells the person who submitted it.
const errConflictMessage = "Someone else changed this device while you were editing it. " +
	"Reload the page to see their change, then make yours again."

// reader adapts the inventory.Repository port to the view's Reader.
type reader struct {
	repo inventory.Repository
}

// List reads one page of devices in id order, keyset paged.
func (r reader) List(ctx context.Context, q view.Query) (view.Page[device], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	// One more than asked for, so a next page is observed rather than
	// guessed at from a full page.
	it, err := r.repo.GetGroup(ctx, pkginventory.Selector{
		After: pkginventory.DeviceID(q.Cursor),
		Limit: limit + 1,
	})
	if err != nil {
		return view.Page[device]{}, err
	}
	defer func() { _ = it.Close() }()

	var page view.Page[device]
	for it.Next(ctx) {
		if len(page.Items) == limit {
			page.NextCursor = string(page.Items[limit-1].item.ID())
			break
		}
		page.Items = append(page.Items, device{item: it.Item()})
	}
	return page, it.Error()
}

// Get reads one device by its name, which is its address in this view.
func (r reader) Get(ctx context.Context, id string) (device, error) {
	item, err := r.repo.GetByName(ctx, id)
	if err != nil {
		return device{}, err
	}
	return device{item: item}, nil
}

// writer adapts the same port's write half.
type writer struct {
	repo inventory.Repository
}

// Create inserts the device Bind built and returns its name.
func (w writer) Create(ctx context.Context, d device) (string, error) {
	if err := w.repo.Create(ctx, d.item); err != nil {
		return "", err
	}
	return d.item.Name(), nil
}

// Update applies an edit to the stored device named id.
//
// The device is read from storage and changed through its own mutator, so
// its id, lifecycle state, source and history are the stored ones, the
// change bumps the version and records a revision, and Save writes it only
// if the row is still at the version just read. A writer that got there
// first makes Save refuse, and the refusal is answered on the form rather
// than as a 500, so the person keeps what they typed. Unchanged tags change
// nothing and Save stores nothing.
func (w writer) Update(ctx context.Context, id string, d device) error {
	stored, err := w.repo.GetByName(ctx, id)
	if err != nil {
		return view.WrapNotFound(err, inventory.ErrItemNotFound)
	}
	changer, ok := stored.(tagChanger)
	if !ok {
		return fmt.Errorf("device %s cannot record a tag change", id)
	}
	changer.ChangeTags(d.tags)
	err = w.repo.Save(ctx, stored)
	if errors.Is(err, inventory.ErrVersionConflict) {
		return view.FieldFault{Field: "tags", Message: errConflictMessage}
	}
	return err
}

// Delete retires the device named id.
func (w writer) Delete(ctx context.Context, id string) error {
	// Retirement, not row removal: every Revision is immutable by schema
	// and the revisions edge carries no cascade, so a real delete would
	// mean destroying the audit trail the schema exists to protect.
	return view.WrapNotFound(w.repo.Retire(ctx, id), inventory.ErrItemNotFound)
}
