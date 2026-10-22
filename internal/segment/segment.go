// Package segment splits a compiled runbook into the segments the Walk tier
// dispatches it as (Phase 117b, PLAN.md Section 14's amendment): runs of
// consecutive tasks that go to the same devices, each dispatched once per
// device, with only the registered values later segments read carried
// between them.
//
// Split is a pure function of the DAG and of how many devices each of its
// targets names. It is built for the Controller to plan a job with, and for
// the Runner to recompute on its own copy of the runbook, refusing a
// dispatch whose segment it does not compute itself (Verify), the way a
// Runner checks a rollback's steps against its own runbook
// (internal/adapters/native's rollback.go). Nothing calls either yet:
// segmented dispatch, which does, is the next part of Phase 117b.
//
// A segment ends, and the next begins, where:
//
//   - the target changes: another device or tag, the job's own devices, or
//     a call that needs no device (which runs on a Runner's local subject);
//   - a target is rendered from data: such a task is a segment of its own,
//     since the devices it names are known only once the segments before it
//     have reported;
//   - a task reads a register that a task earlier in the segment wrote on
//     several devices, since inside one device's dispatch the read would see
//     only that device's result where the Crawl tier sees every device's;
//   - a task on several devices registered a secret (register_mask), since a
//     secret is masked across the whole run from then on, and inside one
//     device's dispatch the others would not know it.
//
// A level of the walk whose tasks go to different targets (a parallel group's
// children) runs its parts one after another, the part that continues the
// current segment first. Branches on different targets with several tasks
// each therefore alternate, a segment per level, where running each branch
// whole would need two.
//
// Split refuses what per-device dispatch would lose rather than lose it:
//
//   - a block with a rescue:, an always: or rollback_on_failure: split
//     across segments, since a failed segment ends the run and what the
//     block keeps for a failure would never run;
//   - such a block that is a unit (a task in it runs somewhere other than
//     hosts:) on several devices, since a unit's failure on one device fails
//     it on all of them and one device's dispatch cannot learn another's;
//   - lock_acquisition: all_at_plan_time on several devices, since each
//     device's dispatch takes only its own lock.
//
// Inside one segment each device's dispatch goes on after another device
// fails, where the Crawl tier stops every device after the level that
// failed; that is the Walk tier's behavior today, with the whole runbook as
// its one segment.
package segment

import (
	"fmt"
	"slices"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// Kind is where a segment's tasks run.
type Kind string

const (
	// KindDevices runs on each device a literal target names. Target ""
	// is a runbook with no hosts:, whose tasks run on the job's own devices.
	KindDevices Kind = "devices"
	// KindData runs on the devices a rendered target names, inside the
	// bound its within: names (Target).
	KindData Kind = "data"
	// KindLocal is a call that needs no device; it runs once.
	KindLocal Kind = "local"
)

// Placement is where one task, and so its segment, runs.
type Placement struct {
	Kind   Kind   `json:"kind"`
	Target string `json:"target,omitempty"`
}

// String names a placement for a message.
func (p Placement) String() string {
	switch {
	case p.Kind == KindLocal:
		return "no device"
	case p.Kind == KindData:
		return fmt.Sprintf("a target rendered from data within %q", p.Target)
	case p.Target == "":
		return "the job's devices"
	}
	return fmt.Sprintf("%q", p.Target)
}

// Cut is why a segment begins where it does.
type Cut string

const (
	// CutStart is the first segment.
	CutStart Cut = "start"
	// CutTarget is a change of target.
	CutTarget Cut = "target"
	// CutData is a task whose target is rendered from data.
	CutData Cut = "data"
	// CutRead is a task reading a register written earlier in the segment
	// on several devices (Segment.Register names it).
	CutRead Cut = "read"
	// CutSecret follows a task that registered a secret on several devices.
	CutSecret Cut = "secret"
)

// Segment is one run of tasks dispatched together, to each device of its
// placement.
type Segment struct {
	// Ordinal is the segment's place in the plan, from 0.
	Ordinal   int       `json:"ordinal"`
	Placement Placement `json:"placement"`
	Cut       Cut       `json:"cut"`
	// Register is the register a CutRead segment begins by reading, or ""
	// when it reads every register (a whole root, a computed index).
	Register string `json:"register,omitempty"`
	// Nodes are the segment's node ids, in the order the walk reaches them.
	Nodes []string `json:"nodes"`
	// Reads are the registers the segment's tasks read that an earlier
	// segment writes: the values its dispatch must carry in. ReadsAll is a
	// task reading every register, so every earlier segment's are carried.
	Reads    []string `json:"reads,omitempty"`
	ReadsAll bool     `json:"reads_all,omitempty"`
	// Writes are the registers the segment's tasks register, and Returns
	// those a later segment reads: what its outcome must carry back.
	Writes  []string `json:"writes,omitempty"`
	Returns []string `json:"returns,omitempty"`
}

// Plan is a runbook's segments, for the DAG version it was computed from.
type Plan struct {
	Version  string    `json:"version"`
	Segments []Segment `json:"segments"`
}

// Devices says how many devices each literal target names, keyed by the
// target as Selectors returns it. The key "" is the job's own devices.
type Devices map[string]int

// Selectors returns every literal target the runbook's walked tasks run
// against, sorted: the keys a caller resolves into Devices before Split.
// "" stands for the job's own devices, the target of a device task under a
// runbook with no hosts:.
func Selectors(dag *engine.DAG) ([]string, error) {
	order, err := walk(dag)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, level := range order {
		for _, id := range level {
			task := dag.Nodes[id]
			if task.Kind() == engine.TaskKindSynthetic {
				continue
			}
			if p := placementOf(dag, task); p.Kind == KindDevices {
				seen[p.Target] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// Split returns dag's segments. devices must count every target Selectors
// returns; one it does not count is an error, never a guess, since whether a
// target names one device or several decides where a segment ends.
func Split(dag *engine.DAG, devices Devices) (Plan, error) {
	order, err := walk(dag)
	if err != nil {
		return Plan{}, err
	}
	s := &splitter{dag: dag, devices: devices, eng: render.New(), reads: map[int]engine.Reads{}}
	for _, level := range order {
		if err := s.level(level); err != nil {
			return Plan{}, err
		}
	}
	s.flush()
	plan := Plan{Version: dag.Version, Segments: s.segments}
	if err := guardsWhole(dag, plan, s.several); err != nil {
		return Plan{}, err
	}
	carry(plan.Segments, s.reads)
	return plan, nil
}

// walk returns dag's happy-path chain level by level, as the executor walks
// it (engine.LevelIterator).
func walk(dag *engine.DAG) ([][]string, error) {
	it := engine.NewLevelIterator(dag)
	var order [][]string
	for {
		level, ok := it.Next()
		if !ok {
			break
		}
		order = append(order, level)
	}
	if err := it.Err(); err != nil {
		return nil, err
	}
	return order, nil
}

// carry fills each segment's Reads and Returns from the registers its tasks
// read (reads, by ordinal) and write: a segment reads in what an earlier one
// wrote, and returns what a later one reads.
func carry(segs []Segment, reads map[int]engine.Reads) {
	written := map[string]bool{}
	for i := range segs {
		r := reads[i]
		segs[i].ReadsAll = r.All
		for _, name := range r.Names {
			if written[name] {
				segs[i].Reads = append(segs[i].Reads, name)
			}
		}
		for _, name := range segs[i].Writes {
			written[name] = true
		}
	}
	later, laterAll := map[string]bool{}, false
	for i := len(segs) - 1; i >= 0; i-- {
		for _, name := range segs[i].Writes {
			if laterAll || later[name] {
				segs[i].Returns = append(segs[i].Returns, name)
			}
		}
		for _, name := range reads[i].Names {
			later[name] = true
		}
		laterAll = laterAll || reads[i].All
	}
}

// union merges b into a, keeping Names sorted and without repeats.
func union(a, b engine.Reads) engine.Reads {
	names := append(slices.Clone(a.Names), b.Names...)
	sort.Strings(names)
	return engine.Reads{Names: slices.Compact(names), All: a.All || b.All}
}
