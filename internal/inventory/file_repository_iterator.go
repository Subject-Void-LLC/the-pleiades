package inventory

import (
	"context"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// fileIterator implements Iterator over an in-memory slice already loaded
// from hosts.yaml by GetGroup. Unlike entIterator's 1000-row batching
// cursor (ent_repository.go), it holds every item in memory at once and
// has no batching machinery at all. That is a deliberate scale trade-off,
// not an oversight: Walk-tier's file-backed inventory targets a
// hand-authored file of, realistically, tens to low thousands of hosts,
// not the Crawl-tier scale batching exists to protect against. Adding a
// batching cursor here would be machinery solving a problem this tier does
// not have.
type fileIterator struct {
	items   []inventory.InventoryItem
	index   int
	current inventory.InventoryItem
}

// Next advances the cursor. It honors ctx cancellation even though
// iterating an already-loaded in-memory slice costs nothing: a caller that
// already gave up should see false rather than more items it no longer
// wants.
func (i *fileIterator) Next(ctx context.Context) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	if i.index >= len(i.items) {
		return false
	}
	i.current = i.items[i.index]
	i.index++
	return true
}

// Item returns the current InventoryItem.
func (i *fileIterator) Item() inventory.InventoryItem {
	return i.current
}

// Error always returns nil: every possible failure (a malformed hosts.yaml
// or sidecar, an unbuildable record) is already surfaced by GetGroup
// itself before this iterator is ever returned, since the whole result set
// is loaded eagerly rather than fetched incrementally.
func (i *fileIterator) Error() error {
	return nil
}

// Close releases the in-memory slice. There is no file handle or cursor to
// release; hosts.yaml and the sidecar were already fully read and closed
// by GetGroup before this iterator was constructed.
func (i *fileIterator) Close() error {
	i.items = nil
	i.current = nil
	return nil
}
