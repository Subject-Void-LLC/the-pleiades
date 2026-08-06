package engine

import (
	"fmt"
	"sync"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/sdk"
)

// runbookContext is the Walk-tier sdk.RunbookContext handed to a Collection
// method: it collects whatever the method emits so the executor can report
// it, and resolves secrets for the device the method is acting on.
//
// It is in-process by design. pkg/sdk's contract is shaped for an
// out-of-process method eventually talking over IPC, but at Walk tier the
// engine links the method directly, so "send this fact back to the engine"
// is a map write. Nothing about the method's own code changes if that
// carrier is replaced later.
type runbookContext struct {
	// mu guards facts. A method is free to emit from more than one
	// goroutine, and nothing in the SDK contract says it may not.
	mu    sync.Mutex
	facts map[string]interface{}

	// secrets is what InjectSecrets returns. It is populated per device by
	// the composition root; an empty map means this run resolved none,
	// which is different from failing to look them up.
	secrets map[string]string
}

// compile-time proof this satisfies both the SDK contract and the engine's
// own fact-collection interface.
var (
	_ sdk.RunbookContext = (*runbookContext)(nil)
	_ FactCollector      = (*runbookContext)(nil)
)

// NewRunbookContext builds a context carrying secrets for one device.
func NewRunbookContext(secrets map[string]string) sdk.RunbookContext {
	copied := make(map[string]string, len(secrets))
	for k, v := range secrets {
		copied[k] = v
	}
	return &runbookContext{facts: map[string]interface{}{}, secrets: copied}
}

// InjectSecrets returns the secrets available to this method.
func (c *runbookContext) InjectSecrets() map[string]string {
	snapshot := make(map[string]string, len(c.secrets))
	for k, v := range c.secrets {
		snapshot[k] = v
	}
	return snapshot
}

// SetStat records a statistic. It shares storage with EmitFact rather than
// keeping a second map, because both end up in the same ActionResult.Stats
// and a split would only force the executor to merge them back.
func (c *runbookContext) SetStat(key string, value interface{}) error {
	return c.record(key, value)
}

// EmitFact records a fact discovered about the device.
func (c *runbookContext) EmitFact(key string, value interface{}) error {
	return c.record(key, value)
}

// record stores one emitted value, rejecting an empty key. An empty key
// would produce a stat nothing can reference in a later when_cel, which is
// a silent no-op rather than an error the author can see.
func (c *runbookContext) record(key string, value interface{}) error {
	if key == "" {
		return fmt.Errorf("runbook context: refusing to record a value under an empty key")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.facts[key] = value
	return nil
}

// Facts returns a snapshot of everything recorded.
func (c *runbookContext) Facts() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshot := make(map[string]interface{}, len(c.facts))
	for k, v := range c.facts {
		snapshot[k] = v
	}
	return snapshot
}

// NewDeviceRunbookContext is the constructor the composition root passes to
// NewCollectionActionExecutor. It exists so the common case (no secrets
// resolved yet) needs no closure at the call site.
func NewDeviceRunbookContext(_ inventory.InventoryItem) sdk.RunbookContext {
	return NewRunbookContext(nil)
}
