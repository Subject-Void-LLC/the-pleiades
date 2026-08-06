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

// maxTaskNestingDepth bounds two distinct risks with one shared counter:
// how many import_tasks hops a single chain may take (A imports B imports
// C ...; cycle detection, see resolving below, already rejects a file
// that imports itself, but a long, non-cyclic chain is still worth
// bounding rather than trusting to be finite by construction), and how
// many levels deep plain block/rescue/always/parallel nesting may go
// within one file, a risk that has nothing to do with import_tasks at
// all. Both share one counter (depth, below) because resolveImportTasksInList
// is the one function that walks both shapes, and unbounded recursion is
// the same defect class either way: Go's goroutine stacks grow
// dynamically (unlike a fixed-size C stack), so a single small payload
// does not trip this the way it might in another language, but the
// ceiling (1GB by default, runtime/debug.SetMaxStack) is still finite and
// a fatal, unrecoverable process crash, not a catchable panic, once
// reached - and several concurrent requests each nested deeply enough can
// exhaust process memory well before any single one hits that ceiling.
// Kept small (32, unchanged from this constant's original import-only
// value) because no legitimate runbook plausibly nests this deep at all:
// this is a defensive ceiling, not a working limit anyone should expect
// to approach.
//
// This is also, deliberately, the only depth bound this package needs:
// resolveImportTasksInList runs first, unconditionally, on the complete
// tree (resolveImportTasks is buildFromDef's very first step, dag.go),
// visiting every Block/Rescue/Always/Parallel level to look for
// import_tasks tasks to resolve, whether or not any exist. Bounding it
// here transitively bounds tasktree.go's synthesizeChain/collectSubtree
// too, since neither ever sees a tree deeper than what already passed
// through this check - checking the same thing twice would be redundant,
// not more defensive. The same defensive-bound style
// internal/forge/genutil.ValidateSegments already uses for segment count.
const maxTaskNestingDepth = 32

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
// Block/Rescue/Always/Parallel (imported or hand-authored) to reach every
// nesting depth. tasks is mutated through its backing array
// (task := &tasks[i]), exactly like tasktree.go's synthesizeChain/
// collectSubtree, so callers passing a WorkflowDef field's slice see the
// mutation without needing to reassign the field themselves.
//
// depth counts every level of recursion this function itself takes.
// See maxTaskNestingDepth's own doc comment for why this one check
// stands in for a depth check in tasktree.go too.
func resolveImportTasksInList(tasks []Task, baseDir string, resolving map[string]bool, depth int) error {
	if depth > maxTaskNestingDepth {
		return fmt.Errorf("import_tasks: exceeded max task nesting depth of %d (block/rescue/always/parallel nested too deeply, or an unbounded import chain)", maxTaskNestingDepth)
	}

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

		for _, sub := range [][]Task{task.Block, task.Rescue, task.Always, task.Parallel} {
			if len(sub) > 0 {
				if err := resolveImportTasksInList(sub, baseDir, resolving, depth+1); err != nil {
					return err
				}
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
	if depth >= maxTaskNestingDepth {
		return nil, fmt.Errorf("import_tasks: exceeded max import depth of %d (a possible unbounded import chain)", maxTaskNestingDepth)
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
