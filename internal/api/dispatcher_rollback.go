// Package api: rolling a job back (Phase 40).
//
// A rollback is planned here, when it is asked for, by the same planner
// `pleiades rollback` uses (internal/rollback), from the undone job's
// journal, what other jobs have done on its devices since, and the
// runbook it ran. Every step is held to that runbook before a job exists.
// A plan the Controller will not stand behind is refused with every
// problem it found, each naming the request field that accepts it, and
// nothing is created; a plan it will is stored on a new job, which fans
// out like any other, except that only the devices the plan names are
// dispatched, each with its own steps, on the rollback subject.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// RollbackJournal is the journal a rollback plans from.
type RollbackJournal interface {
	// AllForJob returns every entry the job wrote.
	AllForJob(ctx context.Context, jobID string) ([]engine.JournalEntry, error)
	// OnDevicesSince returns every entry other jobs wrote on devices that
	// finished after since.
	OnDevicesSince(ctx context.Context, jobID string, devices []string, since time.Time) ([]engine.JournalEntry, error)
}

// DeviceNamer resolves device ids to the names the inventory holds for
// them now, leaving out an id no device holds.
type DeviceNamer func(ctx context.Context, ids []string) (map[string]string, error)

// WithRollback wires what a rollback plans from. Without it the rollback
// route refuses every request.
func WithRollback(journal RollbackJournal, names DeviceNamer) DispatcherOption {
	return func(d *Dispatcher) {
		d.rollbackJournal = journal
		d.deviceNames = names
	}
}

// ErrNotRollbackable is returned when a job cannot be rolled back at all,
// whatever the request accepts.
var ErrNotRollbackable = errors.New("api: this job cannot be rolled back")

// RollbackRequest is what a caller asked a rollback to do and accept.
type RollbackRequest struct {
	// Mode is execute, or check to see what the rollback would do.
	Mode collection.Mode
	// Leave, AllowPartial and AllowUnknown name node ids, as
	// rollback.Request does; AllowUnknown also takes rollback.Unsealed.
	Leave, AllowPartial, AllowUnknown []string
	// DespiteJobs names later jobs to undo beneath.
	DespiteJobs []string
}

// RollbackRefusedError is a rollback the Controller will not stand behind,
// with every problem it found.
type RollbackRefusedError struct {
	Problems []RollbackProblem
}

// Error summarizes the refusal.
func (e *RollbackRefusedError) Error() string {
	return fmt.Sprintf("the rollback was refused, before any device was contacted, for %d reason(s)", len(e.Problems))
}

// RollbackProblem is one reason a rollback was refused, with the request
// field and value that accept it, when anything can.
type RollbackProblem struct {
	Node   string `json:"node,omitempty"`
	Device string `json:"device,omitempty"`
	Reason string `json:"reason"`
	// Field is the request field that accepts the problem, and Value what
	// to put in it; both empty when nothing can.
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}

// acceptFields maps the planner's flag spelling to the request's fields.
var acceptFields = map[string]string{
	"--leave":         "leave",
	"--allow-partial": "allow_partial",
	"--allow-unknown": "allow_unknown",
	"--despite-run":   "despite_job",
}

// problemFrom renders a planner problem for the API.
func problemFrom(p rollback.Problem) RollbackProblem {
	out := RollbackProblem{Node: p.Node, Device: p.DeviceName, Reason: p.Reason}
	if flag, value, ok := strings.Cut(p.Accept, " "); ok {
		if field, known := acceptFields[flag]; known {
			out.Field, out.Value = field, value
		}
	}
	return out
}

// Rollback plans the undoing of job jobID and, when the Controller will
// stand behind the plan, creates the rollback job and returns its id. The
// actor is the caller's own: a rollback is a new decision by whoever made
// it.
func (d *Dispatcher) Rollback(ctx context.Context, actor, jobID string, req RollbackRequest) (string, error) {
	if d.rollbackJournal == nil || d.deviceNames == nil {
		return "", fmt.Errorf("rolling back is not wired on this controller")
	}
	job, tasks, err := d.jobs.Get(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("load job %s: %w", jobID, err)
	}
	if err := rollbackable(job); err != nil {
		return "", err
	}

	// What a template bound, as a relaunch reads it: the template as it is
	// now, so a rotated secret is used, and a credential that asks for an
	// input at launch refused, since the rollback cannot ask.
	var tmpl launch.Template
	if job.TemplateID != 0 {
		if d.templates == nil {
			return "", fmt.Errorf("launching by template is not wired on this controller")
		}
		if tmpl, err = d.templates.Get(ctx, job.TemplateID); err != nil {
			return "", fmt.Errorf("resolve template %d: %w", job.TemplateID, err)
		}
		if err := d.refuseUnrepeatableCredentials(ctx, tmpl); err != nil {
			return "", fmt.Errorf("%w: %w", ErrNotRollbackable, err)
		}
	}

	plan, err := d.planRollback(ctx, job, tasks, req)
	if err != nil {
		return "", err
	}

	fields := maps.Clone(job.Fields)
	if fields == nil {
		fields = launch.Fields{}
	}
	fields[launch.ModeField] = string(req.Mode)
	rb := &dispatch.Job{
		JobID:          newJobID(),
		RunbookID:      job.RunbookID,
		GroupName:      job.GroupName,
		Actor:          actor,
		Kind:           job.Kind,
		InventoryID:    job.InventoryID,
		OrganizationID: job.OrganizationID,
		TemplateID:     job.TemplateID,
		TemplateName:   job.TemplateName,
		Fields:         fields,
		ExtraVars:      withoutSecretAnswers(job.ExtraVars, tmpl),
		CredentialIDs:  tmpl.CredentialIDs,
		// The route requires runbook:execute, so a check of this rollback
		// may run an external program's Check, as a check of any job its
		// caller may run for real does.
		ExternalChecks: true,
		RollbackOf:     job.JobID,
		Rollback:       plan,
	}
	if err := d.jobs.Create(ctx, rb); err != nil {
		return "", fmt.Errorf("create the rollback of job %s: %w", jobID, err)
	}
	if err := d.publishRequested(ctx, actor, rb.JobID, nil); err != nil {
		return "", err
	}
	return rb.JobID, nil
}

// rollbackable refuses a job no request could roll back.
func rollbackable(job *dispatch.Job) error {
	switch {
	case job.RollbackOf != "":
		return fmt.Errorf("%w: it is a rollback of job %s; undoing it is not a rollback, so run that job's template again instead", ErrNotRollbackable, job.RollbackOf)
	case !journals(job.Kind):
		return fmt.Errorf("%w: a %s job keeps no journal to plan a rollback from", ErrNotRollbackable, launch.ResolveKind(job.Kind))
	case job.State != "completed" && job.State != "failed" && job.State != "canceled":
		return fmt.Errorf("%w: it has not finished; roll it back once it has", ErrNotRollbackable)
	}
	if mode, err := job.Mode(); err != nil || mode == collection.ModeCheck {
		return fmt.Errorf("%w: it was a check, which changed nothing", ErrNotRollbackable)
	}
	return nil
}

// journals reports whether a run of kind keeps the journal a rollback is
// planned from.
func journals(kind string) bool {
	d, ok := launch.Lookup(launch.ResolveKind(kind))
	return ok && d.Journals
}

// planRollback plans job's undo and holds every step to the runbook it
// ran, returning the plan a rollback job stores or a refusal.
func (d *Dispatcher) planRollback(ctx context.Context, job *dispatch.Job, tasks []dispatch.JobTask, req RollbackRequest) (*dispatch.RollbackPlan, error) {
	entries, err := d.rollbackJournal.AllForJob(ctx, job.JobID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: it journaled nothing, so there is nothing to undo", ErrNotRollbackable)
	}

	// The journal must name one version of the runbook, and the runbook
	// must still be that version: every step is held to it, here and again
	// on the Runner, and a runbook changed since may no longer have the
	// nodes the journal names.
	version := entries[0].DAGVersion
	for _, e := range entries {
		if e.DAGVersion != version {
			return nil, &RollbackRefusedError{Problems: []RollbackProblem{{Reason: "the journal records more than one version of the runbook for this job, which no run writes"}}}
		}
	}
	dag, err := d.runbooks.GetDAG(ctx, job.RunbookID)
	if err != nil {
		return nil, fmt.Errorf("%w: the runbook it ran cannot be read: %w", ErrNotRollbackable, err)
	}
	if dag.Version != version {
		return nil, &RollbackRefusedError{Problems: []RollbackProblem{{Reason: fmt.Sprintf(
			"runbook %s has changed since this job ran, so the Controller cannot hold the journal to it; put it back as it was to roll the job back (adding or editing a rollback: list is not a change)", job.RunbookID)}}}
	}

	devices, since := journaledDevices(entries)
	later, err := d.rollbackJournal.OnDevicesSince(ctx, job.JobID, devices, since)
	if err != nil {
		return nil, err
	}
	names, err := d.deviceNames(ctx, devices)
	if err != nil {
		return nil, fmt.Errorf("resolve the devices job %s changed: %w", job.JobID, err)
	}

	plan, err := rollback.Build(rollback.Request{
		Target:  rollback.Run{ID: job.JobID, Entries: entries, Sealed: sealed(tasks)},
		Others:  groupByJob(later),
		Devices: func(id string) (string, bool) { name, ok := names[id]; return name, ok },
		Authored: func(node string) ([]engine.Task, error) {
			task, ok := dag.Nodes[node]
			if !ok {
				return nil, fmt.Errorf("runbook %s has no node %s", job.RunbookID, node)
			}
			return task.Rollback, nil
		},
		Leave: setOf(req.Leave), AllowPartial: setOf(req.AllowPartial),
		AllowUnknown: setOf(req.AllowUnknown), DespiteRuns: setOf(req.DespiteJobs),
	})
	var refused *rollback.RefusedError
	var problems []RollbackProblem
	switch {
	case errors.As(err, &refused):
		for _, p := range refused.Problems {
			problems = append(problems, problemFrom(p))
		}
	case err != nil:
		return nil, err
	}

	// An effect nobody can vouch for is a refusal here, where the Crawl
	// tier ends incomplete: a job that ran would read as a rollback done.
	for _, gap := range plan.Unknown {
		if !slices.Contains(req.AllowUnknown, gap.Node) {
			problems = append(problems, RollbackProblem{Node: gap.Node, Device: gap.DeviceName, Reason: gap.Reason, Field: "allow_unknown", Value: gap.Node})
		}
	}

	out := &dispatch.RollbackPlan{DAGVersion: version}
	for _, level := range plan.Levels {
		for _, s := range level {
			if s.DeviceID == "" {
				problems = append(problems, RollbackProblem{Node: s.Node, Reason: "a change made on no device cannot be undone through the Controller", Field: "leave", Value: s.Node})
				continue
			}
			if err := rollback.Verify(dag, s); err != nil {
				problems = append(problems, RollbackProblem{Node: s.Node, Device: s.DeviceName, Reason: err.Error()})
				continue
			}
			addStep(out, s)
		}
	}
	if len(problems) > 0 {
		return nil, &RollbackRefusedError{Problems: problems}
	}
	if len(out.Devices) == 0 {
		return nil, fmt.Errorf("%w: nothing is left to run, since every change is left in place as named", ErrNotRollbackable)
	}
	return out, nil
}

// addStep appends s to its device's steps in plan, in the order they run.
func addStep(plan *dispatch.RollbackPlan, s rollback.Step) {
	step := wire.RollbackStep{Node: s.Node, Index: s.Index, Emitter: s.Emitter, Method: s.FQCN, Params: s.Params, Name: s.Name, Source: s.Source}
	for i := range plan.Devices {
		if plan.Devices[i].DeviceID == s.DeviceID {
			plan.Devices[i].Steps = append(plan.Devices[i].Steps, step)
			return
		}
	}
	plan.Devices = append(plan.Devices, dispatch.RollbackDevice{DeviceID: s.DeviceID, DeviceName: s.DeviceName, Steps: []wire.RollbackStep{step}})
}

// journaledDevices returns the devices entries name, sorted, and when the
// earliest entry began.
func journaledDevices(entries []engine.JournalEntry) ([]string, time.Time) {
	seen := map[string]bool{}
	var since time.Time
	for _, e := range entries {
		if e.DeviceID != "" {
			seen[e.DeviceID] = true
		}
		if !e.StartedAt.IsZero() && (since.IsZero() || e.StartedAt.Before(since)) {
			since = e.StartedAt
		}
	}
	return slices.Sorted(maps.Keys(seen)), since
}

// groupByJob groups entries into the runs the planner reads, one per job.
func groupByJob(entries []engine.JournalEntry) []rollback.Run {
	byJob := map[string][]engine.JournalEntry{}
	for _, e := range entries {
		byJob[e.JobID] = append(byJob[e.JobID], e)
	}
	runs := make([]rollback.Run, 0, len(byJob))
	for _, id := range slices.Sorted(maps.Keys(byJob)) {
		runs = append(runs, rollback.Run{ID: id, Entries: byJob[id], Sealed: true})
	}
	return runs
}

// sealed reports whether a finished job's journal is complete: every
// device handed to a Runner has reported back. One that never did may
// have been cut off mid-level, and whatever that level did is not
// recorded.
func sealed(tasks []dispatch.JobTask) bool {
	for _, t := range tasks {
		if t.Outcome == dispatch.OutcomeDispatched && t.Result == dispatch.ResultPending {
			return false
		}
	}
	return true
}

// setOf turns a list of names into the planner's set.
func setOf(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// withoutSecretAnswers returns vars without any variable the template's
// survey marks secret: a password one operator typed for the undone job
// is not replayed by whoever rolls it back, the rule a relaunch keeps.
// A job with no template had no survey.
func withoutSecretAnswers(vars map[string]any, tmpl launch.Template) map[string]any {
	out := maps.Clone(vars)
	for _, name := range tmpl.Survey.SecretVariables() {
		delete(out, name)
	}
	return out
}

// rollbackBody is POST /jobs/{id}/rollback's body. Every field is
// optional, and an absent body asks for a real rollback that accepts
// nothing.
type rollbackBody struct {
	Mode         string   `json:"mode"`
	Leave        []string `json:"leave"`
	AllowPartial []string `json:"allow_partial"`
	AllowUnknown []string `json:"allow_unknown"`
	DespiteJob   []string `json:"despite_job"`
}

// rollbackRefusedDTO is a refused rollback's body: every problem, each
// with the field that accepts it.
type rollbackRefusedDTO struct {
	LinkSet

	Status   string            `json:"status"`
	Error    string            `json:"error"`
	Problems []RollbackProblem `json:"problems"`
}

// RollbackJob serves POST /jobs/{id}/rollback: undo what a job changed.
func (d *Dispatcher) RollbackJob(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	jobID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(jobID); err != nil {
		RespondError(w, r, http.StatusBadRequest, "job id must be a UUID")
		return
	}
	var body rollbackBody
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	mode := collection.ModeExecute
	switch body.Mode {
	case "", string(collection.ModeExecute):
	case string(collection.ModeCheck):
		mode = collection.ModeCheck
	default:
		RespondError(w, r, http.StatusBadRequest, "mode must be execute or check")
		return
	}

	newJobID, err := d.Rollback(r.Context(), identity.Subject, jobID, RollbackRequest{
		Mode: mode, Leave: body.Leave, AllowPartial: body.AllowPartial,
		AllowUnknown: body.AllowUnknown, DespiteJobs: body.DespiteJob,
	})
	var refused *RollbackRefusedError
	switch {
	case err == nil:
	case errors.As(err, &refused):
		Respond(w, r, http.StatusUnprocessableEntity, &rollbackRefusedDTO{Status: "refused", Error: refused.Error(), Problems: refused.Problems})
		return
	case errors.Is(err, dispatch.ErrJobNotFound):
		RespondError(w, r, http.StatusNotFound, "job not found")
		return
	case errors.Is(err, launch.ErrNotFound):
		RespondError(w, r, http.StatusUnprocessableEntity, "the template this job ran no longer exists")
		return
	case errors.Is(err, ErrNotRollbackable), errors.Is(err, journal.ErrTooMuchToPlan):
		RespondError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	default:
		loggerFrom(r).ErrorContext(r.Context(), "failed to roll back job",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/jobs/"+newJobID)
	Respond(w, r, http.StatusAccepted, &launchAcceptedDTO{Status: "accepted", JobID: newJobID, IgnoredFields: []ignoredFieldDTO{}})
}
