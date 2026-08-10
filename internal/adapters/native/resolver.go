package native

import "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"

// singleDeviceResolver satisfies engine.TargetResolver by always returning
// the one device this Runner invocation was dispatched against,
// regardless of what target string a task names.
//
// This inherits, rather than introduces, a simplification the whole
// per-device dispatch model already made at Phase 14: a single
// wire.DispatchPayload already names one already-admitted device for the
// whole runbook (internal/dispatch's admission check uses the union of
// every task's required capability, not a per-task target), so there is
// no second device this Runner invocation could resolve a different
// task's target to even if it tried. A future phase that wants genuine
// per-task target diversity within one runbook would need to change what
// the Controller fans out, not this resolver.
type singleDeviceResolver struct {
	device inventory.InventoryItem
}

// Resolve implements engine.TargetResolver.
func (r singleDeviceResolver) Resolve(_ string) []inventory.InventoryItem {
	return []inventory.InventoryItem{r.device}
}
