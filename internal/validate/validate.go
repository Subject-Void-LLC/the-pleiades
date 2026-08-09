// Package validate is the shared validation core Part 0 Phase W3 builds.
// PLAN.md Section 5 specifies one validation package behind three
// surfaces: the CLI (built here), the IDE plugin, and backend plan-time
// checks (both adopt this package later rather than reimplement it).
package validate

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// WorldView bundles the parsed inventory and the parsed DAG so a Rule can
// cross-reference them: which devices exist, what a runbook node targets,
// and what that target actually declares.
type WorldView struct {
	Items []inventory.InventoryItem
	DAG   *engine.DAG

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
func Validate(world WorldView) Report {
	world.resolveCache = make(map[string][]inventory.InventoryItem)

	var findings []Finding
	for _, r := range registry {
		findings = append(findings, r(world)...)
	}
	return Report{Findings: findings}
}
