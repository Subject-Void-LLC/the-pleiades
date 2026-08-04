// Package engine contains the core workflow routing and dispatch logic.
package engine

// WorkflowContext holds the aggregated, ephemeral stats for a running workflow.
// It is backed by a transient store (like NATS KV) to prevent memory bloat.
type WorkflowContext interface {
	// Merge inserts a newly returned stat payload from a device into the context.
	Merge(nodeID string, deviceID string, stats map[string]interface{}) error

	// Read generates the JSON structure required by the CEL evaluator.
	Read() (map[string]interface{}, error)
}
