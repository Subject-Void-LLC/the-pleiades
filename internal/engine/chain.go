// Package engine: the walk that turns a runbook's task tree into a DAG's
// Nodes, Conditions and happy-path Adjacency.
//
// The builder and Select (select.go) share this one walk. The builder
// registers every task, validating and compiling it; Select re-walks the
// same tree registering only the tasks a tag filter keeps, copying what
// the builder already compiled. A node's ID comes from its task's place in
// the authored tree ("tasks[2].block[0]"), never from its place among the
// kept tasks, so a filtered DAG names every task exactly as the full one
// does, and a journal entry from a filtered run points at the right line.
package engine

import "fmt"

// chainWalker wires dag from a task tree. register records one kept task
// under its ID; include reports whether a leaf task is kept. A block or a
// parallel group is kept exactly when something inside it is.
type chainWalker struct {
	dag      *DAG
	register func(task *Task, id string) error
	include  func(task *Task) bool
}

// wire walks the runbook's three top-level lists as three independent
// happy-path chains, stitches the non-empty ones together in order, and
// sets dag.EntryPoint to the first one's entry. An empty list, or one
// every task of which was left out, contributes no section at all, so
// nothing dangles.
func (w *chainWalker) wire(pre, main, post []Task) error {
	type section struct{ entry, exit string }
	var sections []section
	for _, list := range []struct {
		tasks  []Task
		prefix string
	}{
		{pre, "pretasks"},
		{main, "tasks"},
		{post, "posttasks"},
	} {
		entry, exit, err := w.chain(list.tasks, list.prefix)
		if err != nil {
			return err
		}
		if entry != "" {
			sections = append(sections, section{entry: entry, exit: exit})
		}
	}
	for i := 1; i < len(sections); i++ {
		w.link(sections[i-1].exit, sections[i].entry)
	}
	if len(sections) > 0 {
		w.dag.EntryPoint = sections[0].entry
	}
	return nil
}

// chain walks tasks, the list at prefix, giving each task the ID
// "prefix[i]" and chaining the kept ones one after another. It returns
// the first and last node the list contributes, or two empty strings when
// it contributes none.
//
// Recursion depth is bounded before this runs: resolveImportTasks walks
// the same tree first and refuses one nested deeper than
// maxTaskNestingDepth (import_tasks.go).
func (w *chainWalker) chain(tasks []Task, prefix string) (entry, exit string, err error) {
	for i := range tasks {
		taskEntry, taskExit, err := w.one(&tasks[i], fmt.Sprintf("%s[%d]", prefix, i))
		if err != nil {
			return "", "", err
		}
		if taskEntry == "" {
			continue
		}
		if entry == "" {
			entry = taskEntry
		}
		if exit != "" {
			w.link(exit, taskEntry)
		}
		exit = taskExit
	}
	return entry, exit, nil
}

// one registers task at id and returns what it contributes to the chain
// around it: itself for a leaf, its block's own chain spliced in for a
// block, and a fan-out/join pair around its children for a parallel
// group. Its rescue and always tasks are registered for coverage only and
// never join the chain (DAG.Adjacency). A group with nothing kept inside,
// rescue and always included, is removed again.
func (w *chainWalker) one(task *Task, id string) (entry, exit string, err error) {
	kind := task.Kind()
	group := kind == TaskKindBlock || kind == TaskKindParallel
	if !group && !w.include(task) {
		return "", "", nil
	}
	if err := w.register(task, id); err != nil {
		return "", "", err
	}

	entry, exit = id, id
	switch kind {
	case TaskKindBlock:
		if entry, exit, err = w.chain(task.Block, id+".block"); err != nil {
			return "", "", err
		}
	case TaskKindParallel:
		if entry, exit, err = w.parallel(task.Parallel, id); err != nil {
			return "", "", err
		}
	}

	covered := 0
	for _, side := range []struct {
		tasks []Task
		label string
	}{{task.Rescue, ".rescue"}, {task.Always, ".always"}} {
		kept, err := w.collect(side.tasks, id+side.label)
		if err != nil {
			return "", "", err
		}
		covered += kept
	}
	if group && entry == "" && covered == 0 {
		w.drop(id)
	}
	return entry, exit, nil
}

// parallel wires a parallel group at id: each kept child's own chain
// between a synthetic fan-out node (id+".fanout") and join node
// (id+".join"). The pair is added only when at least one child is kept.
// The level iterator already groups concurrent siblings from any
// multi-parent, multi-child Adjacency, so this shape needs nothing else.
func (w *chainWalker) parallel(children []Task, id string) (fanoutID, joinID string, err error) {
	type span struct{ entry, exit string }
	var kept []span
	for i := range children {
		entry, exit, err := w.one(&children[i], fmt.Sprintf("%s.parallel[%d]", id, i))
		if err != nil {
			return "", "", err
		}
		if entry != "" {
			kept = append(kept, span{entry, exit})
		}
	}
	if len(kept) == 0 {
		return "", "", nil
	}
	fanoutID, joinID = id+".fanout", id+".join"
	registerSyntheticNode(w.dag, fanoutID)
	registerSyntheticNode(w.dag, joinID)
	for _, child := range kept {
		w.link(fanoutID, child.entry)
		w.link(child.exit, joinID)
	}
	return fanoutID, joinID, nil
}

// collect registers tasks, a rescue or always list at prefix, for
// Nodes and Conditions coverage only, at any depth, never touching
// Adjacency, and returns how many it kept. Everything under a rescue or
// always list stays off the chain, a parallel group's children included,
// which is why a group here gets no fan-out/join pair.
func (w *chainWalker) collect(tasks []Task, prefix string) (int, error) {
	kept := 0
	for i := range tasks {
		task := &tasks[i]
		id := fmt.Sprintf("%s[%d]", prefix, i)
		kind := task.Kind()
		group := kind == TaskKindBlock || kind == TaskKindParallel
		if !group && !w.include(task) {
			continue
		}
		if err := w.register(task, id); err != nil {
			return 0, err
		}
		inside := 0
		for _, sub := range []struct {
			tasks []Task
			label string
		}{{task.Block, ".block"}, {task.Parallel, ".parallel"}, {task.Rescue, ".rescue"}, {task.Always, ".always"}} {
			n, err := w.collect(sub.tasks, id+sub.label)
			if err != nil {
				return 0, err
			}
			inside += n
		}
		if group && inside == 0 {
			w.drop(id)
			continue
		}
		kept++
	}
	return kept, nil
}

// link adds a happy-path edge from one node to the next.
func (w *chainWalker) link(from, to string) {
	w.dag.Adjacency[from] = append(w.dag.Adjacency[from], EdgeConfig{To: to})
}

// drop removes a group registered before the walk learned nothing inside
// it was kept.
func (w *chainWalker) drop(id string) {
	delete(w.dag.Nodes, id)
	delete(w.dag.Conditions, id)
}

// registerSyntheticNode registers a structural marker node the walk
// itself constructs (a parallel group's fan-out/join pair) directly into
// dag.Nodes, bypassing validation: it has neither fqcn, block nor
// parallel by construction, which validateTask would rightly refuse as
// input, and it is not input. It carries no condition.
func registerSyntheticNode(dag *DAG, id string) {
	dag.Nodes[id] = &Task{synthetic: true}
}
