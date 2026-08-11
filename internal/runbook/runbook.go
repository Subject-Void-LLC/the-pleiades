// Package runbook resolves a runbook ID to its compiled required-capability
// set, giving the Controller-side dispatch path (Phase 14, The Dispatcher,
// .SPECIFICATION/IMPLEMENTATION.md) somewhere real to read a runbook from.
// Before this package existed, the dispatcher's HTTP handler accepted a
// "runbook" query parameter and used it only to label a fixed NATS subject
// (dispatcher.go); there was no runbook storage at all, and therefore no
// way to answer Phase 14's own still-open "Call HasCapability before
// dispatch" checklist item. This package supplies the missing
// id -> compiled-capability-set lookup, computed by mirroring the exact
// engine.ActionCapability walk internal/validate/capability_rule.go
// already performs at CLI plan time (CapabilityRule's own
// "for id, task := range world.DAG.Nodes" loop), so plan-time validation
// and Controller-side dispatch can never disagree about what a given
// runbook requires.
//
// This package depends only on internal/engine, pkg/capability, and the
// standard library, deliberately: it sits below internal/api and
// internal/dispatch in the dependency graph (a future dispatch handler
// depends on this package, never the reverse), and it must never import
// internal/ent, so a non-Controller caller (a CLI subcommand, a test) can
// use it without pulling in the storage layer.
package runbook

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// Runbook is the compiled, capability-resolved view of a single runbook: an
// ID and the deduplicated set of device capabilities every task in it
// requires. It carries no task graph or execution detail of its own,
// deliberately: a dispatch handler that only needs to answer "can this
// runbook run against this device" has no reason to hold a full
// *engine.DAG, and keeping Runbook this narrow keeps Source's contract
// capability-shaped rather than letting it become a second, competing way
// to represent a compiled runbook alongside engine.DAG itself.
type Runbook struct {
	// ID is this runbook's identifier, exactly the id a caller passed to
	// Source.Get, never the WorkflowDef.ID a runbook file happens to
	// declare internally (the two usually agree in practice, but ID here
	// is always the caller's own lookup key, not a re-read of the file's
	// own contents).
	ID string

	// Required is the deduplicated, order-stable union of
	// engine.ActionCapability[task.FQCN] across every node in this
	// runbook's compiled DAG, mirroring
	// internal/validate/capability_rule.go's own per-task lookup exactly.
	// A task whose FQCN has no ActionCapability entry (e.g. "noop")
	// contributes nothing. "Order-stable" means deterministic across
	// repeated calls for the identical compiled runbook (nodes are
	// visited in sorted node-ID order before dedup; see
	// requiredCapabilities in dir_source.go), not the order tasks happen
	// to appear in the source YAML: Go's own iteration order over a map
	// (engine.DAG.Nodes) is randomized per-run, so without this a caller
	// diffing two Required slices for the same unchanged runbook across
	// two calls could see spurious differences that have nothing to do
	// with the runbook itself.
	Required []capability.Name

	// Interruptible is engine.Metadata.IsInterruptible()'s own resolved
	// answer for this runbook (dir_source.go's Get applies the default
	// there, once, so every caller of this package gets a concrete bool
	// rather than needing to know engine.Metadata.Interruptible's own
	// nil-means-true convention). PLAN.md Section 16: "Un-abortable tasks
	// (interruptible: false) finish execution, and the Controller
	// quarantines the device instead of re-issuing the lock." A Runner
	// reads this to decide whether to self-abort an in-flight execution
	// on lost lease heartbeat (internal/runner's executeWithLease) or let
	// it run to completion.
	Interruptible bool
}

// Source resolves a runbook ID to its compiled Runbook. It is the seam a
// dispatch handler depends on instead of a concrete storage mechanism
// (filesystem, database, GitOps sync), the same "modularity: every
// component is a pluggable interface" principle AGENTS.md's Architecture
// Principles require of every other component in this codebase. DirSource
// (dir_source.go, via NewDirSource) is the first, Walk-tier
// implementation; nothing about this interface assumes a filesystem.
type Source interface {
	// Get resolves id to its compiled Runbook, or returns an error
	// satisfying errors.Is(err, ErrNotFound) if id names no runbook this
	// Source can resolve. Implementations must treat id as
	// caller-controlled, untrusted input (this package's own doc comment
	// explains why: a real caller reads it straight from an HTTP query
	// parameter) and must validate it before using it to reach any
	// backing store, never echoing an id that failed validation back into
	// an error.
	Get(ctx context.Context, id string) (*Runbook, error)

	// List returns every runbook id this Source can resolve, sorted.
	//
	// It returns ids rather than compiled *Runbook values on purpose. A
	// catalog listing exists to answer "what can I run", and compiling
	// every runbook to answer it would make the cost of opening a list
	// page scale with the size and complexity of the whole runbook
	// library -- for capability data the list does not display. A caller
	// that needs a specific runbook's requirements asks Get for that one.
	//
	// An id that cannot be resolved is omitted rather than reported: a
	// single malformed file must not make the entire catalog
	// unreadable. Implementations that can distinguish "unreadable
	// backing store" from "one bad entry" still return an error for the
	// former.
	List(ctx context.Context) ([]string, error)

	// GetDAG resolves id to its full compiled *engine.DAG, the same
	// underlying compilation Get's own Runbook.Required is derived from,
	// for a caller that actually executes the runbook rather than only
	// asking what capabilities it needs (internal/adapters/native.Adapter,
	// Phase 16, Native Go Execution Adapter). It deliberately is not a
	// field on Runbook: Runbook's own doc comment explains why that type
	// stays capability-shaped rather than becoming a second, competing
	// representation of a compiled runbook alongside engine.DAG. Subject
	// to the same id-validation contract as Get.
	GetDAG(ctx context.Context, id string) (*engine.DAG, error)
}

// ErrNotFound is returned by Source.Get, wrapped with additional context by
// each implementation, when id names no runbook the Source can resolve.
// Callers should use errors.Is(err, ErrNotFound) rather than comparing
// errors directly, since every real implementation wraps this rather than
// returning it bare.
var ErrNotFound = errors.New("runbook not found")
