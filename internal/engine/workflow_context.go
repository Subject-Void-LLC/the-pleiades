package engine

import "sync"

// inProcessWorkflowContext is the Walk-tier local adapter behind the
// WorkflowContext port (trigger.go), the same "adapter behind an existing
// port, selected only by the composition root" shape Phase W4 already
// established for lock.Manager, event.Bus, and inventory.Repository
// (HANDOFF_DOCUMENT.md's Phase W4 session). WorkflowContext's own doc
// comment describes a real backing store (NATS KV) as existing to bound
// memory across a distributed run; a single Walk-tier process run has no
// such distributed-memory concern, so this adapter is nothing more than a
// nested map guarded by one mutex, discarded with the value itself once
// the run finishes (PLAN.md Section 27: "stats are strictly ephemeral...
// once the workflow DAG completes, the stats are discarded").
type inProcessWorkflowContext struct {
	mu sync.RWMutex
	// data is keyed nodeID, then deviceID, holding whatever stats map was
	// last merged for that exact pair.
	data map[string]map[string]map[string]interface{}
}

// NewInProcessWorkflowContext returns a WorkflowContext with no backing
// store at all: every Merge call is held in memory for the lifetime of
// this value, which is exactly one runbook run at Walk tier.
func NewInProcessWorkflowContext() WorkflowContext {
	return &inProcessWorkflowContext{data: make(map[string]map[string]map[string]interface{})}
}

// Merge records stats under nodeID and deviceID, overwriting whatever was
// previously recorded for that exact pair. Executor (executor.go) always
// calls this with a task's Register name as nodeID, never the task's
// synthesized graph ID: Register is the only author-chosen, stable name
// meant "for later tasks to reference" (Task.Register's doc comment,
// dag.go), so it is the name a later when_cel condition can actually
// write down. deviceID groups results exactly the way PLAN.md Section 27
// describes ("grouping them by device ID to prevent namespace
// collisions"); it is the empty string for a controller-side task with no
// target device (Section 14's Execution Contexts).
func (c *inProcessWorkflowContext) Merge(nodeID string, deviceID string, stats map[string]interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	byDevice, ok := c.data[nodeID]
	if !ok {
		byDevice = make(map[string]map[string]interface{})
		c.data[nodeID] = byDevice
	}
	byDevice[deviceID] = stats
	return nil
}

// Read returns a snapshot of every merged stat, nested nodeID then
// deviceID. Executor.runNode binds this same snapshot under both the
// "stat" and "nodes" CEL variables (cel.go declares both as
// map(string, dyn)), so CEL's own field-selection-on-a-map semantics mean
// stat.precheck[""].foo and nodes.precheck[""].foo both work against this
// shape with no further translation; Program.Eval itself no longer assumes
// which variable name a caller's data belongs under; that binding now
// happens explicitly at the call site. Read returns a deep copy, not the
// live map, so a caller holding onto a prior Read's result can never
// observe or corrupt a Merge call that happens afterward.
func (c *inProcessWorkflowContext) Read() (map[string]interface{}, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]interface{}, len(c.data))
	for nodeID, byDevice := range c.data {
		devices := make(map[string]interface{}, len(byDevice))
		for deviceID, stats := range byDevice {
			statsCopy := make(map[string]interface{}, len(stats))
			for k, v := range stats {
				statsCopy[k] = v
			}
			devices[deviceID] = statsCopy
		}
		out[nodeID] = devices
	}
	return out, nil
}
