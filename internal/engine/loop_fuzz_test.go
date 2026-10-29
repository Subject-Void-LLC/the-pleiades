// A fuzz test of authored variables, loops and retries: random combinations of
// vars, set_fact, loop, until, changed_when, failed_when and ignore_errors are
// built through the real Builder and run through the real Executor with a real
// journal file. Whatever they do, a run must not panic, must call a method no
// more often than the bounds allow, and must write journal entries that are
// unique per (node, device) and that the journal's strict reader accepts.
package engine_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// loopFuzzActions runs the in-process actions as the engine does and answers
// every other call with a result whose n counts the calls so far, failing some
// of them by a fixed rule so retries and ignore_errors have work to do.
type loopFuzzActions struct {
	builtin engine.ActionExecutor
	mu      sync.Mutex
	calls   int
	failMod int
}

// Execute implements engine.ActionExecutor.
func (a *loopFuzzActions) Execute(ctx context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	if task.FQCN == engine.SetFactName || task.FQCN == "noop" || task.FQCN == engine.FailName {
		return a.builtin.Execute(ctx, task, d)
	}
	a.mu.Lock()
	a.calls++
	n := a.calls
	a.mu.Unlock()
	if a.failMod > 0 && n%a.failMod == 0 {
		return engine.ActionResult{}, errors.New("scripted failure")
	}
	return engine.ActionResult{Stats: map[string]any{"n": n, "said": fmt.Sprint(task.Params["note"])}}, nil
}

// genLoopItem writes one random loop item as a YAML flow value, including
// shapes a template cannot read an item field from.
func genLoopItem(r *rand.Rand, depth int) string {
	switch r.Intn(7) {
	case 0:
		return fmt.Sprint(r.Intn(100))
	case 1:
		return `"s` + fmt.Sprint(r.Intn(9)) + `"`
	case 2:
		return "null"
	case 3:
		return `"{{ item }}"`
	case 4:
		if depth > 0 {
			return "[" + genLoopItem(r, depth-1) + ", " + genLoopItem(r, depth-1) + "]"
		}
		return "[]"
	default:
		if depth > 0 {
			return "{name: " + genLoopItem(r, depth-1) + ", id: " + fmt.Sprint(r.Intn(5)) + "}"
		}
		return "{name: leaf}"
	}
}

// genLoopRunbook writes a random runbook from seed and returns it with the
// most method calls it may make on one device.
func genLoopRunbook(r *rand.Rand) (yaml string, bound int) {
	var b strings.Builder
	b.WriteString("id: loopfuzz\nhosts: web\n")
	b.WriteString("vars:\n  base: \"x\"\n  chain: \"{{ vars.base }}-1\"\n")
	if r.Intn(4) == 0 {
		b.WriteString("  selfish: \"{{ vars.selfish }}\"\n")
	}
	if r.Intn(5) == 0 {
		b.WriteString("  ping: \"{{ vars.pong }}\"\n  pong: \"{{ vars.ping }}\"\n")
	}
	b.WriteString("tasks:\n")
	tasks := 1 + r.Intn(5)
	for i := range tasks {
		switch r.Intn(5) {
		case 0:
			fmt.Fprintf(&b, "  - set_fact:\n      f%d: \"{{ vars.chain }}-%d\"\n", r.Intn(3), i)
			if r.Intn(2) == 0 {
				b.WriteString("      global: true\n")
			}
			if r.Intn(2) == 0 {
				b.WriteString("      no_override: true\n")
			}
			continue
		case 1:
			fmt.Fprintf(&b, "  - fail:\n      msg: \"stop %d\"\n    ignore_errors: %v\n", i, r.Intn(2) == 0)
			continue
		}
		fmt.Fprintf(&b, "  - rbtest.step:\n      note: \"{{ vars.chain }}-{{ item.name }}-{{ item }}\"\n    register: r%d\n", i)
		calls := 1
		if r.Intn(3) != 0 {
			var n int
			switch r.Intn(6) {
			case 0:
				n = 0
			case 1:
				n = engine.MaxLoopItems + 1
			case 2:
				n = 40
			default:
				n = 1 + r.Intn(4)
			}
			items := make([]string, n)
			for k := range items {
				items[k] = genLoopItem(r, 2)
			}
			fmt.Fprintf(&b, "    loop: [%s]\n", strings.Join(items, ", "))
			calls = min(n, engine.MaxLoopItems)
			if r.Intn(3) == 0 {
				b.WriteString("    loop_control: {loop_var: it, index_var: ix}\n")
			}
		} else if r.Intn(2) == 0 {
			b.WriteString("    loop: \"{{ vars.chain }}\"\n")
			calls = engine.MaxLoopItems
		}
		if r.Intn(2) == 0 {
			retries := r.Intn(5)
			fmt.Fprintf(&b, "    until: [\"result.r%d.n >= %d\"]\n    retries: %d\n    delay: 0\n", i, r.Intn(30), retries)
			calls *= retries + 1
		}
		if r.Intn(3) == 0 {
			fmt.Fprintf(&b, "    changed_when: [\"result.r%d.n > 2\"]\n", i)
		}
		if r.Intn(3) == 0 {
			fmt.Fprintf(&b, "    failed_when: [\"result.r%d.n %% 7 == 0\"]\n", i)
		}
		if r.Intn(3) == 0 {
			b.WriteString("    ignore_errors: true\n")
		}
		bound += calls
	}
	return b.String(), bound
}

// checkLoopRunbook builds and runs the runbook seed makes, then checks the
// run and its journal read back.
func checkLoopRunbook(t *testing.T, seed int64) (ran bool) {
	t.Helper()
	r := rand.New(rand.NewSource(seed)) // #nosec G404 -- a reproducible test input, not a secret
	yaml, bound := genLoopRunbook(r)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(yaml))
	if err != nil {
		return false // a refusal at build time is a valid answer to a hostile runbook
	}
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	actions := &loopFuzzActions{builtin: engine.NewBuiltinActionExecutor(), failMod: r.Intn(6)}
	x := engine.NewExecutor(twoDevices(), actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()), engine.WithJournal(store))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := x.Run(ctx, dag)
	if err != nil {
		return false // a run refused as a whole is a valid answer too
	}
	if actions.calls > bound*2 {
		t.Fatalf("seed %d: %d method calls, bound %d\n%s", seed, actions.calls, bound*2, yaml)
	}
	for _, n := range result.Nodes {
		if strings.Count(n.Iteration, "[") > 2 {
			t.Fatalf("seed %d: node %s has iteration %q\n%s", seed, n.NodeID, n.Iteration, yaml)
		}
	}
	runs, err := journal.ListRuns(root)
	if err != nil {
		t.Fatalf("seed %d: the strict reader refused the journal: %v\n%s", seed, err, yaml)
	}
	total := 0
	for _, run := range runs {
		total += len(run.Entries)
		seen := map[string]bool{}
		for _, e := range run.Entries {
			key := e.NodeID + "@" + e.DeviceID
			if seen[key] {
				t.Fatalf("seed %d: journal node %s on %s written twice\n%s", seed, e.NodeID, e.DeviceID, yaml)
			}
			seen[key] = true
		}
	}
	if actions.calls > 0 && total == 0 {
		t.Fatalf("seed %d: %d methods ran and the journal is empty\n%s", seed, actions.calls, yaml)
	}
	return true
}

// TestLoopFuzz_SeedsHold runs a few hundred generated runbooks.
func TestLoopFuzz_SeedsHold(t *testing.T) {
	ran := 0
	for seed := range int64(300) {
		if checkLoopRunbook(t, seed) {
			ran++
		}
	}
	if ran < 100 {
		t.Fatalf("only %d of 300 generated runbooks ran, so the generator mostly tests the builder's refusals", ran)
	}
}

// FuzzLoopsAndVars explores further: go test -run x -fuzz FuzzLoopsAndVars.
func FuzzLoopsAndVars(f *testing.F) {
	for _, seed := range []int64{0, 1, 7, 42, 99, 1999} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed int64) { checkLoopRunbook(t, seed) })
}
