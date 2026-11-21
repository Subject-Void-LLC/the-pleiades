// Package engine: a check's answer for a task whose condition reads a stored
// fact an earlier task of the same check would emit (owner decision
// 2026-11-21, Phase 41's read half).
//
// A check stores nothing and overlays no prediction (facts_overlay.go), so a
// checked task reading a key an earlier checked task would emit in a real run
// reads what the store held before, which is not what the real run would
// read. Such a task is reported unchecked, with its own reason, and the check
// is incomplete (exit 3): it neither runs its Check nor skips. The taint is
// on what the earlier task would emit (its method's fact declarations, by key
// or by family), never on whether the check predicted a change: a method
// that would change nothing still stores the fact on a device that has none.
//
// One exception buys most checks back: a built-in method whose manifest says
// it only reads (Reversibility.ReadOnly) and that can be checked checks by
// reading the device, so what it emits during the check is an observation,
// which a later checked task reads (facts_overlay.go). Where that task's check
// did not run on a device (it was unchecked there, or it is a rescue a check
// never runs), what it would observe is not known either, and its readers on
// that device are unchecked.
//
// A real task always reads the store and what real tasks emitted, whatever
// was checked before it, so only a checked reader is ever tainted.
package engine

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// FactTaint is one fact a checked task's condition reads that an earlier task
// of the run, itself run as a check, declares it emits.
type FactTaint struct {
	// Key is the key the reading task names.
	Key string
	// Emitter and EmitterLabel are the earlier task's node id and its label,
	// and Method its method.
	Emitter, EmitterLabel, Method string
	// Declared is the declaration Key matched, as a message shows it: the
	// key quoted, or a family's prefix quoted and followed by *.
	Declared string
	// Observes says the emitter checks by reading the device, so what it
	// emits during the check is an observation a later checked task reads;
	// such a taint holds only on a device where its check did not run.
	Observes bool
}

// CheckedFactReads returns, for each task of dag that a run in runMode checks
// (TaskMode) and whose condition reads the facts root, every earlier task the
// run also checks that declares a key it reads, by node id. It is the one
// plan-time statement of the rule: the executor decides with it which tasks
// it reports unchecked, and validation warns with it, so the two cannot
// disagree. Earlier means in an earlier level of the walk; a task's own level
// runs at once and is not read (facts_overlay.go).
func CheckedFactReads(runMode collection.Mode, dag *DAG) map[string][]FactTaint {
	reads := FactReadsOf(dag)
	if len(reads) == 0 {
		return nil
	}
	levels := map[string]int{}
	it := NewLevelIterator(dag)
	for n := 0; ; n++ {
		level, ok := it.Next()
		if !ok {
			break
		}
		for _, id := range level {
			levels[id] = n
		}
	}
	var emitters []string
	for id := range levels {
		emitters = append(emitters, id)
	}
	sort.Strings(emitters)
	out := map[string][]FactTaint{}
	for _, read := range reads {
		reader := dag.Nodes[read.Node]
		at, placed := levels[read.Node]
		if !placed || TaskMode(runMode, dag, reader) != collection.ModeCheck {
			continue
		}
		for _, id := range emitters {
			task := dag.Nodes[id]
			if levels[id] >= at || task == nil || TaskMode(runMode, dag, task) != collection.ModeCheck {
				continue
			}
			out[read.Node] = append(out[read.Node], emitterTaints(id, task, read.Keys)...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// emitterTaints is each of keys that task (node id) declares it emits.
func emitterTaints(id string, task *Task, keys []string) []FactTaint {
	d, registered := collection.Lookup(task.FQCN)
	if !registered {
		return nil
	}
	observes := observesInCheck(task.FQCN)
	if observes {
		observes, _ = Checkable(task.FQCN, task.Params)
	}
	var out []FactTaint
	for _, key := range keys {
		spec, ok := collection.FactFor(d.Manifest.Facts, key)
		if !ok {
			continue
		}
		declared := strconv.Quote(spec.Key)
		if spec.Prefix {
			declared += "*"
		}
		out = append(out, FactTaint{Key: key, Emitter: id, EmitterLabel: taskLabel(id, task), Method: task.FQCN, Declared: declared, Observes: observes})
	}
	return out
}

// observesInCheck reports whether method reads the device when checked, so
// what it emits then is an observation: a built-in (a program's methods are
// third-party code whose declaration nothing holds it to) whose manifest says
// it only reads.
func observesInCheck(method string) bool {
	d, ok := collection.Lookup(method)
	return ok && d.Provider == nil && d.Manifest.Reversibility.ReadOnly
}

// Reason is why t leaves its reader unchecked, for the check's ledger: the
// reading task's key, the emitting task, its method and its declaration,
// never a value.
func (t FactTaint) Reason() string {
	if t.Observes {
		return fmt.Sprintf("its condition reads facts[%s], which task %s observes through %s, and that task's check did not run on this device, so what this task would read is not known",
			strconv.Quote(t.Key), t.EmitterLabel, t.Method)
	}
	return fmt.Sprintf("its condition reads facts[%s], which task %s would emit through %s (it declares %s), and a check stores no fact, so what this task would read in a real run is not known",
		strconv.Quote(t.Key), t.EmitterLabel, t.Method, t.Declared)
}

// factTaint is the reason the check reports task nodeID unchecked on device,
// or "" when it may be checked: an earlier checked task would emit a key it
// reads, or one that would observe it did not run its check here.
func (r *run) factTaint(nodeID, device string) string {
	for _, t := range r.factTaints[nodeID] {
		if !t.Observes || r.knowledge.didNotRun(t.Emitter, device) {
			return t.Reason()
		}
	}
	return ""
}
