// Package engine: whether a whole runbook only reads, decided before it runs.
package engine

// OnlyReads reports whether every task dag can run is a call that only reads,
// decided from the runbook alone, before anything runs: each one a call of a
// first-party built-in method declaring that it only reads for the
// parameters written (readsOnlyCall, the answer the journal records), with
// no parameter left to a template the run would render, no loop, and no
// handler. An engine action, an external Collection's method, a template or
// a loop all make it false, since what such a task does is known only once it
// runs.
//
// cmd/pleiades lets such an adhoc call run without the project's run lock, so
// a poller of reads never stands in a rollback's way. The engine still holds
// each call to its declaration as it runs (holdReadOnly), and journals it as
// one that only read (JournalEntry.ReadOnlyCall).
func OnlyReads(dag *DAG) bool {
	if dag == nil || len(dag.Nodes) == 0 || len(dag.Handlers) > 0 {
		return false
	}
	for _, task := range dag.Nodes {
		switch task.Kind() {
		case TaskKindBlock, TaskKindParallel, TaskKindSynthetic:
			// Structure: what runs is the tasks inside, each a node of its own.
			continue
		case TaskKindLeaf:
		default:
			return false
		}
		if task.Loop != nil || mayRender(task.Params) || !readsOnlyCall(task) {
			return false
		}
	}
	return true
}

// mayRender reports whether v, at any depth, holds a string the run would
// render as a template (IsTemplate), or a value of a kind this cannot look
// inside, which is counted as one: the call's real parameters would then be
// known only once it runs.
func mayRender(v any) bool {
	switch x := v.(type) {
	case nil, bool, int, int64, uint64, float64:
		return false
	case string:
		return IsTemplate(x)
	case map[string]any:
		for _, e := range x {
			if mayRender(e) {
				return true
			}
		}
		return false
	case []any:
		for _, e := range x {
			if mayRender(e) {
				return true
			}
		}
		return false
	case []string:
		for _, e := range x {
			if IsTemplate(e) {
				return true
			}
		}
		return false
	}
	return true
}
