// Fuzz and benchmark for Select.
package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// FuzzSelect projects the tag test runbook with filters built from
// arbitrary input and checks what a selection must always hold: no panic,
// every kept leaf is one the filter runs, every leaf left out is one it
// does not, and every edge joins two kept nodes.
func FuzzSelect(f *testing.F) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	built, err := engine.NewBuilder(eval).BuildFromYAML([]byte(tagRunbook))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][2]string{{"", ""}, {"web", "slow"}, {"never", ""}, {"all", "all"}, {"tagged", "untagged"}, {"wipe,db", "always"}, {"x\x00", ""}, {",,,", "web,"}} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, only, skip string) {
		filter := engine.TagFilter{Tags: splitNonEmpty(only), SkipTags: splitNonEmpty(skip)}
		dag, err := engine.Select(built, filter)
		if err != nil {
			return
		}
		for _, task := range dag.Nodes {
			if task.Kind() == engine.TaskKindLeaf && !filter.Runs(task.Tags) {
				t.Fatalf("kept %q, which %+v does not run", task.Name, filter)
			}
		}
		for id, task := range fullLeaves(t, built) {
			if filter.Runs(task.Tags) != (dag.Nodes[id] != nil) {
				t.Fatalf("leaf %s (%q): Runs=%v but kept=%v under %+v", id, task.Name, filter.Runs(task.Tags), dag.Nodes[id] != nil, filter)
			}
		}
		for from, edges := range dag.Adjacency {
			for _, e := range edges {
				if dag.Nodes[from] == nil || dag.Nodes[e.To] == nil {
					t.Fatalf("edge %s -> %s joins a node the selection left out", from, e.To)
				}
			}
		}
	})
}

// splitNonEmpty splits s on commas, dropping empty names, as the CLI's
// flags do.
func splitNonEmpty(s string) []string {
	var out []string
	for _, name := range strings.Split(s, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// fullLeaves returns every leaf task of built's happy path by ID, read by
// selecting with a filter that keeps everything.
func fullLeaves(t *testing.T, built *engine.DAG) map[string]*engine.Task {
	t.Helper()
	everything, err := engine.Select(built, engine.TagFilter{Tags: []string{"all", "never"}})
	if err != nil {
		t.Fatal(err)
	}
	leaves := map[string]*engine.Task{}
	for id, task := range everything.Nodes {
		if task.Kind() == engine.TaskKindLeaf && !strings.Contains(id, ".always[") && !strings.Contains(id, ".rescue[") {
			leaves[id] = task
		}
	}
	return leaves
}

// bigTagRunbook is the 2,000-task runbook both benchmarks use: 200
// blocks of 10 tasks, the blocks tagged b0 to b9, every fifth task hot.
func bigTagRunbook() []byte {
	var src strings.Builder
	src.WriteString("id: big\ntasks:\n")
	for i := range 200 {
		fmt.Fprintf(&src, "  - name: block %d\n    tags: [b%d]\n    block:\n", i, i%10)
		for j := range 10 {
			fmt.Fprintf(&src, "      - name: t%d-%d\n        fqcn: noop\n", i, j)
			if j%5 == 0 {
				src.WriteString("        tags: [hot]\n")
			}
		}
	}
	return []byte(src.String())
}

// BenchmarkSelect_WithBuild parses, builds and selects the same runbook:
// the whole cost a filtered run pays before its first task, and the fair
// comparison with ansible-playbook --list-tasks, which also parses the
// playbook every time.
func BenchmarkSelect_WithBuild(b *testing.B) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		b.Fatal(err)
	}
	payload := bigTagRunbook()
	filter := engine.TagFilter{Tags: []string{"hot"}, SkipTags: []string{"b3"}}
	for b.Loop() {
		built, err := engine.NewBuilder(eval).BuildFromYAML(payload)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := engine.Select(built, filter); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSelect projects a 2,000-task runbook (200 blocks of 10 tasks,
// a fifth of them tagged) onto one tag, the operation a filtered run adds
// before its first task.
func BenchmarkSelect(b *testing.B) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		b.Fatal(err)
	}
	built, err := engine.NewBuilder(eval).BuildFromYAML(bigTagRunbook())
	if err != nil {
		b.Fatal(err)
	}
	filter := engine.TagFilter{Tags: []string{"hot"}, SkipTags: []string{"b3"}}
	b.ResetTimer()
	for b.Loop() {
		if _, err := engine.Select(built, filter); err != nil {
			b.Fatal(err)
		}
	}
}
