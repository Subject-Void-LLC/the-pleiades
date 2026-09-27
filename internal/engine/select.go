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
	sel, err := NewTagSelection(f, dag)
	if err != nil {
		return nil, err
	}
	return sel.Select(dag)
}

// TagSelection is a TagFilter checked against several runbooks together,
// for a caller that selects from all of them at once (pleiades validate
// runbooks/*). A name must be carried by some task in one of them, so a
// misspelling is still refused. A runbook that does not carry a name
// another one does is not refused: --tags web keeps only its always tasks
// there, as it would in an Ansible playbook with no web task. Build one
// with NewTagSelection; the zero value selects from nothing.
type TagSelection struct {
	// filter is the checked filter every Select applies.
	filter TagFilter

	// checked holds the runbooks filter's names were checked against,
	// the only ones Select will project.
	checked []*DAG
}

// NewTagSelection checks f against every task in dags together and
// returns the selection for them. It refuses a malformed name, or one that
// is neither special nor carried by any task in any of dags. Every dag
// must come from the builder.
func NewTagSelection(f TagFilter, dags ...*DAG) (TagSelection, error) {
	for _, dag := range dags {
		if dag.full == nil {
			return TagSelection{}, fmt.Errorf("select: DAG %q was not produced by the builder, so it has no full task set to select from", dag.ID)
		}
	}
	if err := checkFilterNames(f, dags); err != nil {
		return TagSelection{}, err
	}
	return TagSelection{filter: f, checked: slices.Clone(dags)}, nil
}

// Select returns dag projected onto the tasks the checked filter keeps,
// exactly as the package-level Select does. It refuses a dag this
// selection was not checked against: the names were checked against
// those runbooks alone, and an unchecked one is where a misspelled
// --skip-tags would skip nothing unnoticed.
func (s TagSelection) Select(dag *DAG) (*DAG, error) {
	if !slices.Contains(s.checked, dag) {
		return nil, fmt.Errorf("select: DAG %q is not one of the runbooks this tag filter was checked against", dag.ID)
	}
	f := s.filter
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
// neither special nor carried by any task in any of dags.
func checkFilterNames(f TagFilter, dags []*DAG) error {
	var carried []string
	for _, dag := range dags {
		carried = append(carried, dag.TagNames()...)
	}
	slices.Sort(carried)
	carried = slices.Compact(carried)
	// The refusal lists what the user could have meant, in the terms of
	// what they asked about: one runbook, or the several named at once.
	carries := "the tags this runbook carries are "
	if len(dags) > 1 {
		carries = "the tags these runbooks carry are "
	}
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
					known = carries + strings.Join(carried, ", ")
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
