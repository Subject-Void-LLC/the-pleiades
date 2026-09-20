// Package engine_test: a fuzz of checks over runbooks mixing checkable,
// uncheckable and failing tasks in every shape.
package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// mixedFixtures are FuzzCheckMixedDAG's three methods: one that can be
// checked (its prediction alternates between changed and not, so both
// outcomes appear), one that cannot, and one whose check fails. Every
// Invoke counts, since a check must never reach one.
type mixedFixtures struct {
	names   [3]string
	invokes atomic.Int32
}

// registerMixedFixtures registers the three once per fuzz run.
func registerMixedFixtures(f *testing.F) *mixedFixtures {
	f.Cleanup(collection.SnapshotForTest())
	m := &mixedFixtures{names: [3]string{"mixedfuzz.checkable", "mixedfuzz.uncheckable", "mixedfuzz.failing"}}
	registerMixedFixturesInto(f, m)
	return m
}

// registerMixedFixturesInto registers m's three methods under m's names.
func registerMixedFixturesInto(tb testing.TB, m *mixedFixtures) {
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		m.invokes.Add(1)
		return collection.Result{Changed: true}, nil
	}
	var predictions atomic.Int32
	for i, check := range []collection.Method{
		func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			changed := predictions.Add(1)%2 == 0
			return collection.Result{Changed: changed}, rc.SetStat("seen", "state")
		},
		nil,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, fmt.Errorf("the device refused the read")
		},
	} {
		d := collection.Descriptor{
			Name: m.names[i],
			Manifest: collection.Manifest{
				Status:        collection.StatusImplemented,
				Reversibility: collection.Reversibility{Notes: "a fuzz fixture"},
				SupportsCheck: check != nil,
			},
			Invoke: invoke,
			Check:  check,
		}
		if err := collection.Register(d); err != nil {
			tb.Fatalf("registering %s: %v", d.Name, err)
		}
	}
}

// mixedRunbook decodes fuzz bytes into a runbook from the builder's
// grammar: leaf tasks over the three fixtures, blocks with rescue and
// always, parallel groups, registers, and when, when_or and when_cel
// conditions reading earlier registers, a variable, or a register no task
// writes. It is bounded in size and depth, so a run stays small.
type mixedRunbook struct {
	data      []byte
	pos       int
	tasks     int
	registers []string
}

// next returns the next fuzz byte, or zero once they run out.
func (g *mixedRunbook) next() byte {
	if g.pos >= len(g.data) {
		return 0
	}
	b := g.data[g.pos]
	g.pos++
	return b
}

// list builds up to max sibling tasks at depth.
func (g *mixedRunbook) list(depth, max int, names [3]string) []map[string]any {
	n := 1 + int(g.next())%max
	out := make([]map[string]any, 0, n)
	for i := 0; i < n && g.tasks < 20; i++ {
		out = append(out, g.task(depth, names))
	}
	return out
}

// task builds one task: a leaf, a block or a parallel group.
func (g *mixedRunbook) task(depth int, names [3]string) map[string]any {
	g.tasks++
	name := fmt.Sprintf("t%d", g.tasks)
	switch kind := g.next() % 5; {
	case kind == 3 && depth < 3:
		t := map[string]any{"name": name, "block": g.list(depth+1, 3, names)}
		if g.next()%2 == 0 {
			t["rescue"] = g.list(depth+1, 2, names)
		}
		if g.next()%2 == 0 {
			t["always"] = g.list(depth+1, 2, names)
		}
		return t
	case kind == 4 && depth < 3:
		// A parallel group's children are leaves here, and read no
		// register a sibling writes, so no child waits on another.
		children := make([]map[string]any, 0, 3)
		for i := 0; i < 1+int(g.next())%3 && g.tasks < 20; i++ {
			g.tasks++
			children = append(children, map[string]any{"name": fmt.Sprintf("t%d", g.tasks), "fqcn": names[g.next()%3]})
		}
		return map[string]any{"name": name, "parallel": children}
	default:
		t := map[string]any{"name": name, "fqcn": names[g.next()%3]}
		if key, value, ok := g.condition(); ok {
			t[key] = value
		}
		if g.next()%2 == 0 {
			reg := fmt.Sprintf("r%d", g.tasks)
			t["register"] = reg
			g.registers = append(g.registers, reg)
		}
		return t
	}
}

// condition returns a condition's key and value, or ok false for none.
func (g *mixedRunbook) condition() (key string, value any, ok bool) {
	ref := "stat.nowhere[\"\"].changed"
	if len(g.registers) > 0 && g.next()%4 != 0 {
		ref = fmt.Sprintf("stat.%s[\"\"].changed", g.registers[int(g.next())%len(g.registers)])
	}
	switch g.next() % 6 {
	case 0:
		return "when", ref, true
	case 1:
		return "when_or", []string{ref, "vars.force"}, true
	case 2:
		return "when", []string{"vars.never", ref}, true
	case 3:
		return "when_cel", "has(" + ref[:len(ref)-len(".changed")] + ")", true
	default:
		return "", nil, false
	}
}

// FuzzCheckMixedDAG is Phase 46's Fuzz/Stress item: runbooks mixing
// checkable, uncheckable and failing tasks across every shape the builder
// accepts, run as a check. Whatever the shape:
//
//   - no Invoke is ever called;
//   - the walk ends, within a deadline, rather than deadlocking;
//   - every node is exactly one of ok, would change, unchecked, failed or
//     skipped: an unchecked node is skipped and neither failed nor
//     changed, which is what lets the CLI count it (run.go tests Unchecked
//     first) exactly once, and a skipped or failed node never claims a
//     change;
//   - an uncheckable task is never reported as a success.
func FuzzCheckMixedDAG(f *testing.F) {
	fixtures := registerMixedFixtures(f)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{
		{},
		{2, 0, 0, 0, 1, 0, 0, 1},
		{4, 3, 1, 0, 0, 0, 2, 0, 1, 0, 0, 0},
		{3, 4, 2, 1, 2, 0, 0, 1, 3, 0, 1, 0, 2, 0, 0},
		{5, 0, 0, 0, 0, 0, 1, 1, 1, 0, 2, 2, 3, 1, 4, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		g := &mixedRunbook{data: data}
		body, err := json.Marshal(map[string]any{"id": "mixed", "tasks": g.list(0, 6, fixtures.names)})
		if err != nil {
			t.Fatal(err)
		}
		dag, err := engine.NewBuilder(eval).Build(body)
		if err != nil {
			return // a shape the builder refuses is not this fuzz's subject
		}

		before := fixtures.invokes.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := newCheckExecutor(mapResolver{},
			engine.WithMode(collection.ModeCheck),
			engine.WithVariables(map[string]interface{}{"force": true, "never": false}),
		).Run(ctx, dag)
		if ctx.Err() != nil {
			t.Fatalf("the check did not finish within its deadline: %s", body)
		}
		if err != nil {
			t.Fatalf("Run: %v\n%s", err, body)
		}
		if n := fixtures.invokes.Load() - before; n != 0 {
			t.Fatalf("a check called Invoke %d time(s):\n%s", n, body)
		}
		for _, node := range result.Nodes {
			task := dag.Nodes[node.NodeID]
			switch {
			case node.Unchecked && (!node.Skipped || node.Err != nil || node.Changed):
				t.Fatalf("an unchecked node is not simply skipped: %+v\n%s", node, body)
			case node.Skipped && node.Changed:
				t.Fatalf("a skipped node claims a change: %+v\n%s", node, body)
			case node.Err != nil && (node.Skipped || node.Changed):
				t.Fatalf("a failed node is also skipped or changed: %+v\n%s", node, body)
			case task != nil && task.FQCN == fixtures.names[1] && !node.Skipped && node.Err == nil:
				t.Fatalf("an uncheckable task was reported as a success: %+v\n%s", node, body)
			}
		}
	})
}

// TestCheckMixedDAG_TheGeneratorReachesEveryOutcome keeps the fuzz honest:
// over a fixed spread of inputs, most generated runbooks build, and between
// them every outcome the fuzz classifies appears, so the invariants above
// are exercised rather than vacuously true over runbooks the builder
// refused.
func TestCheckMixedDAG_TheGeneratorReachesEveryOutcome(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	fixtures := &mixedFixtures{names: [3]string{"mixedprobe.checkable", "mixedprobe.uncheckable", "mixedprobe.failing"}}
	registerMixedFixturesInto(t, fixtures)
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	built, seen := 0, map[string]int{}
	rng := rand.New(rand.NewPCG(46, 46))
	const inputs = 400
	for range inputs {
		data := make([]byte, 8+rng.IntN(40))
		for i := range data {
			data[i] = byte(rng.IntN(256))
		}
		body, _ := json.Marshal(map[string]any{"id": "mixed", "tasks": (&mixedRunbook{data: data}).list(0, 6, fixtures.names)})
		dag, err := engine.NewBuilder(eval).Build(body)
		if err != nil {
			continue
		}
		built++
		result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck),
			engine.WithVariables(map[string]interface{}{"force": true, "never": false})).Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		for _, n := range result.Nodes {
			switch {
			case dag.Nodes[n.NodeID] != nil && dag.Nodes[n.NodeID].Kind() == engine.TaskKindSynthetic:
			case n.Unchecked:
				seen["unchecked"]++
			case n.Err != nil:
				seen["failed"]++
			case n.Skipped:
				seen["skipped"]++
			case n.Changed:
				seen["would change"]++
			default:
				seen["ok"]++
			}
		}
	}
	if built < inputs/2 {
		t.Errorf("only %d of %d generated runbooks built", built, inputs)
	}
	for _, outcome := range []string{"ok", "would change", "unchecked", "failed", "skipped"} {
		if seen[outcome] == 0 {
			t.Errorf("no generated runbook produced a %q node (seen: %v)", outcome, seen)
		}
	}
	t.Logf("%d of %d built; outcomes %v", built, inputs, seen)
}
