// This file offers a finished job the one control it was missing: running
// it again.
//
// api.Dispatcher.Relaunch has existed since Phase 21 and the route has been
// mounted the whole time; nothing rendered it, so the only way to repeat a
// job was to call the JSON API by hand. The Jobs view declared no Actions
// at all.
//
// Relaunch resolves everything from the job rather than from the caller,
// which is what makes it a repeat rather than a new launch that happens to
// look similar. That is also why this action prompts for nothing: there is
// no field a caller could usefully answer, and offering one would invite
// somebody to change the configuration and still call it a relaunch.
package jobs

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Relauncher runs a job's template again.
//
// A one-method interface rather than *api.Dispatcher, so this package can
// be tested without constructing a dispatcher and everything one holds. The
// composition root passes the real thing.
type Relauncher interface {
	Relaunch(ctx context.Context, actor, jobID string) (string, []launch.IgnoredField, error)
}

// relaunchable reports whether a job can be run again.
//
// Two conditions, and both come from what Relaunch itself refuses rather
// than from anything this view invented:
//
//   - it has to have finished. Relaunching a running job would start a
//     second concurrent copy of work already in flight, which is never what
//     somebody watching a job means by "run it again".
//   - it has to have come from a template. A pre-Phase-21 job named a group
//     and a runbook directly, and there is no saved definition to repeat.
//
// Gating here rather than only in Submit is the difference between a
// control that is absent and one that is present and always fails. The
// second condition is read off the template NAME because that is what the
// row carries; an empty one is exactly the case Relaunch reports as
// ErrNotRelaunchable.
func relaunchable(row view.Row) bool {
	return terminalStates[row.Cells["state"]] && row.Cells["template"] != ""
}

// applies withdraws the relaunch control on the records it would not work
// on, and leaves every other affordance alone.
func applies(row view.Row, rel auth.LinkRel) bool {
	if rel != auth.RelExecute {
		return true
	}
	return relaunchable(row)
}

// relaunchAction runs this job's template again and sends the caller to the
// new job.
func relaunchAction(runner Relauncher) view.RecordAction {
	return view.RecordAction{
		Name:     "relaunch",
		Label:    "Relaunch",
		Heading:  "Run this job again",
		Endpoint: &apispec.RelaunchJob,
		Submit: func(ctx context.Context, id string, _ view.Values) (string, view.FieldErrors, error) {
			if runner == nil {
				return "", nil, errors.New("relaunching is not wired on this controller")
			}

			// The actor is the caller's own, read from the request rather
			// than from the submission. A relaunch is a new decision by
			// whoever made it, and attributing it to whoever launched the
			// original would put somebody else's name on a dispatch they
			// did not ask for.
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil {
				return "", nil, errors.New("no identity on the request context")
			}

			newJobID, _, err := runner.Relaunch(ctx, identity.Subject, id)
			switch {
			case errors.Is(err, api.ErrNotRelaunchable):
				// Reachable even though the control is gated: the job can
				// finish, or its template can gain a prompted credential,
				// between the page rendering and the button being pressed.
				// The dispatcher's own sentence says which of the reasons
				// applies, and it is more specific than anything this
				// layer could reconstruct.
				return "", view.FieldErrors{"": {relaunchRefusal(err)}}, nil
			case err != nil:
				return "", nil, err
			}

			// Straight to the job that was just started, because the next
			// thing anybody wants is to watch it. The same destination the
			// Templates view's own launch sends somebody to.
			return "/ui/jobs/" + newJobID, nil, nil
		},
	}
}

// relaunchRefusal is the dispatcher's own explanation, which names the
// reason rather than restating that it did not work.
func relaunchRefusal(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
