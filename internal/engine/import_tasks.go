// Package engine: import_tasks resolution. docs/hephaestus.md classifies
// ansible.builtin.import_tasks as "an engine keyword, resolved at parse
// time: static inclusion is compatible with building the DAG up front,"
// unlike include_tasks (runtime inclusion, permanently unresolved). This
// file is that resolution: it runs before synthesizeChain/collectSubtree
// ever see a task, splicing an imported file's own task list into the
// position its import_tasks task occupies.
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxImportDepth bounds how many import_tasks hops a single chain may
// take before resolveImportTasks gives up, the same defensive-bound style
// internal/forge/genutil.ValidateSegments uses for segment count: cycle
// detection (see resolving below) already rejects a file that imports
// itself, but a long, non-cyclic chain (A imports B imports C ...) is
// still worth bounding rather than trusting to be finite by construction.
const maxImportDepth = 32

// resolveImportTasks rewrites every "import_tasks" task reachable from
// def's PreTasks/Tasks/PostTasks, at any Block/Rescue/Always nesting
// depth, into an ordinary block task: Block holds the referenced file's
// own task list (itself fully resolved, recursively, before this
// function returns), and FQCN/Params are cleared. It runs once, as the
// first step of buildFromDef, before any other task-tree walk: after it
// returns, validateTask's existing "exactly one of fqcn or block" rule
// sees an ordinary block task, and synthesizeChain/collectSubtree need no
// changes at all to splice its children into the chain.
//
// baseDir is the directory every import_tasks path in this runbook
// resolves against, always the same directory regardless of import
// nesting depth: a file B imported by A, which itself uses import_tasks,
// resolves that further reference against the same baseDir A did, not
// against B's own directory. This is a deliberate simplification over
// Ansible's own role-relative resolution, chosen because nothing in this
// codebase's runbook model has a "role" concept to make relative-to-
// including-file resolution well defined in the first place. An empty
// baseDir means no file context is available (Build/BuildFromYAML, called
// with a raw payload and no path); resolveOneImport rejects any actual
// import_tasks task encountered in that case with a clear error, but a
// runbook that never uses import_tasks builds exactly as before.
func resolveImportTasks(def *WorkflowDef, baseDir string) error {
	resolving := make(map[string]bool)
	for _, tasks := range [][]Task{def.PreTasks, def.Tasks, def.PostTasks} {
		if err := resolveImportTasksInList(tasks, baseDir, resolving, 0); err != nil {
			return err
		}
	}
	return nil
}

// resolveImportTasksInList walks tasks in place, resolving any
// import_tasks task it finds and recursing into every task's own
// Block/Rescue/Always (imported or hand-authored) to reach every nesting
// depth. tasks is mutated through its backing array (task := &tasks[i]),
// exactly like tasktree.go's synthesizeChain/collectSubtree, so callers
// passing a WorkflowDef field's slice see the mutation without needing to
// reassign the field themselves.
func resolveImportTasksInList(tasks []Task, baseDir string, resolving map[string]bool, depth int) error {
	for i := range tasks {
		task := &tasks[i]

		if task.FQCN == "import_tasks" {
			imported, err := resolveOneImport(task, baseDir, resolving, depth)
			if err != nil {
				return err
			}
			task.Block = imported
			task.FQCN = ""
			task.Params = nil
			// imported is already fully resolved (resolveOneImport
			// recurses before returning), so there is nothing further to
			// walk for this task.
			continue
		}

		if len(task.Block) > 0 {
			if err := resolveImportTasksInList(task.Block, baseDir, resolving, depth); err != nil {
				return err
			}
		}
		if len(task.Rescue) > 0 {
			if err := resolveImportTasksInList(task.Rescue, baseDir, resolving, depth); err != nil {
				return err
			}
		}
		if len(task.Always) > 0 {
			if err := resolveImportTasksInList(task.Always, baseDir, resolving, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveOneImport reads task's referenced file (task.Params["file"]),
// parses it as a bare YAML task list, and recursively resolves any
// import_tasks within that list before returning, so the caller's
// assignment (task.Block = imported) is always a fully resolved subtree
// with no import_tasks task left inside it.
func resolveOneImport(task *Task, baseDir string, resolving map[string]bool, depth int) ([]Task, error) {
	file, _ := task.Params["file"].(string)

	if baseDir == "" {
		return nil, fmt.Errorf("import_tasks: no base directory available to resolve %q; build this runbook via BuildFromYAMLFile, not Build or BuildFromYAML", file)
	}
	if depth >= maxImportDepth {
		return nil, fmt.Errorf("import_tasks: exceeded max import depth of %d (a possible unbounded import chain)", maxImportDepth)
	}

	full, err := resolveImportPath(baseDir, file)
	if err != nil {
		return nil, err
	}

	if resolving[full] {
		return nil, fmt.Errorf("import_tasks: import cycle detected at %q", full)
	}
	resolving[full] = true
	defer delete(resolving, full)

	data, err := os.ReadFile(full) // #nosec G304 -- full is resolveImportPath's own output, verified below to stay under baseDir
	if err != nil {
		return nil, fmt.Errorf("import_tasks: reading %q: %w", full, err)
	}

	var imported []Task
	if err := normalizeWorkflowYAMLTaskList(data, &imported); err != nil {
		return nil, fmt.Errorf("import_tasks: parsing %q: %w", full, err)
	}

	if err := resolveImportTasksInList(imported, baseDir, resolving, depth+1); err != nil {
		return nil, err
	}

	return imported, nil
}

// resolveImportPath resolves userPath (task.Params["file"]) against
// baseDir, failing closed rather than merely escaping: an empty or
// absolute path is rejected outright, and the cleaned, joined result must
// still resolve to a path under baseDir (via filepath.Rel, refusing a
// ".." prefix), the same no-escape-by-construction style
// internal/forge/genutil.ValidateSegment already uses for a generated
// identifier's own path component.
func resolveImportPath(baseDir, userPath string) (string, error) {
	if userPath == "" {
		return "", fmt.Errorf("import_tasks: params.file is required and must be a non-empty string")
	}
	if filepath.IsAbs(userPath) {
		return "", fmt.Errorf("import_tasks: params.file %q must be a relative path", userPath)
	}

	full := filepath.Clean(filepath.Join(baseDir, userPath))

	rel, err := filepath.Rel(baseDir, full)
	if err != nil {
		return "", fmt.Errorf("import_tasks: resolving params.file %q: %w", userPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("import_tasks: params.file %q escapes the runbook's own directory", userPath)
	}

	return full, nil
}
