// Package playbook: the bounds on how much work one playbook can ask for.
//
// A playbook is input from someone else, so every quantity that grows the
// work (bytes, nesting, alias expansion, loop items, emitted tasks,
// variable chains) has a ceiling here, far above any real playbook, and
// crossing one ends the conversion with a clear error rather than a slow
// or unbounded one.
package playbook

const (
	// maxPlaybookBytes bounds a playbook file; maxVarsFileBytes a vars or
	// imported task file.
	maxPlaybookBytes = 4 << 20
	maxVarsFileBytes = 1 << 20

	// maxExpandedNodes bounds the YAML nodes a document holds once every
	// alias is expanded, which is what stops an alias bomb.
	maxExpandedNodes = 200_000

	// maxYAMLDepth bounds how deeply a document nests, aliases included.
	maxYAMLDepth = 200

	// maxTaskDepth bounds block and import nesting, matching the engine's
	// own ceiling (engine.maxTaskNestingDepth) so nothing this emits is
	// refused for depth by the builder.
	maxTaskDepth = 32

	// maxLoopItems bounds one loop's unrolled length, and maxEmittedTasks
	// the whole conversion's.
	maxLoopItems    = 256
	maxEmittedTasks = 10_000

	// maxVarDepth bounds how many variables one template may chain
	// through (a: "{{ b }}", b: "{{ c }}") before resolution gives up.
	maxVarDepth = 16

	// maxWhenBytes bounds one condition's source.
	maxWhenBytes = 4 << 10
)
