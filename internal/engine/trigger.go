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

// Evaluator parses and resolves Common Expression Language (CEL) strings.
type Evaluator interface {
	// Evaluate takes a CEL boolean expression and the current workflow context,
	// returning true or false to determine DAG routing.
	Evaluate(expression string, ctx WorkflowContext) (bool, error)
}
