// Connection persistence: whether a device's SSH connections stay open
// between its tasks, resolved through the device's hierarchy, and how
// the Collection executor lends them.
package engine

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// PersistConnectionsProperty is the Properties key an inventory, group or
// device sets to false to have every task against its devices log in
// afresh, which is what a run did before connections persisted. It is on
// by default.
const PersistConnectionsProperty = "persist_connections"

// PersistFunc reports whether device's connections may persist in this
// run. It answers the device's own ladder only; the run's own setting
// (the CLI flag, the launch field) is applied by not installing a pool
// at all, so off at either ladder is off.
type PersistFunc func(ctx context.Context, device pkginventory.InventoryItem) bool

// persistExtract reads one layer's opinion. A value that is not a
// boolean is read as false rather than ignored: it was written to say
// something, and the setting exists to be turned off, so a mistyped "no"
// must not leave connections persisting.
func persistExtract(props map[string]interface{}) (bool, bool) {
	raw, ok := props[PersistConnectionsProperty]
	if !ok {
		return false, false
	}
	on, isBool := raw.(bool)
	return on && isBool, true
}

// PersistConnections resolves device's setting: on by default, then each
// layer of ancestry (least specific first), then the device's own
// Properties, the most specific opinion winning.
func PersistConnections(ancestry []inventory.HierarchyLayer, device pkginventory.InventoryItem) bool {
	resolved := ResolveHierarchy(true, ancestry, persistExtract)
	if device != nil {
		if on, ok := persistExtract(device.Properties().Raw()); ok {
			return on
		}
	}
	return resolved.Value
}

// AncestryReader is the one inventory call PersistFor needs.
type AncestryReader interface {
	GroupAncestry(ctx context.Context, deviceName string) ([]inventory.HierarchyLayer, error)
}

// PersistFor returns the PersistFunc that resolves each device's ladder
// through repo. A device whose ancestry cannot be read does not persist:
// the setting exists to be turned off, and an unread "off" must not read
// as on.
func PersistFor(repo AncestryReader) PersistFunc {
	return func(ctx context.Context, device pkginventory.InventoryItem) bool {
		ancestry, err := repo.GroupAncestry(ctx, device.Name())
		if err != nil {
			return false
		}
		return PersistConnections(ancestry, device)
	}
}

// WithConnectionPool has the Collection executor lend SSH connections
// from pool to the methods of every device persist admits, and close a
// device's pooled connection after any method whose manifest says it
// ends the login session. The caller owns pool and closes it when the
// run ends. A nil pool leaves every task logging in afresh.
func WithConnectionPool(pool *remoteexec.Pool, persist PersistFunc) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.pool = pool
		e.persist = persist
	}
}

// lendPool gives rc the run's pool when device's connections persist.
// Only the engine's own context carries one; any other context connects
// afresh.
func (e *collectionActionExecutor) lendPool(ctx context.Context, rc sdk.RunbookContext, device pkginventory.InventoryItem) {
	if e.pool == nil || device == nil || (e.persist != nil && !e.persist(ctx, device)) {
		return
	}
	if c, ok := rc.(*runbookContext); ok {
		c.pool = e.pool
	}
}

// endLoginSession closes device's pooled connection after a real run of
// a method that can change what a login carries, so the next task logs
// in again and sees the change. A check changes nothing, so it keeps the
// connection.
func (e *collectionActionExecutor) endLoginSession(desc collection.Descriptor, device pkginventory.InventoryItem, mode collection.Mode) {
	if e.pool == nil || device == nil || mode != collection.ModeExecute || !desc.Manifest.EndsLoginSession {
		return
	}
	e.pool.Discard(device.Name())
}
