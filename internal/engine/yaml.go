package engine

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// BuildFromYAML parses a raw YAML runbook, validates references, compiles
// CEL expressions, and ensures the graph is acyclic. It is the
// Anti-Corruption Layer between the YAML surface format and the domain
// model: YAML vocabulary (indentation, tags, anchors) never travels past
// this function. It decodes into the same WorkflowDef Build (dag.go) uses
// and shares its compilation logic, so a hand-written YAML runbook and a
// JSON-built one compile to the identical *DAG.
func (b *Builder) BuildFromYAML(payload []byte) (*DAG, error) {
	// Cheap pre-parse sniff: a real Ansible playbook is structurally a
	// top-level YAML list of plays (maps), never a top-level map. Detect
	// that shape up front and reject it with a clear, actionable error
	// instead of letting the WorkflowDef unmarshal below fail with a raw
	// type-mismatch error. A bare top-level list is not automatically
	// Ansible-shaped, though: an empty list or a list of scalars has no
	// plays either, so only claim the specific Ansible diagnosis when at
	// least one element actually looks like a play (a map); otherwise use
	// generic wording that doesn't overclaim what the file is. If the
	// probe unmarshal itself fails (genuinely malformed YAML) or the
	// document is empty (nil) or a map, fall through unchanged to the
	// existing full parse path below.
	var probe any
	if err := yaml.Unmarshal(payload, &probe); err == nil {
		if list, isList := probe.([]any); isList {
			looksLikeAnsible := false
			for _, item := range list {
				if _, isMap := item.(map[string]any); isMap {
					looksLikeAnsible = true
					break
				}
			}
			if looksLikeAnsible {
				return nil, fmt.Errorf("this file is shaped like an Ansible playbook (a top-level YAML list of plays), not a native Pleiades runbook (a YAML map with id/tasks); this engine cannot execute Ansible playbooks directly, see PLAN.md Section 23")
			}
			return nil, fmt.Errorf("this file is a top-level YAML list, not a native Pleiades runbook (a YAML map with id/tasks)")
		}
	}

	var def WorkflowDef
	if err := yaml.Unmarshal(payload, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	return b.buildFromDef(def)
}
