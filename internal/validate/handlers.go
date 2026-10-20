// HandlerWarnings: the warning that a block's rescue: and always: do not run.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// HandlerRule is the RuleName of the rescue and always warning.
const HandlerRule = "handlers"

// HandlerWarnings warns, in a warning that never fails validation, about
// every block's rescue: and always: in dag: the engine parses and checks
// them and never runs them yet, so a block whose task fails runs no rescue,
// and its always: tasks are skipped even when it succeeds while the run
// still ends complete. A lab domain join's always: that disabled a test
// account did not run when its block failed, and validate had said
// nothing.
//
// The warning names the block (by the node ID a run gives it), which list
// will not run, and how many tasks that is, counting a task nested anywhere
// under the list, an imported one included, once, under the outermost
// rescue: or always: it sits in.
func HandlerWarnings(dag *engine.DAG) []Finding {
	if dag == nil {
		return nil
	}
	type owner struct{ block, kind string }
	counts := map[owner]int{}
	for id, task := range dag.Nodes {
		// A group's own node never runs: only its tasks would, and they are
		// counted themselves.
		if task == nil || task.Kind() != engine.TaskKindLeaf {
			continue
		}
		if o, ok := outermostHandler(id); ok {
			counts[owner{o.block, o.kind}]++
		}
	}
	owners := make([]owner, 0, len(counts))
	for o := range counts {
		owners = append(owners, o)
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].block != owners[j].block {
			return owners[i].block < owners[j].block
		}
		return owners[i].kind > owners[j].kind // rescue before always
	})
	var out []Finding
	for _, o := range owners {
		what := "a failure in the block runs none of them, and the run just fails"
		if o.kind == "always" {
			what = "they do not run when the block fails, and do not run when it succeeds either, while the run still ends complete"
		}
		out = append(out, Finding{RuleName: HandlerRule, Node: o.block, Message: fmt.Sprintf(
			"%s's %s: holds %d task(s), and the engine does not run %s: yet: %s; keep cleanup and recovery inside the block, in order, until it does",
			o.block, o.kind, counts[o], o.kind, what)})
	}
	return out
}

// outermostHandler returns the block owning id's outermost rescue: or
// always: list and which list it is, or false for a task in neither.
func outermostHandler(id string) (struct{ block, kind string }, bool) {
	at, kind := -1, ""
	for _, k := range []string{"rescue", "always"} {
		if i := strings.Index(id, "."+k+"["); i >= 0 && (at < 0 || i < at) {
			at, kind = i, k
		}
	}
	if at < 0 {
		return struct{ block, kind string }{}, false
	}
	return struct{ block, kind string }{id[:at], kind}, true
}
