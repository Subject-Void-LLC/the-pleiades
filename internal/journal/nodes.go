// Package journal: which method a node of a job ran, as the Controller's own
// record says, rather than as a Runner's batch claims.
//
// A batch names, for each journal row and each fact observation, the method
// that ran (engine.JournalEntry.FQCN, facts.Provenance.Method). That is the
// Runner's text, and any Runner holding a dispatch may publish a batch for it
// (admission.go), so a Runner could name a catalog method for a node that ran
// an external one and pass that method's built-in facts, or a row's undo, as
// its own. The consumers therefore ask the Controller, through a
// NodeResolver, which method it dispatched for the node: the graph of the
// runbook pinned on the job when it was first fanned out, or a rollback job's
// own plan. Both are fixed once the job is dispatched, so a batch gets the
// same answer however often, and whenever, it is delivered. A row or an
// observation is stored under that method, never under a claim that differs
// from it, and a node the Controller cannot resolve has its claim stored as
// unknown.
//
// The resolver is a port: cmd/controller implements it over the dispatch
// record alone (the job's pinned graph and a rollback job's plan), which this
// package does not import. It never reads the runbook source.
package journal

import (
	"context"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// NodeMethod is what the Controller's record says one node of a job ran.
type NodeMethod struct {
	// Resolved reports whether the Controller could resolve the node at
	// all. False for a job that pinned no graph (a playbook, which the
	// platform never compiles, or a job fanned out before graphs were
	// pinned), and for a node or device its graph or plan does not hold.
	Resolved bool

	// Method is the method the Controller dispatched for the node: the
	// task's FQCN as the runbook names it, which for a generic dispatcher
	// (pkg.install) is the generic, since the engine names the task's own
	// method on every row and observation. Empty for the parallel marker,
	// which runs no method, and for an undo step whose method could be one
	// of several (Alternatives).
	Method string

	// Alternatives are other methods a claim may name for the node as
	// itself: for an undo step of a block's rollback_on_failure that
	// replays a recorded undo, each undo the node's method declares
	// (collection.Reversibility.Inverses), one of which the engine ran.
	// Sorted.
	Alternatives []string

	// AnyFirstParty is set for an undo step replaying the undo a node's
	// first-party method recorded. Which undos a first-party method declares
	// is its release's to say, and a newer release may run a method this
	// catalog lacks or one whose declared undos grew: so a claim naming any
	// first-party Collection method that is not one of Method or
	// Alternatives is kept as sent (VerdictKept), and the rollback planner
	// holds it to the declarations as it holds every recorded undo.
	AnyFirstParty bool

	// Reason says, in fixed text that names no delivered byte, why the
	// node is not resolved. Empty when Resolved.
	Reason string
}

// Verdict is what Judge made of a batch's claim for a node.
type Verdict int

// The four verdicts Judge gives.
const (
	// VerdictUnresolved means the claim cannot be held to the node: the node
	// is not resolved, or names no single method the claim could be stored
	// under. The caller stores the claim as unknown.
	VerdictUnresolved Verdict = iota
	// VerdictNamed means the claim is the node's own method, or one of its
	// alternatives.
	VerdictNamed
	// VerdictKept means the claim names a first-party undo outside this
	// catalog's declarations for a node's first-party method
	// (AnyFirstParty): kept as sent, and said at info, since the Runner's
	// release and this one differ.
	VerdictKept
	// VerdictMismatch means the claim names another method than the node
	// ran; the caller stores the batch under Judgement.Method.
	VerdictMismatch
)

// Judgement is Judge's answer.
type Judgement struct {
	// Method is what the caller stores the batch under: the claim for
	// VerdictNamed and VerdictKept, the node's method for VerdictMismatch,
	// and empty for VerdictUnresolved.
	Method string
	// Verdict says which of the four it is.
	Verdict Verdict
	// Reason is fixed text saying why the claim is unresolved; empty for
	// every other verdict.
	Reason string
}

// Judge holds claim, a batch's method for the node, to what the node ran.
// A claim the node ran (Method, or one of Alternatives) is VerdictNamed. For
// an undo step of a first-party method, any other first-party Collection
// method is VerdictKept and any other claim VerdictUnresolved, whatever the
// catalog declares. On any other node a claim it did not run, the empty claim
// of a node that names a method among them, is VerdictMismatch stored under
// Method when the node names one, and VerdictUnresolved with a reason when it
// does not (a marker).
func (m NodeMethod) Judge(claim string) Judgement {
	if !m.Resolved {
		return Judgement{Verdict: VerdictUnresolved, Reason: m.Reason}
	}
	// The parallel marker runs no method, and its row names none.
	if m.Method == "" && len(m.Alternatives) == 0 && !m.AnyFirstParty {
		if claim == "" {
			return Judgement{Verdict: VerdictNamed}
		}
		return Judgement{Verdict: VerdictUnresolved, Reason: "the node is a parallel marker, which runs no method"}
	}
	if claim != "" && (claim == m.Method || slices.Contains(m.Alternatives, claim)) {
		return Judgement{Method: claim, Verdict: VerdictNamed}
	}
	// An undo step of a first-party method: any other first-party Collection
	// method is kept, and anything else is unresolved, however many undos
	// this catalog declares for the method (none, one or several).
	if m.AnyFirstParty {
		if FirstPartyMethod(claim) && !engine.IsBuiltinAction(claim) {
			return Judgement{Method: claim, Verdict: VerdictKept}
		}
		return Judgement{Verdict: VerdictUnresolved, Reason: "the claimed undo is not a first-party Collection method, and the node's first-party method records only those"}
	}
	if m.Method != "" {
		return Judgement{Method: m.Method, Verdict: VerdictMismatch}
	}
	return Judgement{Verdict: VerdictUnresolved, Reason: "the claimed method is none of those the node may have run"}
}

// KeptNote is what both consumers say, at info, of a batch whose claim is
// VerdictKept, so the two say it in one wording.
const KeptNote = "storing what a batch names for an undo step as it names it, an undo this controller's catalog does not declare for its node's first-party method; the Runner's release and this one differ"

// NodeResolver answers, for one node of one job's device, which method the
// Controller dispatched (NodeMethod). nodeID is the batch's own node id, with
// any item or try suffix (engine.BaseNodeID) still on it. An error means the
// record could not be read (a database failure): the consumer returns it, so
// the bus redelivers the batch.
type NodeResolver func(ctx context.Context, jobID, deviceID, nodeID string) (NodeMethod, error)

// WithNodeResolver has the consumer store each row under the method resolve
// says its node ran (nodes.go). Without it every row keeps the method its
// batch names, which only a test of the store alone wants.
func WithNodeResolver(resolve NodeResolver) SubscriberOption {
	return func(s *Subscriber) {
		s.nodes = resolve
	}
}

// FirstPartyMethod reports whether fqcn is a method no external Collection
// can provide: one of the engine's own actions, or a method under a
// first-party namespace (collection.IsFirstPartyNamespace), which the
// Runner's loader refuses to any program. The namespace list is release
// data that only grows, so a method a newer release adds under a first-party
// namespace is first-party here too. Anything else is an external
// Collection's, or a name nothing can run.
func FirstPartyMethod(fqcn string) bool {
	if engine.IsBuiltinAction(fqcn) {
		return true
	}
	ns, _, dotted := strings.Cut(fqcn, ".")
	return dotted && collection.IsFirstPartyNamespace(ns)
}

// IdentityProblem says why id, the Collection identity a batch carries for a
// method its node ran (with program, the external program it names as having
// run it, or "" where a batch names none, as a facts batch never does),
// cannot be that method's, or "" when it can. It is the one rule the facts
// and journal consumers both apply, with the one first-party rule
// (FirstPartyMethod): a first-party method is run by no program and has no
// program's identity, and an external Collection's method has no
// first-party Collection's identity. Each answer is fixed text.
func IdentityProblem(method string, id wire.CollectionIdentity, program string) string {
	firstParty := FirstPartyMethod(method)
	switch {
	case firstParty && program != "":
		return "the method is first-party, and the batch names an external program that ran it"
	case firstParty && id.Program:
		return "the method is first-party, and the batch names an external program's Collection identity"
	case !firstParty && !id.IsZero() && !id.Program:
		return "the method is an external Collection's, and the batch names a first-party Collection identity"
	}
	return ""
}
