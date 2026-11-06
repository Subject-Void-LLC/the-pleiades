// This file offers a finished job the control that takes its changes back
// out: a rollback (Phase 40).
//
// The Controller plans the rollback when the form is sent, and refuses it
// whole when any change cannot be undone exactly. Each refusal names the
// field that accepts it, so the form comes back with every problem written
// under the list it belongs in, and the operator accepts exactly what they
// read by adding the node or job named there. Nothing is prefilled: every
// acceptance is a decision made on this form, never one carried over.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Rollbacker undoes what a job changed.
//
// One method rather than *api.Dispatcher, as Relauncher is, so this view
// can be tested without constructing a dispatcher.
type Rollbacker interface {
	Rollback(ctx context.Context, actor, jobID string, req api.RollbackRequest) (string, error)
}

// The rollback form's fields, named as the API's request fields are, since
// the Controller's problems name them.
const (
	rollbackMode         = "mode"
	rollbackLeave        = "leave"
	rollbackAllowPartial = "allow_partial"
	rollbackAllowUnknown = "allow_unknown"
	rollbackDespiteJob   = "despite_job"
	rollbackSettings     = "method_settings"
)

// rollbackFields is the rollback form.
var rollbackFields = []view.Field{
	{
		Name: rollbackMode, Label: "MODE", Kind: view.KindSelect, Required: true, InForm: true,
		Options: func(context.Context) ([]view.Option, error) {
			return []view.Option{
				{Label: "Roll back: undo the changes", Value: string(collection.ModeExecute)},
				{Label: "Check: show what a rollback would change, and change nothing", Value: string(collection.ModeCheck)},
			}, nil
		},
	},
	{
		Name: rollbackLeave, Label: "LEAVE IN PLACE", Kind: view.KindTags, InForm: true,
		Help: "Nodes whose changes to leave as they are, such as tasks[1]. A refused rollback names each one it cannot undo.",
	},
	{
		Name: rollbackAllowPartial, Label: "ALLOW A PARTIAL UNDO", Kind: view.KindTags, InForm: true,
		Help: "Nodes whose undo does not put back everything the task overwrote, to run anyway.",
	},
	{
		Name: rollbackAllowUnknown, Label: "ACCEPT AN UNKNOWN EFFECT", Kind: view.KindTags, InForm: true,
		Help: "Nodes that failed partway, whose effect nobody can know, to leave as they are; and unsealed, for a job a device never reported back from.",
	},
	{
		Name: rollbackDespiteJob, Label: "UNDO BENEATH A LATER JOB", Kind: view.KindTags, InForm: true,
		Help: "Later jobs that changed the same devices, to undo this one beneath anyway.",
	},
	{
		Name: rollbackSettings, Label: "METHOD SETTINGS", Kind: view.KindTags, InForm: true,
		Help: "Run settings for the undo steps, over the job's own, each <method>.<parameter>=<value>, such as file.remove.check_forbidden=warn " +
			"to check past a path the device says the login may not remove. Only when the job's template lets a launch set method settings.",
	},
}

// rollbackable reports whether the form can succeed for a job at all: a
// finished runbook job that changed something for real and is not itself
// a rollback. Everything else the Controller decides when the form is
// sent.
func rollbackable(row view.Row) bool {
	kind, known := launch.Lookup(launch.ResolveKind(row.Cells["kind"]))
	return terminalStates[row.Cells["state"]] &&
		known && kind.Journals &&
		row.Cells["mode"] == string(collection.ModeExecute) &&
		row.Cells["rollback_of"] == ""
}

// rollbackAction plans the undoing of this job and sends the caller to the
// rollback job, or back to the form with every problem the plan has.
func rollbackAction(rollbacker Rollbacker) view.RecordAction {
	return view.RecordAction{
		Name:     "rollback",
		Label:    "Roll back",
		Heading:  "Undo what this job changed",
		Endpoint: &apispec.RollbackJob,
		Fields:   rollbackFields,
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			if rollbacker == nil {
				return "", nil, errors.New("rolling back is not wired on this controller")
			}
			identity, ok := api.IdentityFromContext(ctx)
			if !ok || identity == nil {
				return "", nil, errors.New("no identity on the request context")
			}
			settings, problems := rollbackRunSettings(v)
			if problems != nil {
				return "", problems, nil
			}
			newJobID, err := rollbacker.Rollback(ctx, identity.Subject, id, api.RollbackRequest{
				Mode:           collection.Mode(v.Get(rollbackMode)),
				Leave:          v.Tags(rollbackLeave),
				AllowPartial:   v.Tags(rollbackAllowPartial),
				AllowUnknown:   v.Tags(rollbackAllowUnknown),
				DespiteJobs:    v.Tags(rollbackDespiteJob),
				MethodSettings: settings,
			})
			var refused *api.RollbackRefusedError
			switch {
			case errors.As(err, &refused):
				return "", rollbackProblems(refused.Problems), nil
			case errors.Is(err, api.ErrSettingLocked):
				return "", view.FieldErrors{rollbackSettings: {"the job's template does not let a launch set method settings, so a rollback cannot either"}}, nil
			case errors.Is(err, api.ErrNotRollbackable):
				return "", view.FieldErrors{"": {err.Error()}}, nil
			case err != nil:
				return "", nil, err
			}
			return "/ui/jobs/" + newJobID, nil, nil
		},
	}
}

// rollbackProblems writes each problem under the field that accepts it,
// and those nothing accepts at the top of the form.
func rollbackProblems(problems []api.RollbackProblem) view.FieldErrors {
	errs := view.FieldErrors{}
	for _, p := range problems {
		where := p.Node
		if p.Device != "" {
			where += " on " + p.Device
		}
		msg := p.Reason
		if where != "" {
			msg = where + ": " + msg
		}
		if p.Field == "" {
			errs[""] = append(errs[""], msg)
			continue
		}
		errs[p.Field] = append(errs[p.Field], fmt.Sprintf("%s. Add %s here to accept it.", msg, p.Value))
	}
	return errs
}

// rollbackRunSettings reads the form's method settings, each
// <method>.<parameter>=<value>, held to its method as the API holds them, so
// a mistyped one comes back under its field rather than as a failed request.
func rollbackRunSettings(v view.Values) (collection.RunSettings, view.FieldErrors) {
	var settings collection.RunSettings
	for _, entry := range v.Tags(rollbackSettings) {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			// Not echoed: what was typed may be anything, and the shape is
			// what the operator needs.
			return nil, view.FieldErrors{rollbackSettings: {"each entry is <method>.<parameter>=<value>, such as file.remove.check_forbidden=warn"}}
		}
		if settings == nil {
			settings = collection.RunSettings{}
		}
		settings[key] = value
	}
	if err := settings.Check(); err != nil {
		return nil, view.FieldErrors{rollbackSettings: {err.Error()}}
	}
	return settings, nil
}
