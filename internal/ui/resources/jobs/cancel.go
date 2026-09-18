// This file offers a running job the control to stop it.
//
// Cancel is its own relation rather than a second use of execute or
// delete, and the contrast with each is the reason: execute STARTS the
// work this stops, and delete would retire the record, which this leaves
// exactly where it is. That distinction is also what lets the Jobs view
// carry both controls at once, since view.Register refuses two actions
// claiming one relation.
//
// What a person gets by pressing this is the record and the fan-out: the
// job stops, and no device it has not already reached is dispatched to.
// Work already running on a device is a separate, best-effort signal. The
// heading below says so rather than implying the run halts everywhere the
// instant the button is pressed.
package jobs

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Canceler stops a running job.
//
// A one-method interface rather than the whole dispatch.JobStore, matching
// Relauncher beside it: this package can be tested without building a
// store and everything one holds, and a double cannot accidentally reach
// the rest of the port.
type Canceler interface {
	Cancel(ctx context.Context, jobID string, canceledBy string) error
}

// cancelable reports whether a job can still be stopped.
//
// It is the exact negation of terminalStates, which is the same predicate
// the record page's own refresh uses to decide whether anything more will
// happen. Deriving both from one map is what keeps the button and the poll
// from disagreeing: a state that stops refreshing while still offering
// cancel would leave a control nobody could see go stale.
func cancelable(row view.Row) bool {
	return !terminalStates[row.Cells["state"]]
}

// cancelAction stops this job and leaves the reader on its record.
func cancelAction(canceler Canceler) view.RecordAction {
	return view.RecordAction{
		Name:     "cancel",
		Label:    "Cancel",
		Heading:  "Stop this job",
		Endpoint: &apispec.CancelJob,
		Submit: func(ctx context.Context, id string, _ view.Values) (string, view.FieldErrors, error) {
			if canceler == nil {
				return "", nil, errors.New("cancelling is not wired on this controller")
			}

			// The caller's own identity, read from the request rather than
			// from the job. Stopping a run is a new decision by whoever
			// made it, and attributing it to whoever launched the original
			// would put somebody else's name on a choice they did not
			// make. Relaunch reads the actor the same way.
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil {
				return "", nil, errors.New("no identity on the request context")
			}

			switch err := canceler.Cancel(ctx, id, identity.Subject); {
			case errors.Is(err, dispatch.ErrNotCancelable):
				// Reachable even though the control is gated. The record
				// page re-evaluates this gate every five seconds, but a
				// job can finish between the fragment rendering and the
				// button being pressed, and the action handler does not
				// re-consult the gate. Withholding the control is about
				// not drawing a dead button, never about safety.
				return "", view.FieldErrors{"": {"this job had already finished, so there was nothing to stop"}}, nil
			case errors.Is(err, dispatch.ErrJobNotFound):
				return "", nil, err
			case err != nil:
				return "", nil, err
			}

			// Back to the job, not onward to anything. The reader pressed
			// cancel while watching a run and wants to watch it settle;
			// the record's own refresh carries it from here.
			return "", nil, nil
		},
	}
}
