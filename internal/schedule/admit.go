// This file is what a schedule's write path checks about its target before
// accepting it.
//
// It replaces a narrower check that asked one question (does this template
// belong to the right organization) of one sort of target. Four things are
// checked now, and each one exists because it would otherwise surface at a
// fire, unattended, hours later, as a skipped occurrence nobody is watching:
//
//  1. The target exists.
//  2. The caller may launch it, which is the scope its own type declares.
//     Before this, writing a schedule needed only schedule:write, so anybody
//     who could write a schedule could arrange for anything to run
//     (FAILURE_PATTERNS.md #268).
//  3. Its tenant is the schedule's tenant, so one organization cannot put
//     another's work on a timer.
//  4. The type accepts this launch at all: a saved configuration only where
//     one is meaningful, and whatever else the type's own launcher objects to
//     (a project with nothing to fetch, a template needing an answer nobody
//     will be there to give).
//
// Every failure is a FieldError naming the control that caused it, because
// these are refusals at a form rather than faults: a message with nowhere to
// land renders as a form-wide failure that says nothing about what to change.
package schedule

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// TargetField is the name every refusal about what a schedule launches is
// attributed to, and the name the API uses for it: AWX's own
// unified_job_template, defined once in internal/launchable so that this
// package, the API, the form and each type's launcher all blame one string.
const TargetField = launchable.TargetField

// Admitter is what this package needs in order to admit a target: read it,
// and ask its type whether the launch would be refused.
//
// It is launchable.Admission in a real controller. Declared here as an
// interface so that this package depends on the question rather than on the
// Dispatcher and the project runner, which is what keeps internal/schedule
// free of internal/api and internal/project (internal/archtest asserts it).
type Admitter interface {
	Get(ctx context.Context, id int) (launchable.Target, error)
	Preflight(ctx context.Context, req launchable.Request) error
}

// admit resolves what a schedule launches and refuses everything a schedule
// must not be saved with, returning the resolved target.
func (s *entStore) admit(ctx context.Context, sched Schedule, reach launchable.Reach) (launchable.Target, error) {
	target, err := s.admitter.Get(ctx, sched.LaunchableID)
	switch {
	case errors.Is(err, launchable.ErrNotFound):
		return launchable.Target{}, FieldError{
			Field:   TargetField,
			Message: "That is not something this deployment can launch. It may have been deleted.",
			Cause:   err,
		}
	case err != nil:
		return launchable.Target{}, err
	}

	if err := reach.Admits(target); err != nil {
		return launchable.Target{}, admissionRefusal(target, err)
	}

	d, err := launchable.Describe(target)
	if err != nil {
		return launchable.Target{}, FieldError{Field: TargetField, Message: err.Error(), Cause: err}
	}
	if err := d.AdmitsSavedConfig(sched.SavedConfigID); err != nil {
		return launchable.Target{}, FieldError{
			Field:   launchable.SavedConfigField,
			Message: err.Error(),
			Cause:   err,
		}
	}

	// The type's own objections, asked before the schedule is stored rather
	// than discovered when it fires.
	if err := s.admitter.Preflight(ctx, launchable.Request{
		Target:        target,
		SavedConfigID: sched.SavedConfigID,
	}); err != nil {
		return launchable.Target{}, preflightRefusal(err)
	}
	return target, nil
}

// admissionRefusal renders a Reach refusal as a message about the control the
// person chose, keeping the distinction the two errors draw: one is about
// permission and one is about tenancy, and telling them apart is what lets
// somebody act on either.
func admissionRefusal(target launchable.Target, err error) error {
	switch {
	case errors.Is(err, launchable.ErrNotPermitted):
		return FieldError{
			Field:   TargetField,
			Message: "You may not launch that, so you may not schedule it either. " + err.Error(),
			Cause:   err,
		}
	case errors.Is(err, launchable.ErrCrossTenant):
		return FieldError{
			Field:   TargetField,
			Message: "That belongs to a different organization.",
			Cause:   err,
		}
	default:
		return FieldError{Field: TargetField, Message: err.Error(), Cause: err}
	}
}

// preflightRefusal renders a type's own objection, keeping the field the
// launcher blamed when it named one: it knows better than this package which
// control is wrong.
func preflightRefusal(err error) error {
	var refusal launchable.Refusal
	if errors.As(err, &refusal) {
		field := refusal.Field
		if field == "" {
			field = TargetField
		}
		return FieldError{Field: field, Message: refusal.Message, Cause: err}
	}
	return FieldError{Field: TargetField, Message: err.Error(), Cause: err}
}
