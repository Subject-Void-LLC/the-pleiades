// This file is how a project sync is started when the caller holds a
// launchable reference rather than a project id: the Runner as
// internal/launchable.Launcher.
//
// It is what makes "sync this repository every night at three" possible, and
// it is deliberately thin. Everything a sync needs is already on the project's
// own record, so there is no configuration to fold and nothing to resolve: the
// work is claiming the project, attributing the attempt, and translating two
// refusals into the vocabulary a schedule understands.
//
// No credential passes through here, for the reason this package's own doc
// comment gives: the syncer resolves the project's credential id internally,
// so a scheduled sync of a private repository authenticates without this file
// ever holding a secret.
package project

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// Preflight reports whether syncing this project would be refused, without
// starting a sync.
//
// Two refusals, and both are things somebody should be told while they are
// still looking at the form rather than at 03:00 on a page nobody has open. A
// project with nothing to fetch can never sync, and a saved launch
// configuration cannot apply to a sync at all: there is nothing for it to
// override, so accepting one would be storing values that silently never
// applied.
//
// A sync already running is deliberately NOT refused here. It is a collision
// with the present moment rather than a property of the schedule, and by the
// time the schedule fires the sync will usually have finished; refusing to
// save the schedule over it would be refusing the wrong thing.
func (r *Runner) Preflight(ctx context.Context, req launchable.Request) error {
	if req.SavedConfigID != 0 {
		return launchable.Refusal{
			Field:   launchable.SavedConfigField,
			Message: "A project sync takes no launch-time overrides: what it fetches is the project's own record.",
		}
	}

	p, err := r.launchTarget(ctx, req)
	if err != nil {
		return err
	}
	if !p.Syncable() {
		return launchable.Refusal{
			Field:   launchable.TargetField,
			Message: ErrNotSyncable.Error() + ". Give it a git URL first, or schedule something else.",
			Err:     ErrNotSyncable,
		}
	}
	return nil
}

// Launch starts a sync of the project this launchable stands for and reports
// the attempt it started.
//
// The attempt's id is the sync run's own row id, rendered as a string because
// that is what a launchable reports: a job names itself with an opaque job id
// and a sync with an integer, and the consumer recording it does not care
// which, only that it can find the run again.
func (r *Runner) Launch(ctx context.Context, req launchable.Request) (launchable.Launched, error) {
	if err := r.Preflight(ctx, req); err != nil {
		return launchable.Launched{}, err
	}

	p, err := r.launchTarget(ctx, req)
	if err != nil {
		return launchable.Launched{}, err
	}

	runID, err := r.Enqueue(ctx, p.ID, req.Actor)
	switch {
	case errors.Is(err, ErrSyncInProgress):
		// Translated rather than returned as it is, because a schedule treats
		// this one refusal differently from every other: it records a skip and
		// moves on, instead of reporting a failure it would retry into a run
		// of failures for as long as the clone lasts.
		return launchable.Launched{}, fmt.Errorf("%w: %s", launchable.ErrBusy, err.Error())
	case err != nil:
		return launchable.Launched{}, err
	}

	// The unified job type is stamped by the router from this type's own
	// descriptor, so it is deliberately not set here.
	return launchable.Launched{RunID: strconv.Itoa(runID)}, nil
}

// launchTarget resolves a launchable reference into the project it stands for.
func (r *Runner) launchTarget(ctx context.Context, req launchable.Request) (Project, error) {
	p, err := r.store.ByLaunchable(ctx, req.Target.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Project{}, launchable.Refusal{
				Field:   launchable.TargetField,
				Message: "That project no longer exists.",
				Err:     err,
			}
		}
		return Project{}, fmt.Errorf("project: resolving launchable %d: %w", req.Target.ID, err)
	}
	return p, nil
}
