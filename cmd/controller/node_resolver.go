// Package main: the node resolver the facts and journal consumers share
// (internal/journal's nodes.go), composed over the dispatch record, since
// internal/journal does not import internal/dispatch.
//
// A batch's node is resolved to the method this Controller dispatched for it,
// from what the job itself records and nothing else, so the same batch gets
// the same answer however often and whenever it is delivered:
//
//   - an ordinary job's node from the graph its first fan-out pinned
//     (dispatch.Job.Graph), the node the node id names once its item and try
//     are taken off (engine.BaseNodeID);
//   - an undo step of a block's rollback_on_failure ("<node>.undo[k]",
//     internal/engine's rollback_on_failure.go) from the node it undoes: the
//     pinned rollback: list's step k, or else one of the undos the node's
//     method declares in this Controller's catalog
//     (collection.Reversibility.Inverses), as the engine replays only an undo
//     the method declares; for a first-party method the catalog lacks, any
//     first-party method (journal.NodeMethod.AnyFirstParty);
//   - a rollback job's node from the job's own plan, step i being node
//     tasks[i] (rollback.NodeID), never from the undone runbook.
//
// A check job resolves as any job does; it publishes neither facts nor
// journal rows. A playbook job publishes neither either, and pins no graph,
// so it resolves to nothing, as does a job fanned out before graphs were
// pinned, and a node or device the graph or plan does not hold. The runbook
// source is never read: a runbook edited since the job began changes no
// answer. The job runs that one graph on both sides: a reclaim or pump
// against a runbook whose graph changed fails the devices left, and each
// dispatch carries the pinned graph's digest (wire.DispatchPayload.GraphScheme
// and GraphDigest), which a Runner whose copy digests otherwise refuses,
// running nothing.
package main

import (
	"container/list"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// nodeResolverJobs bounds how many jobs the resolver keeps what it read for.
// A batch storm of one job reads the job once; a job pushed out is read
// again on its next batch. Caching cannot change an answer: what is cached is
// read from a pinned graph or plan, which never changes once the job has
// dispatched a device, and a consumer asks only after admission found one
// dispatched.
const nodeResolverJobs = 256

// nodeResolver resolves batches' nodes, keeping each job's graph or plan in a
// bounded least recently used cache.
type nodeResolver struct {
	jobs dispatch.JobStore
	max  int

	// mu guards order and byJob.
	mu sync.Mutex
	// order holds *jobNodes, the most recently used at the front.
	order *list.List
	// byJob finds a job's element in order.
	byJob map[string]*list.Element
}

// jobNodes is what the resolver read for one job.
type jobNodes struct {
	jobID string
	// reason, when set, is why none of the job's nodes resolves.
	reason string
	// graph is the graph an ordinary job pinned.
	graph *dispatch.NodeGraph
	// plan is a rollback job's plan.
	plan *dispatch.RollbackPlan
}

// newNodeResolver builds the resolver over jobs.
func newNodeResolver(jobs dispatch.JobStore) *nodeResolver {
	return &nodeResolver{jobs: jobs, max: nodeResolverJobs, order: list.New(), byJob: map[string]*list.Element{}}
}

// Resolve implements journal.NodeResolver.
func (r *nodeResolver) Resolve(ctx context.Context, jobID, deviceID, nodeID string) (journal.NodeMethod, error) {
	view, err := r.job(ctx, jobID)
	if err != nil {
		return journal.NodeMethod{}, err
	}
	switch {
	case view.reason != "":
		return journal.NodeMethod{Reason: view.reason}, nil
	case view.plan != nil:
		return rollbackNode(view.plan, deviceID, nodeID), nil
	default:
		return graphNode(view.graph, nodeID), nil
	}
}

// job returns what the resolver read for jobID, reading it when the cache
// lacks it. A database failure is returned and nothing is cached for it, nor
// for a job the record lacks.
func (r *nodeResolver) job(ctx context.Context, jobID string) (*jobNodes, error) {
	r.mu.Lock()
	if el, ok := r.byJob[jobID]; ok {
		r.order.MoveToFront(el)
		r.mu.Unlock()
		return el.Value.(*jobNodes), nil
	}
	r.mu.Unlock()

	// Read outside the lock: two batches of one job missing at once both
	// read the same immutable record, and the store below keeps one.
	job, err := r.jobs.Lookup(ctx, jobID)
	if errors.Is(err, dispatch.ErrJobNotFound) {
		return &jobNodes{jobID: jobID, reason: "the job is not in this controller's record"}, nil
	}
	if err != nil {
		return nil, err
	}
	view := viewOf(job)
	r.mu.Lock()
	defer r.mu.Unlock()
	if el, ok := r.byJob[jobID]; ok {
		r.order.MoveToFront(el)
		return el.Value.(*jobNodes), nil
	}
	r.byJob[jobID] = r.order.PushFront(view)
	for r.order.Len() > r.max {
		oldest := r.order.Back()
		r.order.Remove(oldest)
		delete(r.byJob, oldest.Value.(*jobNodes).jobID)
	}
	return view, nil
}

// viewOf is what job's nodes resolve from: its rollback plan, or the graph
// it pinned.
func viewOf(job *dispatch.Job) *jobNodes {
	view := &jobNodes{jobID: job.JobID}
	switch {
	case job.RollbackOf != "":
		if job.Rollback == nil {
			view.reason = "the rollback job's plan cannot be read"
		}
		view.plan = job.Rollback
	case job.Graph == nil:
		view.reason = "the job pinned no runbook graph (a playbook job, or one fanned out before graphs were pinned)"
	default:
		view.graph = job.Graph
	}
	return view
}

// graphNode resolves nodeID in graph: an undo step through the node it
// undoes, any other node as the node its base id names.
func graphNode(graph *dispatch.NodeGraph, nodeID string) journal.NodeMethod {
	base := engine.BaseNodeID(nodeID)
	if node, step, ok := undoStepOf(base); ok {
		return undoNode(graph, node, step)
	}
	n, ok := graph.Nodes[base]
	switch {
	case !ok:
		return journal.NodeMethod{Reason: "the job's runbook graph has no such node"}
	case n.Marker:
		// The parallel marker runs no method.
		return journal.NodeMethod{Resolved: true}
	case n.Method == "":
		return journal.NodeMethod{Reason: "the node runs no method"}
	}
	return journal.NodeMethod{Resolved: true, Method: n.Method}
}

// undoStepOf splits an undo step's node id, "<node>.undo[k]" as the engine
// names it, into the node and k.
func undoStepOf(id string) (string, int, bool) {
	i := strings.LastIndex(id, ".undo[")
	if i <= 0 || !strings.HasSuffix(id, "]") {
		return "", 0, false
	}
	digits := id[i+len(".undo[") : len(id)-1]
	step, err := strconv.Atoi(digits)
	if err != nil || step < 0 || strconv.Itoa(step) != digits {
		return "", 0, false
	}
	return id[:i], step, true
}

// undoNode resolves step step of the undo of node, as the engine chose it
// (rollback_on_failure.go's undoSteps): the node's rollback: list when it
// has one, and otherwise the one undo its method recorded, which the engine
// replays only when the method declares it. For a first-party method that is
// one of the undos this catalog declares for it (Method, or Alternatives),
// or any first-party method a newer release may declare, kept as sent
// (AnyFirstParty). An engine action's change, and an external method's
// recorded undo, are never undone this way.
func undoNode(graph *dispatch.NodeGraph, node string, step int) journal.NodeMethod {
	n, ok := graph.Nodes[node]
	if !ok || n.Marker || n.Method == "" {
		return journal.NodeMethod{Reason: "the job's runbook graph has no such node"}
	}
	if n.Authored {
		if step >= len(n.Rollback) {
			return journal.NodeMethod{Reason: "the node's rollback: list has no such step"}
		}
		return journal.NodeMethod{Resolved: true, Method: n.Rollback[step]}
	}
	if step != 0 || engine.IsBuiltinAction(n.Method) {
		return journal.NodeMethod{Reason: "the node's method records no undo the engine replays"}
	}
	// A first-party method's recorded undo is any first-party method a
	// newer release may declare (journal.NodeMethod.AnyFirstParty); an
	// external method's is never replayed by the engine.
	firstParty := journal.FirstPartyMethod(n.Method)
	if !firstParty {
		return journal.NodeMethod{Reason: "the node's method records no undo the engine replays"}
	}
	var inverses []string
	if manifest, ok := collection.ManifestOf(n.Method); ok {
		for _, spec := range manifest.Reversibility.Inverses {
			if !slices.Contains(inverses, spec.FQCN) {
				inverses = append(inverses, spec.FQCN)
			}
		}
	}
	slices.Sort(inverses)
	undo := journal.NodeMethod{Resolved: true, AnyFirstParty: true}
	switch len(inverses) {
	case 0:
	case 1:
		undo.Method = inverses[0]
	default:
		undo.Alternatives = inverses
	}
	return undo
}

// rollbackNode resolves nodeID of a rollback job on deviceID from its plan:
// the device's step i runs as node tasks[i], one step a level, as a Runner
// builds it (rollback.Runbook without targets).
func rollbackNode(plan *dispatch.RollbackPlan, deviceID, nodeID string) journal.NodeMethod {
	steps, ok := plan.StepsFor(deviceID)
	if !ok {
		return journal.NodeMethod{Reason: "the rollback plan names no such device"}
	}
	base := engine.BaseNodeID(nodeID)
	levels := make([][]rollback.Step, len(steps))
	for i := range levels {
		levels[i] = []rollback.Step{{}}
	}
	for i, s := range steps {
		if rollback.NodeID(levels, i, 0) == base {
			return journal.NodeMethod{Resolved: true, Method: s.Method}
		}
	}
	return journal.NodeMethod{Reason: "the rollback plan has no such node"}
}
