// Pause and resume: declared, not built (Phase 27).
//
// A node that needs a person's decision should pause the run: persist what
// has finished and the node awaiting the decision, let the process end,
// and resume from that node when the decision arrives, possibly days
// later, on whichever machine picks the run up. That is what a workflow's
// approval node is (AWX's workflow_approval), and what an ai method's held
// answer wants (sdk.Reviewer).
//
// The engine cannot do that yet. A run is one process holding its DAG in
// memory, so today:
//
//   - a method that holds a result returns an error wrapping sdk.ErrHeld
//     (an *sdk.HeldError naming the review's key);
//   - the executor fails that node closed, and the run reports it with
//     status held, not failed, so an operator can tell the two apart;
//   - the decision is applied by running again: the ai methods find the
//     decided review by its request key and reuse it, so nothing is asked
//     or billed twice.
//
// The declarations below are the seam Phase 27 builds on, so it fills them
// rather than designing around this code. Nothing here is wired: there is
// no option taking a PauseStore, because an option that configured a
// pause the engine then ignored would be a run that silently did not
// pause, the worst failure available. Resume refuses until it is real.
package engine

import (
	"context"
	"errors"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ErrPauseNotBuilt is what Resume returns until Phase 27 builds pausing.
var ErrPauseNotBuilt = errors.New("pausing a run for a decision is not built yet (Phase 27): a held node fails the run as held, and running it again applies the decision")

// Decision is what a person decided about a held node.
type Decision string

// The decisions, which are a review's (sdk.ReviewStatus) for a node held
// by an ai method.
const (
	DecisionApproved Decision = Decision(sdk.ReviewApproved)
	DecisionDenied   Decision = Decision(sdk.ReviewDenied)
)

// Hold is a node's request to pause for a decision: the key it is decided
// under, and why.
type Hold struct {
	Key    string
	Reason string
}

// HoldOf reports the hold err carries, false for an error that is not a
// hold. The executor and the run report read a held node through it.
func HoldOf(err error) (Hold, bool) {
	var held *sdk.HeldError
	if !errors.As(err, &held) {
		return Hold{}, false
	}
	return Hold{Key: held.Key, Reason: held.Reason}, true
}

// HeldNode is one node of a paused run awaiting a decision.
type HeldNode struct {
	// NodeID is the DAG node, and DeviceID the device it ran against.
	NodeID   string
	DeviceID string
	Hold     Hold
	// Expires is when an undecided hold is denied, zero for never.
	Expires time.Time
}

// PausedRun is what a paused run would persist: which compiled DAG, which
// nodes finished and with what, and which are held. Results are kept so
// later nodes' conditions and rendered parameters read the same values
// after resuming as they would have without the pause.
type PausedRun struct {
	RunID      string
	DAGVersion string
	Completed  map[string]ActionResult
	Held       []HeldNode
	PausedAt   time.Time
}

// PauseStore keeps paused runs and their decisions. The Controller's store
// is the one Phase 27 builds (a persisted row awaiting an event, per the
// plan's state model); the Crawl tier's would live in the project.
type PauseStore interface {
	// Pause records run as paused.
	Pause(ctx context.Context, run PausedRun) error
	// Paused returns the paused run with runID, false when none is.
	Paused(ctx context.Context, runID string) (PausedRun, bool, error)
	// Decide records a decision on one held node, by a person named by.
	Decide(ctx context.Context, runID, key string, d Decision, by string) error
}

// Resume would continue the paused run runID from its held nodes once
// each is decided. It refuses until Phase 27 builds pausing.
func (e *Executor) Resume(ctx context.Context, runID string) error {
	return ErrPauseNotBuilt
}
