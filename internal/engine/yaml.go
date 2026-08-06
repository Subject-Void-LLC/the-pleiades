package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// BuildFromYAML parses a raw YAML runbook, validates references, compiles
// CEL expressions, and ensures the graph is acyclic. It is the
// Anti-Corruption Layer between the YAML surface format and the domain
// model: YAML vocabulary (indentation, tags, anchors) never travels past
// this function. It decodes into the same WorkflowDef Build (dag.go) uses
// and shares its compilation logic, so a hand-written YAML runbook and a
// JSON-built one compile to the identical *DAG.
//
// BuildFromYAML has no file context, so an import_tasks task in payload
// fails with a clear error rather than silently misresolving a relative
// path; use BuildFromYAMLFile when payload comes from a real file on
// disk.
func (b *Builder) BuildFromYAML(payload []byte) (*DAG, error) {
	def, err := parseWorkflowYAML(payload)
	if err != nil {
		return nil, err
	}
	return b.buildFromDef(def, "")
}

// BuildFromYAMLFile reads path, then parses and compiles it exactly like
// BuildFromYAML, except with a real file context: an import_tasks task's
// relative params.file resolves against filepath.Dir(path), the runbook's
// own directory. This is the surface cmd/pleiades's runbook loader uses
// instead of a manual os.ReadFile + BuildFromYAML pair.
func (b *Builder) BuildFromYAMLFile(path string) (*DAG, error) {
	payload, err := os.ReadFile(path) // #nosec G304 -- path is a CLI argument the invoking user supplies to their own tool, same trust boundary as cmd/pleiades/load.go's own os.ReadFile call
	if err != nil {
		return nil, fmt.Errorf("failed to read runbook %s: %w", path, err)
	}

	def, err := parseWorkflowYAML(payload)
	if err != nil {
		return nil, err
	}
	return b.buildFromDef(def, filepath.Dir(path))
}

// parseWorkflowYAML is BuildFromYAML and BuildFromYAMLFile's shared
// decode step, factored out so the two never drift: an Ansible-shape sniff
// (see below), then the real WorkflowDef unmarshal.
func parseWorkflowYAML(payload []byte) (WorkflowDef, error) {
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
				return WorkflowDef{}, fmt.Errorf("this file is shaped like an Ansible playbook (a top-level YAML list of plays), not a native Pleiades runbook (a YAML map with id/tasks); this engine cannot execute Ansible playbooks directly, see PLAN.md Section 23")
			}
			return WorkflowDef{}, fmt.Errorf("this file is a top-level YAML list, not a native Pleiades runbook (a YAML map with id/tasks)")
		}
	}

	var def WorkflowDef
	if err := yaml.Unmarshal(payload, &def); err != nil {
		return WorkflowDef{}, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	return def, nil
}
