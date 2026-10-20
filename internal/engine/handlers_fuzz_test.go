// A fuzz test of rescue: and always:: random nestings of blocks, rescues and
// always lists, with random failures, run through the real Executor and
// compared with an independent interpreter of the same semantics, written
// recursively the way the design reads, with no frames and no levels.
package engine_test

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// genTask is one task of a generated runbook: a leaf that may fail, or a
// block with optional rescue and always lists.
type genTask struct {
	name                  string
	fail                  bool
	block, rescue, always []*genTask
}

// genTree builds a random task list from r, at most depth blocks deep.
func genTree(r *rand.Rand, depth int, next *int) []*genTask {
	n := 1 + r.Intn(3)
	out := make([]*genTask, 0, n)
	for range n {
		*next++
		t := &genTask{name: fmt.Sprintf("t%d", *next)}
		if depth > 0 && r.Intn(3) == 0 {
			t.block = genTree(r, depth-1, next)
			if r.Intn(2) == 0 {
				t.rescue = genTree(r, depth-1, next)
			}
			if r.Intn(2) == 0 {
				t.always = genTree(r, depth-1, next)
			}
		} else {
			t.fail = r.Intn(4) == 0
		}
		out = append(out, t)
	}
	return out
}

// yamlOf writes tasks as a runbook's task list at indent.
func yamlOf(tasks []*genTask, indent string, b *strings.Builder) {
	for _, t := range tasks {
		fmt.Fprintf(b, "%s- name: %s\n", indent, t.name)
		if t.block == nil {
			fmt.Fprintf(b, "%s  noop: {}\n", indent)
			continue
		}
		for _, part := range []struct {
			key   string
			tasks []*genTask
		}{{"block", t.block}, {"rescue", t.rescue}, {"always", t.always}} {
			if len(part.tasks) > 0 {
				fmt.Fprintf(b, "%s  %s:\n", indent, part.key)
				yamlOf(part.tasks, indent+"    ", b)
			}
		}
	}
}

// oracle runs tasks as the design says, appending each leaf it runs to ran,
// and reports whether the list ended failed and whether any rescue answered
// a failure.
func oracle(tasks []*genTask, ran *[]string, rescued *bool) (failed bool) {
	for _, t := range tasks {
		if t.block == nil {
			*ran = append(*ran, t.name)
			if t.fail {
				return true
			}
			continue
		}
		f := oracle(t.block, ran, rescued)
		if f && len(t.rescue) > 0 {
			if f = oracle(t.rescue, ran, rescued); !f {
				*rescued = true
			}
		}
		if oracle(t.always, ran, rescued) {
			f = true
		}
		if f {
			return true
		}
	}
	return false
}

// checkAgainstOracle runs one generated runbook on one device and compares.
func checkAgainstOracle(t *testing.T, seed int64) {
	t.Helper()
	r := rand.New(rand.NewSource(seed)) // #nosec G404 -- a reproducible test input, not a secret
	next := 0
	tasks := genTree(r, 3, &next)
	var b strings.Builder
	b.WriteString("id: fuzz\nhosts: web\ntasks:\n")
	yamlOf(tasks, "  ", &b)

	actions := &scripted{fail: map[string]bool{}}
	var mark func([]*genTask)
	mark = func(ts []*genTask) {
		for _, t := range ts {
			if t.fail {
				actions.fail[t.name+"@web1"] = true
			}
			mark(t.block)
			mark(t.rescue)
			mark(t.always)
		}
	}
	mark(tasks)

	var wantRan []string
	wantRescued := false
	wantFailed := oracle(tasks, &wantRan, &wantRescued)

	one := mapResolver{"web": {&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive}}}
	x := engine.NewExecutor(one, actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, b.String()))
	if err != nil {
		t.Fatalf("seed %d: Run: %v\n%s", seed, err, b.String())
	}
	got := make([]string, 0, len(actions.ran))
	for _, k := range actions.ran {
		got = append(got, strings.TrimSuffix(k, "@web1"))
	}
	if !slices.Equal(got, wantRan) || result.HasErrors() != wantFailed || result.Rescued() != wantRescued {
		t.Fatalf("seed %d:\nran      %v\nwant ran %v\nfailed %v want %v, rescued %v want %v\n%s",
			seed, got, wantRan, result.HasErrors(), wantFailed, result.Rescued(), wantRescued, b.String())
	}
	// No task ran twice.
	seen := map[string]bool{}
	for _, k := range got {
		if seen[k] {
			t.Fatalf("seed %d: %s ran twice\n%s", seed, k, b.String())
		}
		seen[k] = true
	}
}

// checkTwoDevices runs one generated runbook on two devices whose tasks
// fail independently. Each device's handling is its own until a failure ends
// the run for both, so each ran a prefix of what the oracle runs for it
// alone, and exactly that when neither device's failures end the run.
func checkTwoDevices(t *testing.T, seed int64) {
	t.Helper()
	r := rand.New(rand.NewSource(seed)) // #nosec G404 -- a reproducible test input, not a secret
	next := 0
	tasks := genTree(r, 3, &next)
	var b strings.Builder
	b.WriteString("id: fuzz2\nhosts: web\ntasks:\n")
	yamlOf(tasks, "  ", &b)

	// Each device fails its own random choice of the leaves.
	actions := &scripted{fail: map[string]bool{}}
	fails := map[string]map[string]bool{"web1": {}, "web2": {}}
	var leaves func([]*genTask)
	leaves = func(ts []*genTask) {
		for _, t := range ts {
			if t.block == nil {
				for _, d := range []string{"web1", "web2"} {
					if r.Intn(4) == 0 {
						actions.fail[t.name+"@"+d] = true
						fails[d][t.name] = true
					}
				}
			}
			leaves(t.block)
			leaves(t.rescue)
			leaves(t.always)
		}
	}
	leaves(tasks)

	result := runScripted(t, b.String(), actions)
	anyFailed := false
	for _, d := range []string{"web1", "web2"} {
		var want []string
		rescued := false
		failed := oracleWith(tasks, fails[d], &want, &rescued)
		anyFailed = anyFailed || failed
		var got []string
		for _, k := range actions.ran {
			if name, dev, _ := strings.Cut(k, "@"); dev == d {
				got = append(got, name)
			}
		}
		if len(got) > len(want) || !slices.Equal(got, want[:len(got)]) {
			t.Fatalf("seed %d, %s ran %v, which is not a prefix of %v\n%s", seed, d, got, want, b.String())
		}
		if !failed && !slices.Equal(got, want) && !anyFailedOther(tasks, fails, d) {
			t.Fatalf("seed %d, %s ran %v, want %v\n%s", seed, d, got, want, b.String())
		}
	}
	if result.HasErrors() != anyFailed {
		t.Fatalf("seed %d: HasErrors %v, want %v\n%s", seed, result.HasErrors(), anyFailed, b.String())
	}
}

// anyFailedOther reports whether a device other than d fails the run alone.
func anyFailedOther(tasks []*genTask, fails map[string]map[string]bool, d string) bool {
	for other, f := range fails {
		var ran []string
		rescued := false
		if other != d && oracleWith(tasks, f, &ran, &rescued) {
			return true
		}
	}
	return false
}

// oracleWith is oracle with the failing leaves named in fail rather than
// marked on the tasks.
func oracleWith(tasks []*genTask, fail map[string]bool, ran *[]string, rescued *bool) (failed bool) {
	for _, t := range tasks {
		if t.block == nil {
			*ran = append(*ran, t.name)
			if fail[t.name] {
				return true
			}
			continue
		}
		f := oracleWith(t.block, fail, ran, rescued)
		if f && len(t.rescue) > 0 {
			if f = oracleWith(t.rescue, fail, ran, rescued); !f {
				*rescued = true
			}
		}
		if oracleWith(t.always, fail, ran, rescued) {
			f = true
		}
		if f {
			return true
		}
	}
	return false
}

// TestHandlers_AgreeWithTheOracle runs a few hundred generated runbooks.
func TestHandlers_AgreeWithTheOracle(t *testing.T) {
	for seed := range int64(400) {
		checkAgainstOracle(t, seed)
		checkTwoDevices(t, seed)
	}
}

// FuzzHandlers explores further: go test -run x -fuzz FuzzHandlers.
func FuzzHandlers(f *testing.F) {
	for _, seed := range []int64{0, 1, 7, 42, 1999} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		checkAgainstOracle(t, seed)
		checkTwoDevices(t, seed)
	})
}
