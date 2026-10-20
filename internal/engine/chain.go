// Package engine: the walk that turns a runbook's task tree into a DAG's
// Nodes, Conditions, Adjacency, and each block's Scopes and Guards.
//
// The builder and Select (select.go) share this one walk. The builder
// registers every task, validating and compiling it; Select re-walks the
// same tree registering only the tasks a tag filter keeps, copying what
// the builder already compiled. A node's ID comes from its task's place in
// the authored tree ("tasks[2].block[0]"), never from its place among the
// kept tasks, so a filtered DAG names every task exactly as the full one
// does, and a journal entry from a filtered run points at the right line.
package engine

import (
	"fmt"
	"slices"
)

// chainWalker wires dag from a task tree. register records one kept task
// under its ID; include reports whether a leaf task is kept. A block or a
// parallel group is kept exactly when something inside it is.
type chainWalker struct {
	dag      *DAG
	register func(task *Task, id string) error
	include  func(task *Task) bool

	// scope is the blocks enclosing the task being walked, outermost
	// first, and walked is every node registered so far, in order, which a
	// block reads back to learn its own parts' nodes. guarded counts the
	// enclosing blocks that have a rescue: or an always:.
	scope  []BlockScope
	walked []string
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
// around it: itself for a leaf, a block's own tasks, then its rescue tasks,
// then its always tasks, spliced in one after another, and a fan-out/join
// pair around its children for a parallel group. Every node is on the
// chain; which of a block's tasks run on a device is the executor's
// decision, per device (handlers.go). A group with nothing kept inside is
// removed again.
func (w *chainWalker) one(task *Task, id string) (entry, exit string, err error) {
	kind := task.Kind()
	group := kind == TaskKindBlock || kind == TaskKindParallel
	if !group && !w.include(task) {
		return "", "", nil
	}
	if err := w.register(task, id); err != nil {
		return "", "", err
	}
	w.note(id)

	entry, exit = id, id
	switch kind {
	case TaskKindBlock:
		if entry, exit, err = w.block(task, id); err != nil {
			return "", "", err
		}
	case TaskKindParallel:
		if entry, exit, err = w.parallel(task.Parallel, id); err != nil {
			return "", "", err
		}
	}
	if group && entry == "" {
		w.drop(id)
	}
	return entry, exit, nil
}

// note records id's enclosing blocks and that it was walked.
func (w *chainWalker) note(id string) {
	if len(w.scope) > 0 {
		w.dag.Scopes[id] = slices.Clone(w.scope)
	}
	w.walked = append(w.walked, id)
}

// block wires a block at id: its own tasks, its rescue tasks and its always
// tasks, each part chained in order and the three one after another, and
// records the nodes of each part as the block's guard.
func (w *chainWalker) block(task *Task, id string) (entry, exit string, err error) {
	guard := BlockGuard{Rollback: task.RollbackOnFailure}
	for _, part := range []struct {
		tasks []Task
		part  BlockPart
		nodes *[]string
	}{
		{task.Block, PartBlock, &guard.Tasks},
		{task.Rescue, PartRescue, &guard.Rescue},
		{task.Always, PartAlways, &guard.Always},
	} {
		w.scope = append(w.scope, BlockScope{Block: id, Part: part.part})
		from := len(w.walked)
		partEntry, partExit, err := w.chain(part.tasks, id+"."+string(part.part))
		w.scope = w.scope[:len(w.scope)-1]
		if err != nil {
			return "", "", err
		}
		*part.nodes = w.visited(w.walked[from:])
		if partEntry == "" {
			continue
		}
		if entry == "" {
			entry = partEntry
		}
		if exit != "" {
			w.link(exit, partEntry)
		}
		exit = partExit
	}
	if entry != "" {
		w.dag.Guards[id] = guard
		for _, n := range slices.Concat(guard.Tasks, guard.Rescue, guard.Always) {
			if t := w.dag.Nodes[n]; t.Kind() == TaskKindLeaf && !onHosts(w.dag, t) {
				w.dag.Units[id] = true
				break
			}
		}
	}
	return entry, exit, nil
}

// onHosts reports whether task runs on the runbook's hosts: no target or
// within: of its own, and a method that uses a device. A block holding one
// that does not runs as one unit (DAG.Units, handlers.go).
func onHosts(dag *DAG, task *Task) bool {
	return task.Within == "" && TaskTarget(dag, task) == dag.Hosts
}

// visited returns the ids among walked that the executor's walk reaches: a
// task or a parallel group's fan-out or join, never a group's own id, and
// never one a group dropped again.
func (w *chainWalker) visited(walked []string) []string {
	var out []string
	for _, id := range walked {
		if t, ok := w.dag.Nodes[id]; ok && (t.Kind() == TaskKindLeaf || t.Kind() == TaskKindSynthetic) {
			out = append(out, id)
		}
	}
	return out
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
	w.note(fanoutID)
	w.note(joinID)
	for _, child := range kept {
		w.link(fanoutID, child.entry)
		w.link(child.exit, joinID)
	}
	return fanoutID, joinID, nil
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
	delete(w.dag.Scopes, id)
	delete(w.dag.Guards, id)
}

// registerSyntheticNode registers a structural marker node the walk
// itself constructs (a parallel group's fan-out/join pair) directly into
// dag.Nodes, bypassing validation: it has neither fqcn, block nor
// parallel by construction, which validateTask would rightly refuse as
// input, and it is not input. It carries no condition.
func registerSyntheticNode(dag *DAG, id string) {
	dag.Nodes[id] = &Task{synthetic: true}
}
