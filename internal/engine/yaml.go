// Package engine: the YAML front door, which parses a runbook file into
// the WorkflowDef the builder compiles, and names the shapes a file can
// take that are not a runbook.
package engine

import (
	"errors"
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

// ErrTaskList is the error a build returns for a file that is a top-level
// list of tasks, the shape an import_tasks file takes, rather than a
// runbook. Such a file has no id or hosts of its own and is checked
// through the runbook that imports it, so a caller given many files at
// once (pleiades validate runbooks/*) can tell it apart with errors.Is
// and pass over it, where any other build error is a real failure.
var ErrTaskList = errors.New("this file is a list of tasks, the shape an import_tasks file takes, not a runbook (a YAML map with id/tasks); name the runbook that imports it instead")

// playKeys are the keys that mark a top-level list item as an Ansible
// play rather than a task. Ansible requires every play to name its
// hosts unless it is an import_playbook entry, and only a play holds task
// sections or roles, so no task carries any of them.
var playKeys = []string{"hosts", "import_playbook", "ansible.builtin.import_playbook", "tasks", "pre_tasks", "post_tasks", "roles", "handlers"}

// parseWorkflowYAML is BuildFromYAML and BuildFromYAMLFile's shared
// decode step, factored out so the two never drift: a shape sniff for a
// top-level list (see below), then the real WorkflowDef unmarshal.
func parseWorkflowYAML(payload []byte) (WorkflowDef, error) {
	// Cheap pre-parse sniff: a runbook is a top-level map, so a top-level
	// list is refused here with an error that says what the file actually
	// is, instead of the raw type-mismatch error the WorkflowDef unmarshal
	// below would give. Two lists of maps are common enough to name. An
	// Ansible playbook is a list of plays, and at least one item carries
	// a play key (playKeys). An import_tasks file is a list of tasks, and
	// none does; it gets ErrTaskList, which callers can test for. A list
	// with no map in it at all (empty, or scalars) is neither, so it gets
	// generic wording that does not overclaim what the file is. If the
	// probe unmarshal itself fails (genuinely malformed YAML) or the
	// document is empty (nil) or a map, fall through unchanged to the
	// full parse path below.
	var probe any
	if err := yaml.Unmarshal(payload, &probe); err == nil {
		if list, isList := probe.([]any); isList {
			return WorkflowDef{}, topLevelListError(list)
		}
	}

	var def WorkflowDef
	if err := normalizeWorkflowYAML(payload, &def); err != nil {
		return WorkflowDef{}, err
	}
	return def, nil
}

// topLevelListError says why list, a file's whole top-level document, is
// not a runbook: an Ansible playbook, a list of tasks, or neither.
func topLevelListError(list []any) error {
	sawMap := false
	for _, item := range list {
		fields, isMap := item.(map[string]any)
		if !isMap {
			continue
		}
		sawMap = true
		for _, key := range playKeys {
			// One play anywhere makes the whole file a playbook: a
			// playbook's plays are its only top-level items.
			if _, isPlay := fields[key]; isPlay {
				return fmt.Errorf("this file is shaped like an Ansible playbook (a top-level YAML list of plays), not a native Pleiades runbook (a YAML map with id/tasks); this engine cannot execute Ansible playbooks directly; convert it with '%s <file>', and see %s", migrateCommand, migrationGuide)
			}
		}
	}
	if sawMap {
		return ErrTaskList
	}
	return fmt.Errorf("this file is a top-level YAML list, not a native Pleiades runbook (a YAML map with id/tasks)")
}
