// Package validate is the shared validation core Part 0 Phase W3 builds.
// PLAN.md Section 5 specifies one validation package behind three
// surfaces: the CLI (built here), the IDE plugin, and backend plan-time
// checks (both adopt this package later rather than reimplement it).
package validate

import (
	"cmp"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// WorldView bundles the parsed inventory and the parsed DAG so a Rule can
// cross-reference them: which devices exist, what a runbook node targets,
// and what that target actually declares.
type WorldView struct {
	Items []inventory.InventoryItem
	DAG   *engine.DAG

	// Mode is the mode the runbook is about to run in. The zero value, and
	// anything but collection.ModeCheck, validates for a real run, which is
	// the strict reading: a caller that never set it gets exactly the rules
	// it always had. LifecycleRule is the one rule that reads it, since a
	// check may target a simulate-locked device and a real run may not.
	Mode collection.Mode

	// Resolver, when set, answers every target exactly as the executor
	// that will run this runbook answers it, instead of the name-then-tag
	// lookup over Items. It is the same engine.TargetResolver the executor
	// is handed, so validation and execution cannot disagree about which
	// devices a task reaches. The Runner sets it to its one-device
	// resolver, which resolves every target, and a task with no target at
	// all, to the device the dispatch names. Left nil (the CLI), a task
	// with no target is controller-side and reaches no device.
	Resolver engine.TargetResolver

	// resolveCache memoizes Resolve by target string. Unexported and
	// unset by every caller that builds a WorldView directly (Resolve is
	// nil-safe and falls back to computing directly), so this changes
	// performance only, never Resolve's documented behavior. Validate
	// initializes it once, on its own local copy, before dispatching to
	// any Rule; every Rule receives a copy of that same WorldView value,
	// and since a map field's underlying data is shared across value
	// copies, every Rule's calls share the one cache for that Validate
	// call. See the Pattern Entry Gate note this fixes: with two rules
	// each independently calling Resolve per task (CapabilityRule,
	// LifecycleRule), the O(n) linear scan Resolve already documents as
	// "O(n^2) at that scale" was being paid twice per task instead of
	// once, measured doubling BenchmarkValidateFullInventory from 239ms
	// to ~480ms before this fix.
	resolveCache map[string][]inventory.InventoryItem
}

// Resolve returns every item matching target: an exact device Name match
// takes precedence and returns that single item; otherwise every item
// carrying target as a Tag is returned. An empty result means target
// matches nothing in this inventory.
func (w WorldView) Resolve(target string) []inventory.InventoryItem {
	if w.resolveCache != nil {
		if cached, ok := w.resolveCache[target]; ok {
			return cached
		}
	}

	result := w.resolveUncached(target)

	if w.resolveCache != nil {
		w.resolveCache[target] = result
	}
	return result
}

func (w WorldView) resolveUncached(target string) []inventory.InventoryItem {
	if w.Resolver != nil {
		return w.Resolver.Resolve(target)
	}
	for _, item := range w.Items {
		if item.Name() == target {
			return []inventory.InventoryItem{item}
		}
	}

	var matches []inventory.InventoryItem
	for _, item := range w.Items {
		for _, tag := range item.Tags() {
			if string(tag) == target {
				matches = append(matches, item)
				break
			}
		}
	}
	return matches
}

// taskDevices returns the target task names (TaskTarget: its own, or
// the runbook's hosts:) and the devices it reaches. A task that names no
// target reaches the Resolver's answer for the empty target when a
// Resolver is set, and no device otherwise, which is how the executor
// treats it (engine's resolveDevices).
func (w WorldView) taskDevices(task *engine.Task) (string, []inventory.InventoryItem) {
	target := engine.TaskTarget(w.DAG, task)
	if target == "" && w.Resolver == nil {
		return "", nil
	}
	return target, w.Resolve(target)
}

// Rule inspects a WorldView and returns the Findings it detects. A Rule
// never mutates the WorldView and never has side effects: it is a pure
// function from world state to problems, so any surface can run the same
// rule the same way.
type Rule func(world WorldView) []Finding

// registry holds every rule that runs on Validate. Rules register
// themselves from their own init(), so adding a rule never means editing
// this file or a growing if-chain (Registry pattern, Section 25).
var registry []Rule

// Register adds a Rule to the set Validate runs. Call it from an init()
// in the file that defines the rule.
func Register(r Rule) {
	registry = append(registry, r)
}

// Validate runs every registered rule against world and collects their
// Findings into one Report. It initializes world's Resolve cache once,
// on its own local copy, before running any rule; see WorldView's
// resolveCache field for why every rule ends up sharing it.
//
// The findings are sorted by node, then rule, then message. Rules walk
// DAG.Nodes, a map, so without the sort the same runbook listed its
// findings in a different order on every run, which a person comparing
// two runs, or a test comparing output, reads as a change.
func Validate(world WorldView) Report {
	world.resolveCache = make(map[string][]inventory.InventoryItem)

	var findings []Finding
	for _, r := range registry {
		findings = append(findings, r(world)...)
	}
	slices.SortStableFunc(findings, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Node, b.Node), cmp.Compare(a.RuleName, b.RuleName), cmp.Compare(a.Message, b.Message))
	})
	return Report{Findings: findings}
}
