// Fuzz test for the segmenter: random runbooks of mixed targets, registers,
// reads, parallel groups and guarded blocks.
package segment_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/segment"
)

// fuzzRunbook turns data into a runbook: each pair of bytes is a task whose
// first byte picks its target and shape and whose second picks what it
// registers and reads.
func fuzzRunbook(data []byte) string {
	var b strings.Builder
	b.WriteString("id: f\nhosts: web\ntasks:\n")
	task := func(indent string, n int, pick, use byte) {
		fmt.Fprintf(&b, "%s- name: t%d\n", indent, n)
		switch pick % 6 {
		case 0:
			fmt.Fprintf(&b, "%s  noop: {}\n", indent)
		case 1, 2, 3:
			fmt.Fprintf(&b, "%s  noop:\n%s    target: %s\n", indent, indent, []string{"db", "web01", "api"}[pick%6-1])
		case 4:
			fmt.Fprintf(&b, "%s  t117b.local: {}\n", indent)
		case 5:
			fmt.Fprintf(&b, "%s  noop:\n%s    target: \"{{ vars.x }}\"\n%s  within: db\n", indent, indent, indent)
		}
		if r := use % 4; r > 0 {
			fmt.Fprintf(&b, "%s  register: r%d\n", indent, r)
		}
		if r := (use / 4) % 4; r > 0 {
			fmt.Fprintf(&b, "%s  when: stat.r%d.size() > 0\n", indent, r)
		}
		if use&0x40 != 0 {
			fmt.Fprintf(&b, "%s  register_mask: token\n", indent)
		}
	}
	n := 0
	for i := 0; i+1 < len(data) && n < 40; i += 2 {
		pick, use := data[i], data[i+1]
		switch pick / 6 % 4 {
		case 1:
			// A parallel group of this task and, when there is one, the next.
			fmt.Fprintf(&b, "  - name: p%d\n    parallel:\n", n)
			task("      ", n, pick, use)
			n++
			if i+3 < len(data) {
				i += 2
				task("      ", n, data[i], data[i+1])
				n++
			}
		case 2:
			// A block with a rescue: around this task.
			fmt.Fprintf(&b, "  - name: b%d\n    block:\n", n)
			task("      ", n, pick, use)
			n++
			fmt.Fprintf(&b, "    rescue:\n      - name: rescue%d\n        noop: {}\n", n)
			n++
		default:
			task("  ", n, pick, use)
			n++
		}
	}
	if n == 0 {
		b.WriteString("  - name: only\n    noop: {}\n")
	}
	return b.String()
}

// FuzzSplit: for any runbook that builds, Split either refuses a guarded
// block it would split, or puts every walked node in exactly one segment,
// numbered in order, in an order every edge of the walk allows, each
// segment on one placement and a rendered target alone in its own.
func FuzzSplit(f *testing.F) {
	for _, seed := range [][]byte{
		{0, 0}, {1, 1, 0, 4}, {5, 1, 5, 2}, {6, 0, 7, 5}, {12, 1, 2, 4},
		{4, 1, 4, 4, 0, 0}, {0, 0x41, 0, 0}, {13, 5, 1, 0, 2, 8, 3, 12},
	} {
		f.Add(seed)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		registerLocal(t)
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(fuzzRunbook(data)))
		if err != nil {
			t.Skip("does not build")
		}
		plan, err := segment.Split(dag, devices)
		if err != nil {
			if !strings.Contains(err.Error(), "has a rescue:, an always: or rollback_on_failure:") {
				t.Fatalf("Split refused for another reason: %v", err)
			}
			return
		}
		// A plan Split accepts keeps every guarded block's tasks in one
		// segment, structural nodes aside.
		segmentOf := map[string]int{}
		for _, seg := range plan.Segments {
			for _, id := range seg.Nodes {
				segmentOf[id] = seg.Ordinal
			}
		}
		for block, g := range dag.Guards {
			if !g.HasRescue() && !g.HasAlways() && !g.Rollback {
				continue
			}
			span := -1
			for _, id := range append(append(append([]string(nil), g.Tasks...), g.Rescue...), g.Always...) {
				if dag.Nodes[id].Kind() == engine.TaskKindSynthetic {
					continue
				}
				if span >= 0 && segmentOf[id] != span {
					t.Fatalf("guarded block %s was accepted across segments %d and %d", block, span, segmentOf[id])
				}
				span = segmentOf[id]
			}
		}
		walked, err := engine.TopologicalOrder(dag)
		if err != nil {
			t.Fatal(err)
		}
		position := map[string]int{}
		for i, seg := range plan.Segments {
			if seg.Ordinal != i {
				t.Fatalf("segment %d has ordinal %d", i, seg.Ordinal)
			}
			tasks := 0
			for _, id := range seg.Nodes {
				if _, dup := position[id]; dup {
					t.Fatalf("node %s is in two segments", id)
				}
				position[id] = len(position)
				task := dag.Nodes[id]
				if task.Kind() == engine.TaskKindSynthetic {
					continue
				}
				tasks++
				if task.Within != "" && seg.Placement.Kind != segment.KindData {
					t.Fatalf("node %s has a rendered target and sits in a %s segment", id, seg.Placement.Kind)
				}
			}
			if seg.Placement.Kind == segment.KindData && tasks != 1 {
				t.Fatalf("a rendered-target segment holds %d tasks", tasks)
			}
		}
		if len(position) != len(walked) {
			t.Fatalf("segments hold %d nodes, the walk %d", len(position), len(walked))
		}
		for from, edges := range dag.Adjacency {
			for _, e := range edges {
				if position[from] >= position[e.To] {
					t.Fatalf("edge %s -> %s runs backwards across the plan", from, e.To)
				}
			}
		}
	})
}
