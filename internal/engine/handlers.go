// Package engine: a block's rescue: and always:, run per device
// (docs/design/block-rescue-always.md).
//
// The walk stays one chain for every device: a block's tasks, then its
// rescue tasks, then its always tasks, then whatever follows the block.
// Each device keeps a stack of failures its blocks are handling (frames),
// and each node asks, per device, whether it runs there: a block task does
// not run on a device where an earlier task of the block failed, a rescue
// task runs only where its block failed, and an always task runs whatever
// happened. A failure stops the run only when no rescue: or always: is left
// that would run for it, so a runbook without either behaves exactly as it
// did: its first failure ends the walk.
package engine

import (
	"fmt"
	"slices"
)

// BlockPart names where a task sits in a block that encloses it.
type BlockPart string

// The three parts of a block.
const (
	PartBlock  BlockPart = "block"
	PartRescue BlockPart = "rescue"
	PartAlways BlockPart = "always"
)

// BlockScope is one block enclosing a task, and the part of it the task is
// in. DAG.Scopes lists them outermost first.
type BlockScope struct {
	Block string
	Part  BlockPart
}

// BlockGuard is what a block has besides its own tasks, and every node of
// each part, at any depth, for knowing when the walk has passed them.
type BlockGuard struct {
	Rescue, Always []string
	Tasks          []string
}

// HasRescue and HasAlways report whether the block kept a rescue: or an
// always: task after tag selection.
func (g BlockGuard) HasRescue() bool { return len(g.Rescue) > 0 }

// HasAlways reports whether the block kept an always: task.
func (g BlockGuard) HasAlways() bool { return len(g.Always) > 0 }

// InPart reports whether a task with scopes sits in part of any block that
// encloses it.
func InPart(scopes []BlockScope, part BlockPart) bool {
	return slices.ContainsFunc(scopes, func(s BlockScope) bool { return s.Part == part })
}

// Guarded reports whether a task with scopes sits inside a block that has a
// rescue: or an always:, which is what makes its device's failures
// something other than the end of the run.
func (d *DAG) Guarded(scopes []BlockScope) bool {
	return slices.ContainsFunc(scopes, func(s BlockScope) bool {
		g := d.Guards[s.Block]
		return g.HasRescue() || g.HasAlways()
	})
}

// handlerFrame is one block's handling of a failure on one device: from
// the task that failed until the walk has passed every task of the block.
type handlerFrame struct {
	block string
	// part is where the failure entered the block: its own tasks (the one
	// failure a rescue answers), or its always tasks.
	part BlockPart
	// origin is the failure the block is handling, the first that reached
	// it, and causes every failure since that belongs to the same handling:
	// a rescue task that failed, an always task that failed.
	origin NodeResult
	causes []NodeResult
	// held is a failure held for a person's decision, which no rescue
	// answers: the always tasks run and the run ends held.
	held bool
	// rescueFailed and alwaysFailed are a rescue task, or an always task,
	// that failed while the frame was handling: the rest of that part does
	// not run.
	rescueFailed, alwaysFailed bool
}

// handlers is every device's stack of frames, keyed by device ID, the
// empty string keying a task that runs with no device, and a block that
// runs as one unit (DAG.Units) keying all of its devices together (key). Run reads and
// writes it at the level barrier only; nodes running in a level read it,
// and nothing changes it while they do.
type handlers struct {
	dag    *DAG
	frames map[string][]*handlerFrame
	done   map[string]bool
	// rescued maps each node result that a rescue answered, by node and
	// device, to the rescue's first task.
	rescued map[resultKey]string
}

// resultKey names one node's result on one device.
type resultKey struct{ node, device string }

func newHandlers(dag *DAG) *handlers {
	return &handlers{dag: dag, frames: map[string][]*handlerFrame{}, done: map[string]bool{}, rescued: map[resultKey]string{}}
}

// skip is why a node does not run on a device.
type skip struct {
	kind   SkipKind
	reason string
}

// key is the handling nodeID's failures and decisions on device belong to:
// the outermost unit block enclosing it, whatever the device, or the
// device itself.
func (h *handlers) key(nodeID, device string) string {
	for _, s := range h.dag.Scopes[nodeID] {
		if h.dag.Units[s.Block] {
			return "unit:" + s.Block
		}
	}
	return device
}

// decide reports whether nodeID runs on device, and when it does not, why.
func (h *handlers) decide(nodeID, device string) (bool, skip) {
	scopes := h.dag.Scopes[nodeID]
	frames := h.frames[h.key(nodeID, device)]
	inner := scopes
	if len(frames) > 0 {
		top := frames[len(frames)-1]
		at := slices.IndexFunc(scopes, func(s BlockScope) bool { return s.Block == top.block })
		if at < 0 {
			// The frame's block is not this node's: the walk has not yet
			// passed it (advance would have popped it), so this node is a
			// sibling the failure keeps from running, as it would outside
			// any block.
			return false, skip{SkipKindBlockFailed, fmt.Sprintf("%s failed on this device", top.origin.NodeID)}
		}
		switch scopes[at].Part {
		case PartBlock:
			return false, skip{SkipKindBlockFailed, fmt.Sprintf("%s failed on this device earlier in its block", top.origin.NodeID)}
		case PartRescue:
			switch {
			case top.part != PartBlock:
				return false, skip{SkipKindNotFailed, "runs only if its block fails, and on this device it did not"}
			case top.held:
				return false, skip{SkipKindBlockFailed, fmt.Sprintf("%s is held for a person's decision, which no rescue answers", top.origin.NodeID)}
			case top.rescueFailed:
				return false, skip{SkipKindBlockFailed, fmt.Sprintf("an earlier task of this rescue failed while it handled %s", top.origin.NodeID)}
			}
		case PartAlways:
			if top.alwaysFailed {
				return false, skip{SkipKindBlockFailed, "an earlier task of this always: failed on this device"}
			}
		}
		inner = scopes[at+1:]
	}
	// Below the block handling a failure, or with none, a rescue runs only
	// for a failure, and there is none in these blocks on this device.
	if InPart(inner, PartRescue) {
		return false, skip{SkipKindNotFailed, "runs only if its block fails, and on this device it did not"}
	}
	return true, skip{}
}

// top returns device's innermost frame, or nil.
func (h *handlers) top(device string) *handlerFrame {
	frames := h.frames[device]
	if len(frames) == 0 {
		return nil
	}
	return frames[len(frames)-1]
}

// failed takes n, a result that failed, into its handling (key), and
// reports whether a rescue: or always: is left to run for it. false means
// the failure ends the run, exactly as one outside any block does. A task
// whose target resolved to nothing, or that could not lock every device at
// once, failed for every device alike, and ends the run whatever encloses
// it (the always: of the blocks it is in still runs, executor.go).
func (h *handlers) failed(n NodeResult) bool {
	scopes := h.dag.Scopes[n.NodeID]
	if len(scopes) == 0 || !h.dag.Guarded(scopes) {
		return false
	}
	switch n.failureStage {
	case FailureStageResolveTarget, FailureStageLockAll:
		return false
	}
	_, held := HoldOf(n.Err)
	innermost := scopes[len(scopes)-1]
	key := h.key(n.NodeID, n.Device)
	top := h.top(key)
	switch {
	case innermost.Part == PartRescue && top != nil && top.block == innermost.Block:
		top.rescueFailed = true
		top.causes = append(top.causes, n)
	case innermost.Part == PartAlways && top != nil && top.block == innermost.Block:
		top.alwaysFailed = true
		top.causes = append(top.causes, n)
	default:
		h.frames[key] = append(h.frames[key], &handlerFrame{
			block: innermost.Block, part: innermost.Part, origin: n, held: held, alwaysFailed: innermost.Part == PartAlways,
		})
	}
	return h.handled(key)
}

// handled reports whether device's failures still have a rescue: or an
// always: ahead of the walk that would run for them. It unwinds a copy of
// the device's frames exactly as advance and propagate would for real, so a
// failure that no handler is left to meet ends the run at once, as one
// outside any block does, rather than when the walk reaches the block's end.
func (h *handlers) handled(device string) bool {
	type sim struct {
		block                            string
		part                             BlockPart
		held, rescueFailed, alwaysFailed bool
	}
	var stack []sim
	for _, f := range h.frames[device] {
		stack = append(stack, sim{f.block, f.part, f.held, f.rescueFailed, f.alwaysFailed})
	}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		g := h.dag.Guards[top.block]
		if top.part == PartBlock && g.HasRescue() && !top.held && !top.rescueFailed && !h.allDone(g.Rescue) {
			return true
		}
		if g.HasAlways() && !top.alwaysFailed && !h.allDone(g.Always) {
			return true
		}
		stack = stack[:len(stack)-1]
		scopes := h.dag.Scopes[top.block]
		if len(scopes) == 0 {
			return false
		}
		outer := scopes[len(scopes)-1]
		if n := len(stack); n > 0 && stack[n-1].block == outer.Block {
			switch outer.Part {
			case PartRescue:
				stack[n-1].rescueFailed = true
			case PartAlways:
				stack[n-1].alwaysFailed = true
			}
			continue
		}
		stack = append(stack, sim{block: outer.Block, part: outer.Part, held: top.held, alwaysFailed: outer.Part == PartAlways})
	}
	return false
}

// allDone reports whether the walk has passed every node in ids.
func (h *handlers) allDone(ids []string) bool {
	for _, id := range ids {
		if !h.done[id] {
			return false
		}
	}
	return true
}

// finished records that the walk has passed each node of a level.
func (h *handlers) finished(level []string) {
	for _, id := range level {
		h.done[id] = true
	}
}

// advance settles every device's frames against how far the walk has come:
// a block whose rescue has finished without failing is rescued, and a block
// the walk has passed entirely hands a failure it did not rescue to the
// block enclosing it. It reports a device whose failure is past every
// handler, which ends the run.
func (h *handlers) advance() (fatal []NodeResult) {
	devices := make([]string, 0, len(h.frames))
	for d := range h.frames {
		devices = append(devices, d)
	}
	slices.Sort(devices)
	for _, d := range devices {
		if lost := h.advanceDevice(d); lost != nil {
			fatal = append(fatal, *lost)
		}
	}
	return fatal
}

// advanceDevice is advance for one device, returning the failure that ends
// the run, if one does.
func (h *handlers) advanceDevice(device string) *NodeResult {
	for {
		top := h.top(device)
		if top == nil {
			return nil
		}
		g := h.dag.Guards[top.block]
		if top.part == PartBlock && g.HasRescue() && !top.held && !top.rescueFailed && h.allDone(g.Rescue) {
			// The rescue ran to its end without failing: the block's failure
			// is answered, every failure it carried with it too (an inner
			// rescue or always that failed on the way here), and its always:
			// runs as after a success.
			for _, n := range append([]NodeResult{top.origin}, top.causes...) {
				h.rescued[resultKey{n.NodeID, n.Device}] = g.Rescue[0]
			}
			h.pop(device)
			continue
		}
		if !h.allDone(g.Tasks) || !h.allDone(g.Rescue) || !h.allDone(g.Always) {
			return nil
		}
		// The walk has passed the whole block and the failure stands: it
		// goes on to the block enclosing this one, as a failure of the
		// block's own place there.
		h.pop(device)
		if !h.propagate(device, top) {
			lost := top.origin
			return &lost
		}
	}
}

// pop removes device's innermost frame.
func (h *handlers) pop(device string) {
	frames := h.frames[device]
	h.frames[device] = frames[:len(frames)-1]
	if len(h.frames[device]) == 0 {
		delete(h.frames, device)
	}
}

// propagate hands frame's unanswered failure to the block enclosing
// frame.block, reporting false when there is none to take it.
func (h *handlers) propagate(device string, frame *handlerFrame) bool {
	scopes := h.dag.Scopes[frame.block]
	if len(scopes) == 0 {
		return false
	}
	outer := scopes[len(scopes)-1]
	top := h.top(device)
	switch {
	case top != nil && top.block == outer.Block && outer.Part == PartRescue:
		top.rescueFailed = true
		top.causes = append(top.causes, append([]NodeResult{frame.origin}, frame.causes...)...)
	case top != nil && top.block == outer.Block && outer.Part == PartAlways:
		top.alwaysFailed = true
		top.causes = append(top.causes, append([]NodeResult{frame.origin}, frame.causes...)...)
	default:
		h.frames[device] = append(h.frames[device], &handlerFrame{
			block: outer.Block, part: outer.Part, origin: frame.origin, causes: frame.causes, held: frame.held,
			alwaysFailed: outer.Part == PartAlways,
		})
	}
	return h.handled(device)
}

// errorContext is the error root rescue task nodeID reads on device: the
// failure its block is handling there, or nil when there is none.
func (h *handlers) errorContext(nodeID, device string, dag *DAG, mask func(string) string) map[string]any {
	top := h.top(h.key(nodeID, device))
	if top == nil {
		return nil
	}
	o := top.origin
	task := dag.Nodes[o.NodeID]
	out := map[string]any{
		"task":      o.NodeID,
		"device":    o.Device,
		"message":   "",
		"unreached": Unreached(o.Err),
	}
	if task != nil {
		out["name"], out["method"] = task.Name, task.FQCN
	}
	if o.Err != nil {
		out["message"] = mask(o.Err.Error())
	}
	return out
}

// enclosing returns every block that encloses a node of level: where a run
// that has to end stands when it decides to.
func (h *handlers) enclosing(level []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range level {
		for _, s := range h.dag.Scopes[id] {
			out[s.Block] = true
		}
	}
	return out
}

// whileEnding says what a run that has to end does with level, given the
// blocks it was inside when it had to (enclosing): it still runs the always:
// of each of them, on every device, since a cleanup one device's failure
// would skip is what always: exists for; it passes over their other tasks
// without running them; and it stops once it has left them.
func (h *handlers) whileEnding(level []string, within map[string]bool) (run, pass bool) {
	for _, id := range level {
		for _, s := range h.dag.Scopes[id] {
			if !within[s.Block] {
				continue
			}
			if s.Part == PartAlways {
				return true, false
			}
			pass = true
		}
	}
	return false, pass
}
