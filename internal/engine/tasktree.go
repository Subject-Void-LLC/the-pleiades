package engine

import "fmt"

// taskLabel formats a task's synthesized ID and, if present, its Name for
// use in an error message, e.g. `tasks[2] (name "reboot device")`. Name is
// a free-form human label with no uniqueness requirement, so it is
// included only to help a human locate the offending task, never as part
// of the ID itself.
func taskLabel(id string, task *Task) string {
	if task.Name == "" {
		return id
	}
	return fmt.Sprintf("%s (name %q)", id, task.Name)
}

// validateTask checks the structural rules a Task must satisfy regardless
// of where it appears in the tree: exactly one of FQCN (a leaf task),
// Block (a block task), or Parallel (a parallel task) must be set, having
// more than one or none of them is an error, mirroring Ansible where a
// task is either a module call or a block (Parallel is Pleiades' own
// addition, PLAN.md Section 14). It also rejects Rescue or Always set
// without a Block, since rescue/always without a block to guard is
// meaningless: there is nothing that could fail, and a Parallel task
// carrying either is rejected the same way, deliberately - Ansible has no
// established parallel-failure-handling vocabulary to mirror. Every error
// names the task's synthesized ID (and Name, if set) so it can be found in
// a large runbook.
func validateTask(task *Task, id string) error {
	if task.Kind() == TaskKindInvalid {
		hasFQCN, hasBlock, hasParallel := taskShape(task)
		var set []string
		if hasFQCN {
			set = append(set, "fqcn")
		}
		if hasBlock {
			set = append(set, "block")
		}
		if hasParallel {
			set = append(set, "parallel")
		}
		if len(set) == 0 {
			return fmt.Errorf("task %s has none of fqcn, block, or parallel: a task must be exactly one of a module call (fqcn), a block, or a parallel group", taskLabel(id, task))
		}
		return fmt.Errorf("task %s sets more than one of fqcn, block, and parallel (%v): a task must be exactly one of a module call (fqcn), a block, or a parallel group, never more than one", taskLabel(id, task), set)
	}

	if task.Kind() != TaskKindBlock && (len(task.Rescue) > 0 || len(task.Always) > 0) {
		return fmt.Errorf("task %s sets rescue or always without a block: rescue/always only make sense guarding a block", taskLabel(id, task))
	}

	if task.SecretMask != nil {
		if task.SecretMask.Register == "" {
			return fmt.Errorf("task %s sets secret_mask with an empty register: it must name the earlier task's register to mask fields of", taskLabel(id, task))
		}
		if len(task.SecretMask.Fields) == 0 {
			return fmt.Errorf("task %s sets secret_mask with no fields: it must name at least one field to mask", taskLabel(id, task))
		}
	}

	return nil
}

// registerTask compiles task's embedded Conditional and records task in
// dag.Nodes (and, if conditional, dag.Conditions), both keyed by id. It is
// shared by synthesizeChain (the happy-path walk) and collectSubtree (the
// Rescue/Always coverage-only walk), since both need identical
// validate-compile-register handling for every task they visit.
func (b *Builder) registerTask(dag *DAG, task *Task, id string) error {
	if err := validateTask(task, id); err != nil {
		return err
	}

	// Compile this task's optional when/when_or/when_cel skip condition.
	// An unconditional task yields a nil Program and no error, so it gets
	// no entry in dag.Conditions.
	prg, err := task.Conditional.Compile(b.cel)
	if err != nil {
		return fmt.Errorf("failed to compile when-condition for task %s: %w", taskLabel(id, task), err)
	}
	if prg != nil {
		dag.Conditions[id] = prg
	}

	dag.Nodes[id] = task
	return nil
}

// synthesizeChain walks tasks, an ordered list appearing at prefix (e.g.
// "tasks" for the top-level list, or "tasks[2].block" for a block task's
// children), assigning each task the synthesized ID "prefix[i]" (via
// synthesizeOne), and wires dag.Adjacency into a single happy-path chain
// across them.
//
// It returns the entry (first) and exit (last) node ID reached by this
// list, so the caller can splice this list's chain into its own
// surrounding one. An empty tasks list returns ("", "").
//
// Recursion depth here (into a task's own Block/Parallel children, via
// synthesizeOne) is bounded transitively, not by a check in this
// function: resolveImportTasks (import_tasks.go) walks this identical
// tree first, unconditionally, as buildFromDef's very first step
// (dag.go), and rejects anything nested deeper than maxTaskNestingDepth
// before synthesizeChain ever runs - see maxTaskNestingDepth's own doc
// comment for why bounding it there, once, is enough.
func (b *Builder) synthesizeChain(dag *DAG, tasks []Task, prefix string) (entry, exit string, err error) {
	for i := range tasks {
		id := fmt.Sprintf("%s[%d]", prefix, i)

		taskEntry, taskExit, err := b.synthesizeOne(dag, &tasks[i], id)
		if err != nil {
			return "", "", err
		}

		if entry == "" {
			// First task in this list: it is this list's entry point.
			entry = taskEntry
		}
		if exit != "" {
			// Chain the previous task's exit into this task's entry.
			dag.Adjacency[exit] = append(dag.Adjacency[exit], EdgeConfig{To: taskEntry})
		}
		exit = taskExit
	}
	return entry, exit, nil
}

// synthesizeOne registers task at the caller-supplied id and returns the
// entry/exit node(s) it contributes to a surrounding chain: itself, for a
// leaf task; its Block children's own chain, spliced in, for a block
// task; a synthetic fan-out/join pair wrapping each Parallel child's own
// independently synthesized chain, for a parallel task (synthesizeParallel).
// Rescue and Always are walked separately via collectSubtree, for
// Nodes/Conditions coverage only, never joining the chain.
//
// Shared by synthesizeChain (list stitching, id = "prefix[i]") and
// synthesizeParallel (id = "prefix.parallel[i]", one independent call per
// child rather than a chained list), so a single task's own splice logic
// exists in exactly one place regardless of which shape reached it.
func (b *Builder) synthesizeOne(dag *DAG, task *Task, id string) (entry, exit string, err error) {
	if err := b.registerTask(dag, task, id); err != nil {
		return "", "", err
	}

	taskEntry, taskExit := id, id
	switch task.Kind() {
	case TaskKindBlock:
		blockEntry, blockExit, err := b.synthesizeChain(dag, task.Block, id+".block")
		if err != nil {
			return "", "", err
		}
		taskEntry, taskExit = blockEntry, blockExit
	case TaskKindParallel:
		fanoutID, joinID, err := b.synthesizeParallel(dag, task.Parallel, id)
		if err != nil {
			return "", "", err
		}
		taskEntry, taskExit = fanoutID, joinID
	}

	if len(task.Rescue) > 0 {
		if err := b.collectSubtree(dag, task.Rescue, id+".rescue"); err != nil {
			return "", "", err
		}
	}
	if len(task.Always) > 0 {
		if err := b.collectSubtree(dag, task.Always, id+".always"); err != nil {
			return "", "", err
		}
	}

	return taskEntry, taskExit, nil
}

// synthesizeParallel builds the fan-out/join splice for a parallel task at
// id: a synthetic fan-out node (id+".fanout") and join node (id+".join"),
// each registered via registerSyntheticNode rather than synthesizeOne
// (neither is user input, so neither goes through validateTask). Each
// child in children is synthesized independently at "id.parallel[i]" (its
// own entry/exit, not chained to its siblings the way synthesizeChain
// chains an ordinary list), then wired fanout -> child's entry and
// child's exit -> join.
//
// This needs zero change to LevelIterator/TopologicalOrder: both already
// compute reachability generically over Adjacency
// (reachableWithInDegree, topology.go), and TestLevelIterator_Diamond
// already proves a hand-built multi-parent/multi-child graph groups
// concurrent siblings into one level and holds the join node back
// correctly - only Builder needed to learn to produce that shape.
func (b *Builder) synthesizeParallel(dag *DAG, children []Task, id string) (fanoutID, joinID string, err error) {
	fanoutID = id + ".fanout"
	joinID = id + ".join"
	registerSyntheticNode(dag, fanoutID)
	registerSyntheticNode(dag, joinID)

	for i := range children {
		childID := fmt.Sprintf("%s.parallel[%d]", id, i)
		childEntry, childExit, err := b.synthesizeOne(dag, &children[i], childID)
		if err != nil {
			return "", "", err
		}
		dag.Adjacency[fanoutID] = append(dag.Adjacency[fanoutID], EdgeConfig{To: childEntry})
		dag.Adjacency[childExit] = append(dag.Adjacency[childExit], EdgeConfig{To: joinID})
	}
	return fanoutID, joinID, nil
}

// registerSyntheticNode registers a structural marker node the Builder
// itself constructs (synthesizeParallel's fan-out/join pair) directly into
// dag.Nodes, bypassing registerTask/validateTask entirely: a synthetic
// node has neither fqcn, block, nor parallel by construction, which
// validateTask would otherwise correctly reject as invalid input - it is
// not user input, so it does not go through the user-input validation
// gate. It carries no Conditional (an author never writes one for a node
// that does not exist in their runbook), so it gets no dag.Conditions
// entry either.
func registerSyntheticNode(dag *DAG, id string) {
	dag.Nodes[id] = &Task{synthetic: true}
}

// collectSubtree walks tasks (a Rescue or Always list, at prefix) purely
// for Nodes/Conditions coverage: it validates and registers every task, and
// recurses into any further Block/Rescue/Always/Parallel nesting, but
// never touches dag.Adjacency. This mirrors why Rescue and Always are
// excluded from the happy-path chain (see DAG.Adjacency's doc comment):
// once inside a Rescue/Always subtree, none of its descendants, at any
// depth, join the chain either - including a Parallel task's own children,
// which get Nodes/Conditions coverage here but no synthetic fan-out/join
// pair, since there is no chain position for one to splice into.
func (b *Builder) collectSubtree(dag *DAG, tasks []Task, prefix string) error {
	for i := range tasks {
		task := &tasks[i]
		id := fmt.Sprintf("%s[%d]", prefix, i)

		if err := b.registerTask(dag, task, id); err != nil {
			return err
		}

		switch task.Kind() {
		case TaskKindBlock:
			if err := b.collectSubtree(dag, task.Block, id+".block"); err != nil {
				return err
			}
		case TaskKindParallel:
			if err := b.collectSubtree(dag, task.Parallel, id+".parallel"); err != nil {
				return err
			}
		}
		if len(task.Rescue) > 0 {
			if err := b.collectSubtree(dag, task.Rescue, id+".rescue"); err != nil {
				return err
			}
		}
		if len(task.Always) > 0 {
			if err := b.collectSubtree(dag, task.Always, id+".always"); err != nil {
				return err
			}
		}
	}
	return nil
}
