// Package segment: the walk that places each task and decides where a
// segment ends.
package segment

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// splitter is one Split call's state while it walks the levels.
type splitter struct {
	dag     *engine.DAG
	devices Devices
	eng     render.Engine

	segments []Segment
	// reads is every register each segment's tasks read, by ordinal.
	reads map[int]engine.Reads
	// spread is the registers the current segment's tasks wrote on several
	// devices, which a later task in it may not read.
	spread map[string]bool
	// secret is set once a task in the current segment registered a secret
	// on several devices, which ends the segment after it.
	secret bool
	// pending holds structural nodes (a parallel group's fan-out and join)
	// met before any segment exists, for the first one.
	pending []string
}

// placementOf is where task runs: within: beside a rendered target is data,
// a call needing no device is local, and anything else runs on the target
// engine.TaskTarget gives it, the one place the default and the override are
// resolved.
func placementOf(dag *engine.DAG, task *engine.Task) Placement {
	if target, _ := task.Params[collection.TargetParam].(string); task.Within != "" && strings.Contains(target, "{{") {
		return Placement{Kind: KindData, Target: task.Within}
	}
	target := engine.TaskTarget(dag, task)
	if target == "" && !engine.TaskNeedsDevice(task) {
		return Placement{Kind: KindLocal}
	}
	return Placement{Kind: KindDevices, Target: target}
}

// several reports whether p may run on more than one device. A rendered
// target may name several, and is counted so.
func (s *splitter) several(p Placement) (bool, error) {
	switch p.Kind {
	case KindLocal:
		return false, nil
	case KindData:
		return true, nil
	}
	n, ok := s.devices[p.Target]
	if !ok {
		return false, fmt.Errorf("the target %s has no device count, so where its segment ends cannot be decided", p)
	}
	return n > 1, nil
}

// level places one level's nodes. Its tasks have no order among them, so
// they are grouped by placement, the group continuing the current segment
// first and the rest in the order their first node sorts.
func (s *splitter) level(level []string) error {
	var keys []Placement
	groups := map[Placement][]string{}
	for _, id := range level {
		task := s.dag.Nodes[id]
		if task.Kind() == engine.TaskKindSynthetic {
			s.structural(id)
			continue
		}
		p := placementOf(s.dag, task)
		if _, ok := groups[p]; !ok {
			keys = append(keys, p)
		}
		groups[p] = append(groups[p], id)
	}
	if cur := s.current(); cur != nil {
		sort.SliceStable(keys, func(i, j int) bool { return keys[i] == cur.Placement && keys[j] != cur.Placement })
	}
	for _, p := range keys {
		for _, id := range groups[p] {
			if err := s.place(id, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// structural adds a parallel group's fan-out or join node, which runs
// nothing, to the current segment, or holds it for the first.
func (s *splitter) structural(id string) {
	if cur := s.current(); cur != nil {
		cur.Nodes = append(cur.Nodes, id)
		return
	}
	s.pending = append(s.pending, id)
}

// place adds one task to the current segment, or begins a new one with it.
func (s *splitter) place(id string, p Placement) error {
	task := s.dag.Nodes[id]
	reads, err := engine.TaskReads(s.eng, task)
	if err != nil {
		return fmt.Errorf("task %s: %w", label(s.dag, id), err)
	}
	if cut, register := s.cutBefore(p, reads); cut != "" {
		s.begin(p, cut, register)
	}
	cur := s.current()
	cur.Nodes = append(cur.Nodes, id)
	s.reads[cur.Ordinal] = union(s.reads[cur.Ordinal], reads)

	several, err := s.several(p)
	if err != nil {
		return err
	}
	// A lock taken on every device before any runs cannot be all or nothing
	// when each device is dispatched on its own.
	if several && task.LockAcquisition == engine.AcquisitionAllAtPlanTime {
		return fmt.Errorf("task %s takes every device's lock before any runs (lock_acquisition), and on the Walk tier each of the devices %s names is dispatched on its own, so the locks cannot be taken all or nothing", label(s.dag, id), p)
	}
	if task.Register != "" {
		if !slices.Contains(cur.Writes, task.Register) {
			cur.Writes = append(cur.Writes, task.Register)
			sort.Strings(cur.Writes)
		}
		if several {
			s.spread[task.Register] = true
		}
	}
	if several && len(task.RegisterMask) > 0 {
		s.secret = true
	}
	return nil
}

// cutBefore returns why a task placed at p, reading reads, cannot join the
// current segment, or "" when it can. For a read, register names it ("" for
// a read of every register).
func (s *splitter) cutBefore(p Placement, reads engine.Reads) (Cut, string) {
	cur := s.current()
	switch {
	case cur == nil:
		return CutStart, ""
	case p.Kind == KindData:
		return CutData, ""
	case p != cur.Placement:
		return CutTarget, ""
	case s.secret:
		return CutSecret, ""
	case reads.All && len(s.spread) > 0:
		return CutRead, ""
	}
	for _, name := range reads.Names {
		if s.spread[name] {
			return CutRead, name
		}
	}
	return "", ""
}

// begin starts a segment at p, taking any structural nodes held for it.
func (s *splitter) begin(p Placement, cut Cut, register string) {
	s.segments = append(s.segments, Segment{
		Ordinal: len(s.segments), Placement: p, Cut: cut, Register: register, Nodes: s.pending,
	})
	s.pending, s.spread, s.secret = nil, map[string]bool{}, false
}

// flush gives structural nodes no task followed a segment of their own, so
// every walked node lands in exactly one.
func (s *splitter) flush() {
	if len(s.pending) > 0 {
		s.begin(Placement{Kind: KindLocal}, CutStart, "")
	}
}

// current is the segment being filled, or nil before the first.
func (s *splitter) current() *Segment {
	if len(s.segments) == 0 {
		return nil
	}
	return &s.segments[len(s.segments)-1]
}

// label names a node for a message: its id and, when it has one, its name.
func label(dag *engine.DAG, id string) string {
	if task := dag.Nodes[id]; task != nil && task.Name != "" {
		return fmt.Sprintf("%s (name %q)", id, task.Name)
	}
	return id
}
