// Package engine: a block's rollback_on_failure
// (docs/design/block-rescue-always.md, section 5).
//
// When a task of such a block fails on a device, the engine undoes what the
// block's own tasks completed there, newest first, before the block's rescue
// starts, so the rescue starts from where the block began. Each undo step is
// journaled as a rollback of the node it undoes (RollbackOf names this run,
// UndoesNode and UndoesStep the node and step), so a later `pleiades
// rollback` of the run finds it already undone and does not repeat it. The
// steps are the ones that rollback would run: the task's authored rollback:
// list, or the undo its method recorded, and only a recorded undo the
// journal holds exactly (InverseComplete), since validate refuses the key
// on a block holding a task with any other kind.
//
// An undo that fails, or that cannot be run at all, leaves the device in a
// state nobody chose: the rescue does not run, the always tasks do, and the
// run fails with the block's failure and the undo's. The task that failed
// is undone first when its method kept its undo through the failure
// (sdk.Inverse.KeepOnFailure, which the executor journals as a change);
// otherwise a failed task records no undo, and what it did is the rescue's
// to answer.
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// UndoStep is what NodeResult.Undo carries: the node and step undone, and
// the name and method of the task the step ran.
type UndoStep struct {
	Undo
	Name, Method string
}

// undoNodeID names step step of the undo of node, in this run's journal.
func undoNodeID(node string, step int) string {
	return fmt.Sprintf("%s.undo[%d]", node, step)
}

// taskFor returns the task behind nodeID: the DAG's, or an undo step's.
func (r *run) taskFor(nodeID string) *Task {
	if t := r.dag.Nodes[nodeID]; t != nil {
		return t
	}
	return r.undoTasks[nodeID]
}

// rollBack runs every undo the handlers have asked for since it last ran,
// and reports whether a failed undo leaves a failure with nothing left to
// run for it, which ends the run.
func (r *run) rollBack(ctx context.Context, levelIndex int, result *RunResult) (fatal bool) {
	for _, req := range r.handlers.takePending() {
		failure := r.undoBlock(ctx, levelIndex, req, result)
		if req.frame == nil {
			// A block that failed everywhere at once: the run ends anyway.
			if failure != nil {
				fatal = true
			}
			continue
		}
		req.frame.undoPending = false
		if failure == nil {
			continue
		}
		req.frame.undoFailed = true
		req.frame.causes = append(req.frame.causes, *failure)
		if !r.handlers.handled(req.key) {
			fatal = true
		}
	}
	return fatal
}

// undoBlock undoes, newest first, every change req.block's own tasks made
// on req's devices this run, and returns the result that stopped it: a step
// that failed, or a change it could not undo exactly.
func (r *run) undoBlock(ctx context.Context, levelIndex int, req rollbackRequest, result *RunResult) *NodeResult {
	// A check predicts and changes nothing, and is never journaled: there
	// is nothing to undo.
	if r.mode != collection.ModeExecute {
		return nil
	}
	for i := len(result.Nodes) - 1; i >= 0; i-- {
		n := result.Nodes[i]
		if n.Undo != nil || n.Checked || !n.journalChanged || !inPartOf(r.dag.Scopes[n.NodeID], req.block, PartBlock) {
			continue
		}
		if !req.all && !strings.HasPrefix(req.key, "unit:") && n.Device != req.key {
			continue
		}
		key := resultKey{n.NodeID, n.Device}
		if r.undone[key] {
			continue
		}
		r.undone[key] = true
		steps, authored, err := r.undoSteps(n)
		if err != nil {
			refused := r.undoRefused(n, err)
			result.Nodes = append(result.Nodes, refused)
			return &result.Nodes[len(result.Nodes)-1]
		}
		var device inventory.InventoryItem
		if n.Device != "" {
			r.itemsMu.Lock()
			device = r.items[n.Device]
			r.itemsMu.Unlock()
		}
		for step, task := range steps {
			res := r.runUndoStep(ctx, n, step, task, authored, device)
			r.recordLevel(ctx, levelIndex, [][]NodeResult{{res}})
			result.Nodes = append(result.Nodes, res)
			if res.Err != nil {
				return &result.Nodes[len(result.Nodes)-1]
			}
		}
	}
	return nil
}

// runUndoStep runs one step of n's undo on device. An authored step renders
// as any task does; a recorded undo's params are the values the method
// recorded and are run as they are, since one holding "{{" is a value.
func (r *run) runUndoStep(ctx context.Context, n NodeResult, step int, task *Task, authored bool, device inventory.InventoryItem) NodeResult {
	id := undoNodeID(n.NodeID, step)
	r.undoTasks[id] = task
	undo := &UndoStep{Undo: Undo{Node: n.NodeID, Step: step}, Name: task.Name, Method: task.FQCN}
	if authored {
		prepared, early := r.prepare(id, task, time.Now().UTC(), n.Device, map[string]any{TemplateRootDevice: deviceRoot(device)})
		if early != nil {
			early.Undo = undo
			return *early
		}
		task = prepared
		r.undoTasks[id] = task
	}
	res := r.runOne(ctx, nodeExecution{NodeID: id, Task: task, Device: device})
	res.Undo = undo
	return res
}

// undoSteps returns the tasks that undo n, as a later rollback of the run
// would run them: the authored rollback: list, or the one call the method
// recorded, when the journal holds it exactly. It refuses a change with
// neither, which validate keeps off a block with rollback_on_failure but a
// run can still meet (a value the journal could not hold).
func (r *run) undoSteps(n NodeResult) (steps []*Task, authored bool, err error) {
	task := r.dag.Nodes[n.NodeID]
	if task == nil {
		return nil, false, fmt.Errorf("%s names no task of the runbook", n.NodeID)
	}
	// A rollback: list that is present and empty says undoing the task means
	// doing nothing: no steps, nothing journaled, and a later rollback of
	// the run reports it as nothing to undo.
	if task.Rollback != nil {
		for i := range task.Rollback {
			step := task.Rollback[i]
			if step.Name == "" {
				step.Name = fmt.Sprintf("undo %s, step %d", taskLabel(n.NodeID, task), i+1)
			}
			asAccountOf(&step, task)
			steps = append(steps, &step)
		}
		return steps, true, nil
	}
	resolved := resolveFQCN(task.FQCN)
	if resolved.engineAction {
		return nil, false, fmt.Errorf("%s ran %s, an engine action whose effect no recorded undo reverses", taskLabel(n.NodeID, task), task.FQCN)
	}
	var entry JournalEntry
	var emitter *collection.Descriptor
	if desc, ok := collection.Lookup(task.FQCN); ok {
		emitter = &desc
	}
	projectInverse(&entry, n.journalStats, emitter)
	switch {
	case entry.InverseFQCN == "":
		return nil, false, fmt.Errorf("%s changed the device and recorded no undo", taskLabel(n.NodeID, task))
	case !entry.InverseComplete:
		return nil, false, fmt.Errorf("%s recorded an undo the journal cannot hold exactly (part of it, or a value too long or not plain), so it cannot be replayed", taskLabel(n.NodeID, task))
	}
	params := make(map[string]interface{}, len(entry.InverseParams))
	for _, p := range entry.InverseParams {
		if v, ok := p.Value(); ok {
			params[p.Key] = v
		}
	}
	undo := &Task{Name: "undo " + taskLabel(n.NodeID, task), FQCN: entry.InverseFQCN, Params: params}
	asAccountOf(undo, task)
	return []*Task{undo}, false, nil
}

// asAccountOf makes step run as the account task ran as, when it ran as one,
// the way a later pleiades rollback replays the undo (rollback.Step.Become): an
// undo of a change made as root that ran as the login would meet files and
// state it has no right to, or fail.
func asAccountOf(step, task *Task) {
	if user, on := BecomeFor(task); on {
		yes := true
		step.Become, step.BecomeUser, step.BecomeMethod = &yes, user, BecomeMethodSudo
	}
}

// undoRefused is the failed result of a change undoBlock could not undo: it
// ran nothing, so it is reported and not journaled.
func (r *run) undoRefused(n NodeResult, err error) NodeResult {
	id := undoNodeID(n.NodeID, 0)
	now := time.Now().UTC()
	res := NodeResult{NodeID: id, Device: n.Device, StartedAt: now, FinishedAt: now,
		Undo: &UndoStep{Undo: Undo{Node: n.NodeID}}}
	res.fail(FailureStageAction, fmt.Errorf("could not undo it before the rescue: %w", err))
	r.publish(id, r.dag.Nodes[n.NodeID], "", "failed", res.Err.Error())
	return res
}

// inPartOf reports whether a task with scopes sits in part of block.
func inPartOf(scopes []BlockScope, block string, part BlockPart) bool {
	for _, s := range scopes {
		if s.Block == block && s.Part == part {
			return true
		}
	}
	return false
}
