package inventory

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
)

type entRepository struct {
	client  *ent.Client
	factory *ItemFactory
}

// NewEntRepository creates a new inventory Repository backed by Postgres/SQLite.
func NewEntRepository(client *ent.Client, factory *ItemFactory) Repository {
	return &entRepository{
		client:  client,
		factory: factory,
	}
}

func (r *entRepository) GetGroup(ctx context.Context, groupName string) (Iterator, error) {
	// Query devices. If groupName is provided, we would normally filter here.
	// For this phase, we'll just stream all devices, or implement a basic JSON check.
	// entgo json filtering: .Where(sql.ExprP("properties->>'group' = ?", groupName))
	// For simplicity in the Iterator implementation, we fetch everything for now.

	query := r.client.Device.Query()
	
	// Create an offset-based iterator.
	// In production Postgres, a server-side cursor (DECLARE cursor_name CURSOR FOR...) is better,
	// but offset/limit batching is fully supported by all SQL drivers (including SQLite).
	return &entIterator{
		ctx:     ctx,
		query:   query,
		factory: r.factory,
		batchSize: 1000,
		offset:    0,
		buffer:    nil,
		index:     0,
	}, nil
}

type entIterator struct {
	ctx       context.Context
	query     *ent.DeviceQuery
	factory   *ItemFactory
	batchSize int
	offset    int
	buffer    []*ent.Device
	index     int
	current   InventoryItem
	err       error
}

func (i *entIterator) Next(ctx context.Context) bool {
	if i.err != nil {
		return false
	}

	// If we still have items in the buffer, just advance the index
	if i.index < len(i.buffer) {
		item, err := i.factory.Build(i.buffer[i.index])
		if err != nil {
			i.err = fmt.Errorf("factory failed to build item %s: %w", i.buffer[i.index].Name, err)
			return false
		}
		i.current = item
		i.index++
		return true
	}

	// Buffer is empty (or fully consumed). Fetch the next batch.
	batch, err := i.query.Clone().Limit(i.batchSize).Offset(i.offset).All(i.ctx)
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
	item, err := i.factory.Build(i.buffer[i.index])
	if err != nil {
		i.err = fmt.Errorf("factory failed to build item %s: %w", i.buffer[i.index].Name, err)
		return false
	}
	
	i.current = item
	i.index++
	return true
}

func (i *entIterator) Item() InventoryItem {
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
