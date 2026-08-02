package engine

import (
	"encoding/json"
	"fmt"
)

// WorkflowDef represents the JSON structure of a user playbook.
type WorkflowDef struct {
	ID    string    `json:"id"`
	Nodes []NodeDef `json:"nodes"`
	Edges []EdgeDef `json:"edges"`
}

// NodeDef defines a single action/step in the playbook.
type NodeDef struct {
	ID     string                 `json:"id"`
	Action string                 `json:"action"` // e.g. "RESTCONF_GET", "CLI_EXEC"
	Params map[string]interface{} `json:"params"`
}

// EdgeDef defines the directed edges between nodes.
type EdgeDef struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"` // CEL Expression, e.g. "stat.ping_ms < 50"
}

// DAG is the executable in-memory graph.
type DAG struct {
	ID       string
	Nodes    map[string]*NodeDef
	Adjacency map[string][]EdgeConfig
}

// EdgeConfig holds the compiled CEL program for the transition.
type EdgeConfig struct {
	To        string
	Condition Program // Compiled CEL Expression, can be nil if unconditional
}

// Builder compiles JSON into an executable memory DAG.
type Builder struct {
	cel Evaluator
}

// NewBuilder initializes a new Workflow DAG builder.
func NewBuilder(celEvaluator Evaluator) *Builder {
	return &Builder{
		cel: celEvaluator,
	}
}

// Build parses a raw JSON payload, validates references, compiles CEL expressions,
// and ensures the graph is acyclic.
func (b *Builder) Build(payload []byte) (*DAG, error) {
	var def WorkflowDef
	if err := json.Unmarshal(payload, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	dag := &DAG{
		ID:       def.ID,
		Nodes:    make(map[string]*NodeDef),
		Adjacency: make(map[string][]EdgeConfig),
	}

	// 1. Index Nodes
	for i := range def.Nodes {
		node := &def.Nodes[i]
		if _, exists := dag.Nodes[node.ID]; exists {
			return nil, fmt.Errorf("duplicate node ID detected: %s", node.ID)
		}
		dag.Nodes[node.ID] = node
	}

	// 2. Validate and Compile Edges
	for _, edge := range def.Edges {
		if _, exists := dag.Nodes[edge.From]; !exists {
			return nil, fmt.Errorf("edge references unknown 'from' node: %s", edge.From)
		}
		if _, exists := dag.Nodes[edge.To]; !exists {
			return nil, fmt.Errorf("edge references unknown 'to' node: %s", edge.To)
		}

		var prg Program
		if edge.Condition != "" {
			var err error
			prg, err = b.cel.Compile(edge.Condition)
			if err != nil {
				return nil, fmt.Errorf("failed to compile condition for edge %s->%s: %w", edge.From, edge.To, err)
			}
		}

		dag.Adjacency[edge.From] = append(dag.Adjacency[edge.From], EdgeConfig{
			To:        edge.To,
			Condition: prg,
		})
	}

	// 3. Cycle Detection (DFS)
	if hasCycle(dag) {
		return nil, fmt.Errorf("circular dependency detected in workflow DAG")
	}

	return dag, nil
}

// hasCycle performs a Depth-First Search to detect back-edges.
func hasCycle(dag *DAG) bool {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(nodeID string) bool
	dfs = func(nodeID string) bool {
		visited[nodeID] = true
		recStack[nodeID] = true

		for _, edge := range dag.Adjacency[nodeID] {
			if !visited[edge.To] {
				if dfs(edge.To) {
					return true
				}
			} else if recStack[edge.To] {
				// We hit a node currently in our recursion stack -> CYCLE!
				return true
			}
		}

		recStack[nodeID] = false
		return false
	}

	for nodeID := range dag.Nodes {
		if !visited[nodeID] {
			if dfs(nodeID) {
				return true
			}
		}
	}

	return false
}
