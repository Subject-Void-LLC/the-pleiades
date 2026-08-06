package engine

// TaskKind classifies a Task by which of its mutually exclusive shape
// fields is set. It is computed from field presence via Task.Kind, never
// authored or stored as its own field: unlike AcquisitionStrategy
// (genuinely author-set YAML/JSON data), a stored Kind would be a second
// source of truth that can drift from the fields it is supposed to
// describe.
type TaskKind int

const (
	// TaskKindLeaf is a module-call task: FQCN is set.
	TaskKindLeaf TaskKind = iota

	// TaskKindBlock is a block task: Block is set.
	TaskKindBlock

	// TaskKindParallel is a parallel task: Parallel is set.
	TaskKindParallel

	// TaskKindSynthetic is a structural fan-out/join marker node the
	// Builder itself constructs for a Parallel task (synthesizeParallel,
	// tasktree.go), never one any runbook author writes. See
	// registerSyntheticNode.
	TaskKindSynthetic

	// TaskKindInvalid marks a Task satisfying none, or more than one, of
	// FQCN/Block/Parallel. validateTask (tasktree.go) rejects this before
	// anything downstream calls Kind on it in practice, but Kind stays
	// total (never panics) so a caller reached before validation still
	// gets an explicit, checkable value instead of an ambiguous guess.
	TaskKindInvalid
)

// String returns TaskKind's name, used in error messages and logs.
func (k TaskKind) String() string {
	switch k {
	case TaskKindLeaf:
		return "leaf"
	case TaskKindBlock:
		return "block"
	case TaskKindParallel:
		return "parallel"
	case TaskKindSynthetic:
		return "synthetic"
	default:
		return "invalid"
	}
}

// taskShape reports which of t's three mutually exclusive shape fields are
// set. It is the one shared derivation Kind (below) and validateTask
// (tasktree.go) both build on: Kind collapses it into a single classifying
// enum for a caller that already knows t is valid (import_tasks.go's
// resolveImportTasksInList, cmd/pleiades/run.go's printTaskList),
// validateTask keeps the raw booleans to report precisely which fields
// conflict, a distinction Kind's single TaskKindInvalid bucket cannot
// express.
func taskShape(t *Task) (hasFQCN, hasBlock, hasParallel bool) {
	return t.FQCN != "", len(t.Block) > 0, len(t.Parallel) > 0
}

// Kind classifies t by which shape field is set: TaskKindSynthetic if t is
// a Builder-constructed marker node, otherwise whichever of
// TaskKindLeaf/TaskKindBlock/TaskKindParallel matches, or TaskKindInvalid
// if none or more than one field is set.
func (t Task) Kind() TaskKind {
	if t.synthetic {
		return TaskKindSynthetic
	}

	hasFQCN, hasBlock, hasParallel := taskShape(&t)
	switch {
	case hasFQCN && !hasBlock && !hasParallel:
		return TaskKindLeaf
	case hasBlock && !hasFQCN && !hasParallel:
		return TaskKindBlock
	case hasParallel && !hasFQCN && !hasBlock:
		return TaskKindParallel
	default:
		return TaskKindInvalid
	}
}
