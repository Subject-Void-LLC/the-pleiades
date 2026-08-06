package inventory

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/device"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/group"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/internal/storage"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

type entRepository struct {
	client  *ent.Client
	factory *ItemFactory
	uow     storage.UnitOfWork
}

// NewEntRepository creates a new inventory Repository backed by
// Postgres/SQLite through ent.
func NewEntRepository(client *ent.Client, factory *ItemFactory) Repository {
	return &entRepository{
		client:  client,
		factory: factory,
		uow:     storage.NewEntUnitOfWork(client),
	}
}

// entClient returns the client Save/GetByName/GetGroup should issue
// their queries through: the transaction-scoped one stashed in ctx by a
// UnitOfWork.WithTx call further up the stack, if there is one, or the
// repository's own top-level client otherwise. This is what makes
// storage.UnitOfWork reusable beyond Save specifically: any repository
// method reachable from inside a WithTx callback transparently joins
// that same transaction instead of escaping it.
func (r *entRepository) entClient(ctx context.Context) *ent.Client {
	if txClient := ent.FromContext(ctx); txClient != nil {
		return txClient
	}
	return r.client
}

func (r *entRepository) GetGroup(ctx context.Context, sel inventory.Selector) (Iterator, error) {
	// Order once, here, rather than per batch in Next: Clone() carries an
	// applied Order forward, so there is no need to reapply it on every
	// page fetch.
	query := r.entClient(ctx).Device.Query().Order(device.ByDeviceID())

	// A non-empty GroupName pushes the filter down to SQL via the Group
	// edge (an EXISTS subquery, ent's HasGroupsWith, not a join, so no
	// duplicate-row risk). Before this, GroupName was received and never
	// referenced again: every call streamed the whole devices table
	// regardless of what a caller asked for.
	//
	// This matches on DIRECT Group membership only. PLAN.md Section 3's
	// group nesting (a group's own Group.children/parents edges,
	// internal/ent/schema/group.go) is not traversed here: nothing in this
	// codebase populates those edges yet, so there is no real nested-group
	// data to test against, and building traversal for a case with zero
	// live callers would be exactly the kind of premature infrastructure
	// this project's own conventions avoid elsewhere. A group with real
	// child groups but no direct device members will dispatch to zero
	// devices today, the same "fails closed, not open" behavior an
	// unpopulated or nonexistent group name gets.
	if sel.GroupName != "" {
		query = query.Where(device.HasGroupsWith(group.NameEQ(sel.GroupName)))
	}

	// Keyset-paginated iterator: batches page on device_id, the stable,
	// indexed, time-ordered (UUIDv7) column this schema was built to
	// support (internal/ent/schema/device.go). In production Postgres, a
	// server-side cursor (DECLARE cursor_name CURSOR FOR...) is another
	// option, but keyset batching needs no such driver-specific feature
	// and works identically across SQL dialects.
	return &entIterator{
		query:     query,
		factory:   r.factory,
		batchSize: 1000,
	}, nil
}

// GetByName loads one device by its unique name, together with its stored
// audit trail, and hydrates it into a typed InventoryItem.
func (r *entRepository) GetByName(ctx context.Context, name string) (inventory.InventoryItem, error) {
	dev, err := r.entClient(ctx).Device.Query().Where(device.NameEQ(name)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load device %s: %w", name, err)
	}

	rec, err := toRecord(dev)
	if err != nil {
		return nil, err
	}

	// Unlike the list path, a single-item read carries history: this is the
	// method callers use precisely when they need the audit trail.
	rec.History, err = loadRevisions(ctx, dev)
	if err != nil {
		return nil, err
	}

	item, err := r.factory.Build(rec)
	if err != nil {
		return nil, fmt.Errorf("factory failed to build item %s: %w", name, err)
	}
	return item, nil
}

// toRecord converts an ent-backed row into the storage-agnostic Record the
// factory hydrates. This is the one place ent's shape meets the domain
// model; nowhere else in the factory or the concrete device types knows
// ent exists.
func toRecord(dev *ent.Device) (record.Record, error) {
	// An unrecognized stored state is an error, never a silent promotion to
	// active. Active is the one state that permits execution, so guessing
	// it for a value this build does not understand would let a
	// quarantined or archived device accept work.
	state, err := inventory.ParseLifecycleState(dev.State)
	if err != nil {
		return record.Record{}, fmt.Errorf("device %s: %w", dev.Name, err)
	}

	source := inventory.SourceAuthority{Plugin: dev.Source}
	if dev.SourceSyncedAt != nil {
		source.SyncedAt = *dev.SourceSyncedAt
	}

	return record.Record{
		// device_id is the stable opaque identifier (internal/ent/schema/
		// device.go), distinct from both the mutable Name and the ent
		// internal auto-increment primary key, which stays a
		// storage-layer detail this record never exposes.
		ID:         inventory.DeviceID(dev.DeviceID),
		Name:       dev.Name,
		Type:       dev.Type,
		Properties: dev.Properties,
		Tags:       toTags(dev.Tags),
		State:      state,
		Source:     source,
		// Carrying the stored version forward is what makes it an
		// optimistic-concurrency token. Without it every hydrated item
		// restarted at 0 and Save could not detect a lost update.
		Version: dev.Version,
	}, nil
}

type entIterator struct {
	query     *ent.DeviceQuery
	factory   *ItemFactory
	batchSize int
	// cursor is the device_id of the last row yielded so far, valid once
	// started is true. Paging on this stable, unique, indexed column
	// (rather than a numeric offset) means a device removed elsewhere in
	// the table between batch fetches cannot shift which rows the next
	// WHERE device_id > cursor batch returns: offset pagination has no
	// such guarantee, since OFFSET counts row position dynamically and
	// SQL defines no row order at all without an ORDER BY, so two
	// sequential unordered queries are not even guaranteed to agree with
	// each other, let alone survive a concurrent write. A device inserted
	// with a device_id sorting AHEAD of the cursor is picked up normally;
	// one inserted behind it (only possible if a caller bypasses the
	// schema's own UUIDv7 default, or two writers' clocks skew) is not
	// retroactively surfaced, the same trade-off every keyset-paginated
	// cursor makes.
	//
	// started distinguishes "before the first row" (cursor is not yet
	// meaningful) from "the first-ever device_id happens to be the empty
	// string." device_id is NotEmpty() at the ent validation layer, so
	// this cannot occur through this schema's own write path, but that
	// guarantee is application-level, not a database CHECK constraint
	// (internal/ent/migrate/schema.go), so a row written by something
	// other than this ent client is not structurally impossible. Without
	// started, such a row would make cursor == "" forever, and Next would
	// refetch the same first batch on every call rather than reaching EOF.
	started bool
	cursor  string
	buffer  []*ent.Device
	index   int
	current inventory.InventoryItem
	err     error
}

func (i *entIterator) build(dev *ent.Device) (inventory.InventoryItem, error) {
	rec, err := toRecord(dev)
	if err != nil {
		return nil, fmt.Errorf("failed to convert device %s: %w", dev.Name, err)
	}
	item, err := i.factory.Build(rec)
	if err != nil {
		return nil, fmt.Errorf("factory failed to build item %s: %w", dev.Name, err)
	}
	return item, nil
}

func (i *entIterator) Next(ctx context.Context) bool {
	if i.err != nil {
		return false
	}
	// A caller that already gave up should see false rather than more
	// items it no longer wants, matching fileIterator.Next's identical
	// ctx-honoring behavior (file_repository_iterator.go): the Repository
	// port's two adapters are documented to behave identically to callers.
	if err := ctx.Err(); err != nil {
		return false
	}

	// If we still have items in the buffer, just advance the index
	if i.index < len(i.buffer) {
		item, err := i.build(i.buffer[i.index])
		if err != nil {
			i.err = err
			return false
		}
		i.current = item
		i.index++
		return true
	}

	// Buffer is empty (or fully consumed). Fetch the next batch, honoring
	// the ctx argument passed to this call rather than a context captured
	// at construction time. i.query already carries Order(ByDeviceID())
	// from GetGroup; only the cursor predicate and the limit are added
	// per batch here.
	q := i.query.Clone().Limit(i.batchSize)
	if i.started {
		q = q.Where(device.DeviceIDGT(i.cursor))
	}
	batch, err := q.All(ctx)
	if err != nil {
		i.err = fmt.Errorf("failed to fetch device batch: %w", err)
		return false
	}

	if len(batch) == 0 {
		return false // EOF
	}

	i.buffer = batch
	i.started = true
	i.cursor = batch[len(batch)-1].DeviceID
	i.index = 0

	// Build the first item of the new batch
	item, err := i.build(i.buffer[i.index])
	if err != nil {
		i.err = err
		return false
	}

	i.current = item
	i.index++
	return true
}

func (i *entIterator) Item() inventory.InventoryItem {
	return i.current
}

func (i *entIterator) Error() error {
	return i.err
}

func (i *entIterator) Close() error {
	i.buffer = nil
	i.current = nil
	return nil
}
