// Package engine: Select, which projects a built runbook onto the tasks a
// tag filter keeps (--tags and --skip-tags), without rebuilding it.
package engine

import (
	"fmt"
	"slices"
	"strings"
)

// fullGraph is every task and compiled condition a build produced, before
// any tag selection.
type fullGraph struct {
	nodes      map[string]*Task
	conditions map[string]*ConditionProgram
}

// filterSpecialTags are the names a filter may use whether or not any
// task carries them.
var filterSpecialTags = []string{TagAll, TagAlways, TagNever, TagTagged, TagUntagged}

// Select returns dag projected onto the tasks f keeps: Nodes, Conditions,
// Adjacency and EntryPoint cover only those, under the node IDs the full
// runbook gives them, and Selection records f. It never changes dag, so
// one DAG a caller shares (the Runner caches them) can be selected from
// concurrently. ID, Version and the authored task lists are dag's own:
// the version names the runbook, and a filter is a property of the run.
//
// It refuses a filter naming a tag no task carries, since a misspelled
// --skip-tags would otherwise skip nothing and run what the operator meant
// to hold back. For an explicit filter it also refuses a selection that
// keeps a task reading a registered result only a left-out task produces:
// the kept task would read nothing and its condition would not mean what
// it says. The builder's own default projection skips that second check,
// as Ansible does: a task reading a never task's result is the runbook's
// author's to guard, exactly as in the playbook it came from.
//
// dag must come from the builder, which records the full task set Select
// projects from.
func Select(dag *DAG, f TagFilter) (*DAG, error) {
	if dag.full == nil {
		return nil, fmt.Errorf("select: DAG %q was not produced by the builder, so it has no full task set to select from", dag.ID)
	}
	if err := checkFilterNames(f, dag.full); err != nil {
		return nil, err
	}
	out := &DAG{
		ID:         dag.ID,
		Metadata:   dag.Metadata,
		CheckMode:  dag.CheckMode,
		Name:       dag.Name,
		Hosts:      dag.Hosts,
		Version:    dag.Version,
		PreTasks:   dag.PreTasks,
		Tasks:      dag.Tasks,
		PostTasks:  dag.PostTasks,
		Nodes:      make(map[string]*Task),
		Adjacency:  make(map[string][]EdgeConfig),
		Conditions: make(map[string]*ConditionProgram),
		Selection:  f,
		full:       dag.full,
	}
	w := chainWalker{
		dag: out,
		register: func(task *Task, id string) error {
			out.Nodes[id] = task
			if prg := dag.full.conditions[id]; prg != nil {
				out.Conditions[id] = prg
			}
			return nil
		},
		include: func(task *Task) bool { return f.Runs(task.Tags) },
	}
	if err := w.wire(dag.PreTasks, dag.Tasks, dag.PostTasks); err != nil {
		return nil, err
	}
	if !f.IsZero() {
		if err := checkSelectedReads(out, dag.full); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// TagNames returns every tag a task in dag carries, inherited ones
// included, sorted: the names a filter for this runbook can use besides
// the special ones.
func (d *DAG) TagNames() []string {
	nodes := d.Nodes
	if d.full != nil {
		nodes = d.full.nodes
	}
	var names []string
	for _, task := range nodes {
		for _, t := range task.Tags {
			if !slices.Contains(names, t) {
				names = append(names, t)
			}
		}
	}
	slices.Sort(names)
	return names
}

// checkFilterNames refuses a name in f that is malformed, or that is
// neither special nor carried by any task in full.
func checkFilterNames(f TagFilter, full *fullGraph) error {
	carried := (&DAG{full: full}).TagNames()
	for _, set := range []struct {
		flag  string
		names []string
	}{{"--tags", f.Tags}, {"--skip-tags", f.SkipTags}} {
		for _, name := range set.names {
			if err := checkTagName(name); err != nil {
				return fmt.Errorf("%s: %w", set.flag, err)
			}
			if !slices.Contains(filterSpecialTags, name) && !slices.Contains(carried, name) {
				known := "no task carries a tag"
				if len(carried) > 0 {
					known = "the tags this runbook carries are " + strings.Join(carried, ", ")
				}
				return fmt.Errorf("%s names %q, which no task carries; %s", set.flag, name, known)
			}
		}
	}
	return nil
}

// checkSelectedReads refuses a projection that keeps a task whose
// condition or secret_mask reads a register that only left-out tasks
// produce. A condition whose reads cannot be known before the run (a
// computed index) is not refused: nothing here can say what it reads.
func checkSelectedReads(out *DAG, full *fullGraph) error {
	producedBy := func(nodes map[string]*Task, name string) bool {
		for _, t := range nodes {
			if t.Register == name {
				return true
			}
		}
		return false
	}
	ids := make([]string, 0, len(out.Nodes))
	for id := range out.Nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		task := out.Nodes[id]
		reads, _, err := ConditionReads(task.Conditional)
		if err != nil {
			return fmt.Errorf("task %s: %w", taskLabel(id, task), err)
		}
		if task.SecretMask != nil {
			reads = append(reads, task.SecretMask.Register)
		}
		for _, name := range reads {
			if producedBy(full.nodes, name) && !producedBy(out.Nodes, name) {
				return fmt.Errorf("task %s reads the result registered as %q, but every task that registers it is left out by this selection; add that task's tag to --tags, or tag it always", taskLabel(id, task), name)
			}
		}
	}
	return nil
}
