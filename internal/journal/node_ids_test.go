// Package journal_test: every node id the DAG builder makes reads back.
package journal_test

import (
	"context"
	"sort"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// everyShapeRunbook uses each structure the builder turns into node ids:
// pre, main and post task sections, a block with rescue and always, a
// parallel group (its synthetic fan-out and join nodes are journaled too),
// a group inside a group, and handlers, which are copied after each section.
const everyShapeRunbook = `id: every-shape
hosts: all
pretasks:
  - name: before
    exec.command:
      cmd: "true"
    notify: restart
tasks:
  - name: a block
    block:
      - name: inside
        exec.command:
          cmd: "true"
      - name: fan out
        parallel:
          - name: one
            exec.command:
              cmd: "true"
          - name: two
            exec.command:
              cmd: "true"
    rescue:
      - name: on failure
        exec.command:
          cmd: "true"
    always:
      - name: every time
        exec.command:
          cmd: "true"
  - name: side by side
    parallel:
      - name: three
        exec.command:
          cmd: "true"
posttasks:
  - name: after
    exec.command:
      cmd: "true"
handlers:
  - name: restart
    exec.command:
      cmd: "true"
`

// TestEveryNodeIDTheBuilderMakesReadsBack holds the reader's node grammar
// to the builder's, rather than to a second copy of it: each id in a DAG
// built from a runbook using every structure is recorded through the real
// FileStore and read back through the strict reader. Twice a shape the
// reader did not know (a handler's copy, a parallel group's fan-out and
// join) made one run's journal refuse every journal in the directory
// (FAILURE_PATTERNS 959).
func TestEveryNodeIDTheBuilderMakesReadsBack(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(everyShapeRunbook))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ids := make([]string, 0, len(dag.Nodes))
	for id := range dag.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// The control: the runbook reaches the shapes it is meant to.
	for _, want := range []string{"pretasks.handlers[0]", "tasks[1].fanout", "tasks[1].join", "tasks[0].block[1].fanout"} {
		if dag.Nodes[want] == nil {
			t.Fatalf("the builder made no node %q, so the test covers less than it says; ids: %q", want, ids)
		}
	}
	store, root := newStore(t)
	entries := make([]engine.JournalEntry, 0, len(ids))
	for i, id := range ids {
		entries = append(entries, entry("run-shapes", i+1, id))
	}
	if err := store.Record(context.Background(), entries); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := journal.ReadRun(root, "run-shapes"); err != nil {
		t.Fatalf("a run using every structure does not read back: %v\nids: %q", err, ids)
	}
}
