// Package segment: the checks on a plan, the block rule Split holds every
// plan to and the one a Runner holds a dispatched segment to.
package segment

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// guardsWhole refuses a plan that splits a block with a rescue:, an always:
// or rollback_on_failure: across segments. A failed segment ends the run, so
// a rescue, an always or an undo in a later segment than the failure would
// never run; each such block is named with where and why it was split. A
// parallel group's structural nodes run nothing and are not counted.
//
// It also refuses such a block that is a unit (engine.DAG.Units: a task in
// it runs somewhere other than hosts:) on several devices. A unit's failure
// on one device fails it on all of them on the Crawl tier, its rescue and
// undo running everywhere, and one device's dispatch cannot learn another's
// failure.
func guardsWhole(dag *engine.DAG, plan Plan, several func(Placement) (bool, error)) error {
	at := map[string]int{}
	for _, seg := range plan.Segments {
		for _, id := range seg.Nodes {
			at[id] = seg.Ordinal
		}
	}
	blocks := make([]string, 0, len(dag.Guards))
	for block := range dag.Guards {
		blocks = append(blocks, block)
	}
	sort.Strings(blocks)
	for _, block := range blocks {
		g := dag.Guards[block]
		if !g.HasRescue() && !g.HasAlways() && !g.Rollback {
			continue
		}
		var spans []int
		for _, id := range slices.Concat(g.Tasks, g.Rescue, g.Always) {
			if dag.Nodes[id].Kind() == engine.TaskKindSynthetic {
				continue
			}
			if ord, ok := at[id]; ok && !slices.Contains(spans, ord) {
				spans = append(spans, ord)
			}
		}
		if len(spans) == 1 && dag.Units[block] {
			place := plan.Segments[spans[0]].Placement
			if many, err := several(place); err != nil {
				return err
			} else if many {
				return fmt.Errorf("block %s has a rescue:, an always: or rollback_on_failure:, and a task in it runs somewhere other than hosts:, so a failure on one device fails it on every device; on the Walk tier each device %s names is dispatched on its own and cannot learn another's failure. "+
					"Run the block on one device, or move the task with its own target out of it", label(dag, block), place)
			}
		}
		if len(spans) < 2 {
			continue
		}
		sort.Ints(spans)
		why := make([]string, 0, len(spans)-1)
		for _, ord := range spans[1:] {
			why = append(why, plan.Segments[ord].describe())
		}
		return fmt.Errorf("block %s has a rescue:, an always: or rollback_on_failure:, and on the Walk tier its tasks would run in %d separate dispatches (%s); a failed dispatch ends the run, so what the block keeps for a failure would not run. "+
			"Keep the block's tasks, rescue and always on one target, and read no register its tasks write on several devices inside it",
			label(dag, block), len(spans), strings.Join(why, "; "))
	}
	return nil
}

// describe says why a segment begins, for a message.
func (s Segment) describe() string {
	switch s.Cut {
	case CutTarget:
		return fmt.Sprintf("one begins where the target changes to %s", s.Placement)
	case CutData:
		return fmt.Sprintf("one begins at %s", s.Placement)
	case CutRead:
		if s.Register == "" {
			return "one begins where a task reads every register, after a task wrote one on several devices"
		}
		return fmt.Sprintf("one begins where a task reads the register %q, which an earlier task wrote on several devices", s.Register)
	case CutSecret:
		return "one begins after a task registered a secret (register_mask) on several devices"
	}
	return "one begins the run"
}

// Verify recomputes dag's plan from devices and refuses a dispatched segment
// that is not exactly the one this copy of the runbook computes at the same
// ordinal: planned from another version, with other nodes, another placement
// or other carried registers. A Runner calls it before running a segment, so
// a Controller whose runbook differs, or a message that names nodes of its
// own choosing, runs nothing.
func Verify(dag *engine.DAG, devices Devices, version string, got Segment) error {
	if version != dag.Version {
		return fmt.Errorf("this copy of runbook %q is not the version the segment was planned from", dag.ID)
	}
	plan, err := Split(dag, devices)
	if err != nil {
		return err
	}
	if got.Ordinal < 0 || got.Ordinal >= len(plan.Segments) {
		return fmt.Errorf("runbook %q has %d segments here, so segment %d does not exist", dag.ID, len(plan.Segments), got.Ordinal)
	}
	want := plan.Segments[got.Ordinal]
	switch {
	case !slices.Equal(want.Nodes, got.Nodes):
		return fmt.Errorf("segment %d of runbook %q runs %s here, not the %d nodes the dispatch names", got.Ordinal, dag.ID, quoted(want.Nodes), len(got.Nodes))
	case want.Placement != got.Placement:
		return fmt.Errorf("segment %d of runbook %q runs on %s here, not where the dispatch says", got.Ordinal, dag.ID, want.Placement)
	case !slices.Equal(want.Reads, got.Reads) || want.ReadsAll != got.ReadsAll || !slices.Equal(want.Returns, got.Returns):
		return fmt.Errorf("segment %d of runbook %q carries other registers here", got.Ordinal, dag.ID)
	}
	return nil
}

// maxQuoted bounds how many node ids a message quotes.
const maxQuoted = 8

// quoted lists ids for a message, each quoted and at most maxQuoted of them.
// Only this copy's own ids are ever listed: a dispatch's are counted, never
// echoed, since the message carrying them is what Verify does not trust.
func quoted(ids []string) string {
	shown := ids[:min(len(ids), maxQuoted)]
	parts := make([]string, len(shown))
	for i, id := range shown {
		parts[i] = fmt.Sprintf("%q", id)
	}
	out := strings.Join(parts, ", ")
	if len(ids) > maxQuoted {
		out += fmt.Sprintf(" and %d more", len(ids)-maxQuoted)
	}
	return out
}
