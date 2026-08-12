// Package routing selects which execution adapter runs a dispatch, from
// the launch kind the dispatch carries.
//
// It exists because the Runner's routing decision used to be made at
// process-composition time: cmd/runner constructed exactly one adapter and
// every message went to it, with a comment saying adapter selection was out
// of scope "since a real adapter-selection mechanism needs a Launchable
// Kind registry (Phase 21) that does not exist yet". The registry exists
// now, so this is that mechanism.
//
// It lives in internal/ rather than in cmd/runner for the reason CLAUDE.md
// gives: a composition root parses configuration and wires concrete drivers
// into interfaces, and business logic does not live there. Deciding which
// adapter runs a payload is a rule, and a rule that lived only in a main
// package could not be tested without building a binary.
//
// The Router satisfies the same one-method interface an adapter does, so
// the Agent that consumes it does not know it is holding a router at all.
// That is what keeps the routing out of the Agent: adding a kind changes
// this package's input, not the Agent's code.
package routing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Executor is what a Router routes to. It is structurally identical to
// internal/runner.ExecutionAdapter, and declared here rather than imported
// so that this package does not depend on the Agent it is composed into.
// Both concrete adapters already satisfy it.
type Executor interface {
	Execute(ctx context.Context, payload wire.DispatchPayload) error
}

// ErrNoAdapter is returned when a payload names a kind this process cannot
// run.
//
// It is deliberately distinguishable from an execution failure, because the
// two mean different things to a Runner holding the message. A job that ran
// and failed is a result to report and a message to acknowledge. A kind
// this binary has no adapter for can never become routable by redelivery,
// so retrying it forever is the one thing that must not happen.
var ErrNoAdapter = fmt.Errorf("routing: no execution adapter for this kind")

// DefaultKind is what an empty Kind on the wire resolves to.
//
// The rule lives here, at exactly one place, and it is what makes
// wire.DispatchPayload.Kind an additive field: a dispatch published before
// that field existed carries no kind, and must still reach the adapter it
// was always going to reach. Defaulting it independently at two call sites
// is how the two would eventually disagree.
const DefaultKind = "runbook"

// Router picks an adapter per dispatch.
type Router struct {
	// byKind maps a launch kind onto the adapter that runs it. Keyed by
	// KIND rather than by adapter name, even though the registry stores
	// the adapter name on the descriptor, because the payload carries a
	// kind and a lookup that had to translate first would be a second
	// place the mapping could be wrong.
	byKind map[string]Executor
}

// New builds a Router over the registered launch kinds.
//
// adapters is keyed by adapter NAME, the value each kind's Descriptor
// declares: "native", "legacy". That indirection is the point. A caller
// composes the adapters it has, this resolves which kinds those adapters
// serve by asking the registry, and neither side names the other. Adding a
// kind that runs on an already-composed adapter therefore needs no change
// here and none in the composition root.
//
// A kind whose adapter was not supplied is not an error at construction: a
// deployment that has never run Ansible legitimately composes no legacy
// adapter, and refusing to build a Router would stop the Runner booting
// over a capability it does not use. Such a kind is unroutable at dispatch
// time instead, which is reported on the job rather than swallowed.
func New(adapters map[string]Executor) *Router {
	byKind := make(map[string]Executor, len(adapters))
	for _, d := range launch.Kinds() {
		if adapter, ok := adapters[d.Adapter]; ok && adapter != nil {
			byKind[d.Kind] = adapter
		}
	}
	return &Router{byKind: byKind}
}

// Execute routes one dispatch to the adapter its kind declares.
//
// This is the whole of the routing decision, and it is a map lookup rather
// than a switch. PLAN.md Section 28's requirement, and Phase 21's
// adversarial gate, is that no consumer branches on kind: a switch here
// would need a case per kind, which is the closed-enum cost the open
// registry exists to avoid.
func (r *Router) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	kind := Resolve(payload.Kind)

	adapter, ok := r.byKind[kind]
	if !ok {
		return fmt.Errorf("%w: %q (this runner can run %s)", ErrNoAdapter, kind, r.describe())
	}
	return adapter.Execute(ctx, payload)
}

// Resolve normalises a kind read off the wire.
//
// Exported because the Controller-side check and the Runner-side lookup
// must agree about what an empty kind means, and two implementations of one
// default is how they would stop agreeing.
func Resolve(kind string) string {
	if trimmed := strings.TrimSpace(kind); trimmed != "" {
		return trimmed
	}
	return DefaultKind
}

// Routable reports whether this Router can run a kind. It is what a
// composition root logs at startup, so an operator learns that playbooks
// are unroutable when the Runner starts rather than when somebody launches
// one.
func (r *Router) Routable(kind string) bool {
	_, ok := r.byKind[Resolve(kind)]
	return ok
}

// Kinds lists what this Router can run, in a stable order.
func (r *Router) Kinds() []string {
	out := make([]string, 0, len(r.byKind))
	for kind := range r.byKind {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// describe renders the routable kinds for an error message. A refusal that
// names what this binary *can* run turns "no adapter" into a diagnosis.
func (r *Router) describe() string {
	kinds := r.Kinds()
	if len(kinds) == 0 {
		return "nothing"
	}
	return strings.Join(kinds, ", ")
}
