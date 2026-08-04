package inventory

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/device"
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

func (r *entRepository) GetGroup(ctx context.Context, groupName string) (Iterator, error) {
	// Query devices. If groupName is provided, we would normally filter here.
	// For this phase, we'll just stream all devices, or implement a basic JSON check.
	// entgo json filtering: .Where(sql.ExprP("properties->>'group' = ?", groupName))
	// For simplicity in the Iterator implementation, we fetch everything for now.

	query := r.entClient(ctx).Device.Query()

	// Create an offset-based iterator.
	// In production Postgres, a server-side cursor (DECLARE cursor_name CURSOR FOR...) is better,
	// but offset/limit batching is fully supported by all SQL drivers (including SQLite).
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
	offset    int
	buffer    []*ent.Device
	index     int
	current   inventory.InventoryItem
	err       error
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
	// at construction time.
	batch, err := i.query.Clone().Limit(i.batchSize).Offset(i.offset).All(ctx)
	if err != nil {
		i.err = fmt.Errorf("failed to fetch device batch: %w", err)
		return false
	}

	if len(batch) == 0 {
		return false // EOF
	}

	i.buffer = batch
	i.offset += len(batch)
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
