package validate

import (
	"fmt"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// CollectionRule flags a task whose fqcn names a pkg/collection method
// that either does not exist at all, or is registered but still
// declared, never implemented (Phase 34's Release Gate: "pleiades
// validate fails a runbook calling an unimplemented name with a
// plain-language message").
//
// Every legacy built-in fqcn (noop, ssh_exec, ios_backup) and every
// engine keyword (set_fact, debug, import_tasks) is, and by
// docs/hephaestus.md's own design will remain, a bare, undotted word:
// pkg/collection.Register itself refuses to register any name without a
// dot (PLAN.md Section 2's <namespace>.<method> requirement), so no
// undotted name has ever been, or will be, registered in pkg/collection.
// Skipping any fqcn with no dot is therefore a closed-by-construction
// discriminator between a Collection name and an engine keyword or
// legacy built-in, not a hand-maintained allowlist that a new keyword
// could fall out of sync with.
//
// "set_metadata" is the one deliberate, named exception to that
// closed-by-construction claim: it is also reachable as the dotted
// "pleiades.builtin.set_metadata" (engine/action.go's switch dispatches
// both spellings identically), so it needs an explicit entry in
// dottedBuiltinExemptions below rather than falling out of
// isCollectionName's dot check for free. It stays a hardcoded builtin
// rather than a real pkg/collection registration on purpose: no
// dispatcher in this codebase invokes a registered pkg/collection method
// today (every Descriptor's Go implementation is unreachable until a
// later phase builds that dispatcher), and set_metadata already has real,
// working, tested logic in action.go that routing it through an unbuilt
// dispatcher would put at risk for no benefit. "pleiades.builtin.wait.port"
// is not exempted here: unlike set_metadata it has zero working
// implementation to protect and is already a real, dotted,
// pkg/collection-registered name (internal/forge/catalogdata/
// collections_gating.go), so it correctly flows through the normal
// collection.Lookup path below like any other declared-but-unimplemented
// method.
//
// This rule is independent of CapabilityRule, which keys off the
// separate engine.ActionCapability map and does not know about
// pkg/collection names at all: whether a catalog task's target device has
// the right capability is a different question from whether the name it
// calls exists and is implemented, and this phase only answers the
// second one.

// dottedBuiltinExemptions names every dotted fqcn that is a hardcoded
// engine builtin (internal/engine/action.go), not a real pkg/collection
// registration, and must therefore be skipped by CollectionRule exactly
// like an undotted legacy builtin, despite containing a dot.
var dottedBuiltinExemptions = map[string]bool{
	"pleiades.builtin.set_metadata": true,
}

func CollectionRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		if !isCollectionName(task.FQCN) || dottedBuiltinExemptions[task.FQCN] {
			continue
		}

		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}

		desc, ok := collection.Lookup(task.FQCN)
		switch {
		case !ok:
			findings = append(findings, Finding{
				RuleName: "collection",
				Node:     id,
				Message:  fmt.Sprintf("task %s calls %q, which is not a registered collection name", label, task.FQCN),
			})
		case desc.Manifest.Status == collection.StatusDeclared:
			findings = append(findings, Finding{
				RuleName: "collection",
				Node:     id,
				Message:  fmt.Sprintf("task %s calls %q, which is declared but not yet implemented", label, task.FQCN),
			})
		}
	}

	return findings
}

// isCollectionName reports whether fqcn is namespaced the way every real
// pkg/collection registration must be (PLAN.md Section 2), the same
// first-dot split pkg/collection.Register itself performs.
func isCollectionName(fqcn string) bool {
	return strings.Contains(fqcn, ".")
}

func init() {
	Register(CollectionRule)
}
