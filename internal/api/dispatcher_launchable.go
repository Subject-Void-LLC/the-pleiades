// This file is how a job template is launched when the caller holds a
// launchable reference rather than a template id: the Dispatcher as
// internal/launchable.Launcher.
//
// It replaces LaunchScheduled, which the scheduler used to call directly with
// a template id. The refusals are the same ones that method made, and are the
// reason this is not a two-line adapter: a template bound to a credential
// that prompts at launch, or carrying a saved configuration that answers a
// survey password, can never run unattended, because neither value is stored
// and there is nobody to ask. What changes is when they are said. Preflight
// makes them answerable at the write, so a schedule that could only ever fail
// is refused while somebody is still looking at the form, instead of failing
// silently at three in the morning.
package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// Preflight reports whether launching this template unattended would be
// refused, without launching it.
//
// It is the Preflighter half of the launchable contract. Every refusal is a
// launchable.Refusal carrying the field it belongs to, so a form can attach
// it to the control somebody chose rather than rendering it as a whole-form
// failure.
func (d *Dispatcher) Preflight(ctx context.Context, req launchable.Request) error {
	_, _, err := d.unattended(ctx, req)
	return err
}

// Launch runs the template this launchable stands for, unattended.
//
// The actor comes from the request, which for a schedule is
// schedule.ScheduleActor's "scheduler:<id>" string. It is never derived here:
// internal/access's audited store refuses an unattributed write, and its doc
// comment prescribes exactly this shape for an unattended run, naming which
// schedule so a reader can get from an unexpected job back to its cause.
func (d *Dispatcher) Launch(ctx context.Context, req launchable.Request) (launchable.Launched, error) {
	tmpl, cfg, err := d.unattended(ctx, req)
	if err != nil {
		return launchable.Launched{}, err
	}

	// No prompted credential inputs, and there cannot be any: every
	// credential that would need one was refused above. A scheduled run runs
	// for real, so a scheduled check may do what a real run would.
	jobID, _, err := d.LaunchTemplate(ctx, req.Actor, tmpl.ID, cfg, nil, MayRunForReal(true))
	if err != nil {
		return launchable.Launched{}, err
	}

	// The unified job type is stamped by the router from the type's own
	// descriptor, so it is deliberately not set here.
	return launchable.Launched{RunID: jobID}, nil
}

// unattended resolves a launchable reference into the template it stands for
// and the configuration an unattended run would use, refusing everything such
// a run cannot do.
//
// One function for both Preflight and Launch, deliberately: a refusal that
// Preflight did not make and Launch did would be a schedule that saved
// cleanly and then failed forever, which is the failure this whole seam
// exists to prevent.
func (d *Dispatcher) unattended(ctx context.Context, req launchable.Request) (launch.Template, launch.Config, error) {
	if d.templates == nil {
		return launch.Template{}, launch.Config{}, fmt.Errorf("launching by template is not wired on this controller")
	}

	tmpl, err := d.templates.ByLaunchable(ctx, req.Target.ID)
	if err != nil {
		if errors.Is(err, launch.ErrNotFound) {
			return launch.Template{}, launch.Config{}, launchable.Refusal{
				Field:   launchable.TargetField,
				Message: "That template no longer exists.",
				Err:     err,
			}
		}
		return launch.Template{}, launch.Config{}, fmt.Errorf("resolve the template for launchable %d: %w", req.Target.ID, err)
	}

	// A credential whose type prompts for an input at launch cannot be run
	// unattended: a prompted input is never stored, so there is nobody to ask
	// and nothing to replay.
	if err := d.refuseUnrepeatableCredentials(ctx, tmpl); err != nil {
		return launch.Template{}, launch.Config{}, launchable.Refusal{
			Field:   launchable.TargetField,
			Message: err.Error(),
			Err:     err,
		}
	}

	if req.SavedConfigID == 0 {
		return tmpl, launch.Config{}, nil
	}

	stored, err := d.savedConfigFor(ctx, tmpl.ID, req.SavedConfigID)
	if err != nil {
		// A configuration belonging to another template is the case worth
		// naming: it is a choice somebody made on a form, and until now it
		// was only caught when the schedule fired.
		return launch.Template{}, launch.Config{}, launchable.Refusal{
			Field:   launchable.SavedConfigField,
			Message: err.Error(),
			Err:     err,
		}
	}

	// A stored survey password is not replayed, which is the same refusal a
	// relaunch makes and which matters MORE here, not less. There the value
	// would be reused once, by a person who chose to press the button; here it
	// would be reused unattended, on every occurrence, indefinitely, under no
	// individual's decision at all.
	for _, name := range tmpl.Survey.SecretVariables() {
		if _, answered := stored.Answers[name]; answered {
			return launch.Template{}, launch.Config{}, launchable.Refusal{
				Field: launchable.SavedConfigField,
				Message: fmt.Sprintf(
					"That saved configuration answers %q, which this platform will not replay unattended.", name),
			}
		}
	}
	return tmpl, stored.Config(), nil
}
