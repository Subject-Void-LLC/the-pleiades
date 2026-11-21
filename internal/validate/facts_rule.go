// Package validate: FactsRule, which holds a runbook's reads of stored facts
// (internal/engine's facts root) to the store the run will have, before
// anything runs (Phase 41's read half).
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// FactsRuleName is FactsRule's RuleName.
const FactsRuleName = "facts"

// FactStore is what a run can read of its devices' stored facts.
type FactStore int

const (
	// FactStoreNone is a run that cannot read stored facts at all: a
	// Walk-tier Runner, which is handed a runbook and no store (a fetch at
	// claim is not built yet), and every caller that does not say. It is
	// the zero value, so a caller that never set it refuses a runbook
	// reading facts rather than running one that cannot be answered.
	FactStoreNone FactStore = iota
	// FactStoreReadable is the command line in a project with its master
	// key, which every stored fact is sealed under.
	FactStoreReadable
	// FactStoreNoKey is the command line in a project with no master key,
	// which has no stored facts it can read: none was ever stored, or what
	// was is sealed under a key that is gone.
	FactStoreNoKey
)

// ErrNoFactsOnRunner is how a Runner, and the Controller before it, refuse a
// runbook that reads stored facts, after naming the task.
const ErrNoFactsOnRunner = "and a Runner cannot read stored facts in this release, so nothing ran on any device; " +
	"pleiades run reads them on the command line, and on this tier a run can gather in an earlier task and read that task's registered result (register:, then result.<name>[\"<key>\"])"

// FactsRule refuses a task whose condition reads stored facts that the run
// cannot answer, before any task runs:
//
//   - on a tier with no store (FactStoreNone), every such task: a Runner
//     would otherwise reach the task after earlier ones had changed the
//     device;
//   - in a project with no master key (FactStoreNoKey), each key no task in
//     an earlier level declares it emits: nothing stored can be read, so
//     only what this run gathers first can answer, and a read nothing
//     gathers would fail at its task. A key an earlier task declares is
//     allowed, and fails at its task if that task does not emit it;
//   - a task with no device, which has no facts;
//   - lock_acquisition: all_at_plan_time, since a task reading its device's
//     facts is decided on each device on its own.
//
// Where a fact may be read at all (a condition, by a literal key) is the
// builder's to refuse, so a runbook reaching here reads facts only as the
// root allows; a parameter reading facts is TemplateRule's.
func FactsRule(world WorldView) []Finding {
	reads := engine.FactReadsOf(world.DAG)
	var findings []Finding
	add := func(node, message string) {
		findings = append(findings, Finding{RuleName: FactsRuleName, Node: node, Message: message})
	}
	for _, read := range reads {
		task := world.DAG.Nodes[read.Node]
		keys := engine.QuotedFactKeys(read.Keys)
		switch world.StoredFacts {
		case FactStoreNone:
			// Authorization, for when a Runner can read stored facts: a run
			// reading facts then needs inventory:read on each device it reads
			// (owner decision 2026-11-21), checked where the fetch is served,
			// since a condition is otherwise a yes or no answer about a fact
			// to an author holding only runbook:execute.
			add(read.Node, fmt.Sprintf("task %s reads stored facts (%s), %s", read.Label, keys, ErrNoFactsOnRunner))
			continue
		case FactStoreNoKey:
			for _, key := range read.Keys {
				if !engine.DeclaredBefore(world.DAG, read.Node, key) {
					add(read.Node, fmt.Sprintf("task %s reads %s, and this project has no master key, so it has no stored facts to read: "+
						"set PLEIADES_MASTER_KEY or restore .pleiades/master.key, or gather the fact in an earlier task of this run (pleiades doc <method> lists the facts a method emits)",
						read.Label, engine.QuotedFactKeys([]string{key})))
				}
			}
		}
		if target, _ := world.taskDevices(task); target == "" && world.Resolver == nil {
			add(read.Node, fmt.Sprintf("task %s reads %s, and it has no device, so it has no facts: give it hosts: or a target", read.Label, keys))
		}
		if task.LockAcquisition == engine.AcquisitionAllAtPlanTime {
			add(read.Node, fmt.Sprintf("task %s reads %s: a task reading its device's facts is decided on each device on its own, so it cannot take lock_acquisition: all_at_plan_time, which takes every device's lock together", read.Label, keys))
		}
	}
	return findings
}

// FactCheckWarnings warns, for a run in world.Mode, of each checked task its
// check will report unchecked because an earlier checked task would emit a
// fact its condition reads (engine.CheckedFactReads, the rule the executor
// applies): a check stores no fact, so the check of such a runbook is never
// complete. A read-only gather an earlier task checks by reading is an
// observation and is not warned of.
func FactCheckWarnings(world WorldView) []Finding {
	plan := engine.CheckedFactReads(world.Mode, world.DAG)
	nodes := make([]string, 0, len(plan))
	for node := range plan {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	var out []Finding
	for _, node := range nodes {
		for _, t := range plan[node] {
			if t.Observes {
				continue
			}
			out = append(out, Finding{RuleName: FactsRuleName, Node: node,
				Message: fmt.Sprintf("task %s will be reported unchecked in a check: %s", taskName(node, world.DAG.Nodes[node]), t.Reason())})
			break
		}
	}
	return out
}

func init() {
	Register(FactsRule)
}
