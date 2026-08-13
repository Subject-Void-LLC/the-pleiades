package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// Dispatcher launches an asynchronous runbook dispatch. It resolves the
// requested runbook, persists a pending Job, and publishes exactly one
// job.requested event, handing the actual per-device fan-out off to
// internal/dispatch.Worker entirely, rather than performing it inline
// inside the HTTP request the way this handler used to. See
// internal/dispatch's own package doc comment for the full shape this
// replaces (PLAN.md Section 28.4): a launch persists a Job with a stored
// selector and returns 202 Accepted immediately, and a durable worker
// performs the actual fan-out later, off the HTTP request path entirely.
type Dispatcher struct {
	// runbooks resolves a runbook id to its compiled Runbook, used here
	// only to answer "does this id name a real runbook" before a Job is
	// ever persisted for it; the compiled capability requirements
	// themselves are read again, independently, by internal/dispatch.Worker
	// at fan-out time, since a runbook file can change between a job's
	// launch and its (possibly much later) fan-out.
	runbooks runbook.Source
	// jobs persists the Job row this launch creates. It is the same
	// dispatch.JobStore port internal/dispatch.Worker depends on, so a
	// job this handler creates is immediately visible to the Worker that
	// will claim its fan-out.
	jobs dispatch.JobStore
	// bus is where the single job.requested event is published, handing
	// fan-out off to internal/dispatch.Worker's own bus.Subscribe.
	bus event.Bus
	// templates resolves the saved definition a launch names.
	//
	// A narrow read-only port rather than the whole launch.Store, matching
	// the Interface Segregation precedent JobRepository sets in this
	// package: a dispatcher that took the full store would look like it
	// administers templates, and would gain the ability to delete one as a
	// side effect of being able to run one.
	templates TemplateReader

	// configs records what each launch was configured with, and reads one
	// back when a job is relaunched.
	configs LaunchConfigStore
}

// TemplateReader is the slice of internal/launch's store a launch needs.
type TemplateReader interface {
	Get(ctx context.Context, id int) (launch.Template, error)
}

// LaunchConfigStore is the second, separate slice: recording what a launch
// supplied, and reading it back to repeat it.
//
// Separate from TemplateReader rather than folded into it, because the two
// carry different authority. Reading a template is what running one
// requires; writing a configuration against a template is a durable side
// effect of a launch. Keeping them apart is what lets the read port stay
// honestly read-only, and lets a test supply one without the other.
type LaunchConfigStore interface {
	SaveConfig(ctx context.Context, cfg launch.SavedConfig) (launch.SavedConfig, error)
	GetConfig(ctx context.Context, id int) (launch.SavedConfig, error)
}

// ErrNotRelaunchable is returned when a job cannot be repeated as it ran.
//
// Distinct from a missing job and from a store failure, because it is
// neither: the job is right there, and the platform is working. What is
// missing is something a relaunch structurally needs, and every case of it
// is one a caller can act on by launching the template directly with fresh
// input. The wrapped message says which case it was.
var ErrNotRelaunchable = errors.New("api: this job cannot be relaunched")

// NewDispatcher creates a new task dispatcher over runbooks, jobs, and
// bus.
//
// This constructor used to also take an inventory.Repository, so the
// handler itself could stream the target group and publish one event per
// device inline inside the HTTP request. That loop is gone:
// internal/dispatch.Worker now owns streaming the group, admitting or
// skipping each device (lifecycle and capability checks alike), and
// publishing the per-device wire.DispatchPayload, entirely off the HTTP
// request path; this handler has no remaining use for
// inventory.Repository at all, so, mirroring devices.go's own
// DeviceRepository precedent of depending on only the methods a handler
// actually calls, it is dropped rather than kept unused.
//
// This constructor also used to take an auth.Evaluator, so a per-device
// loop here could re-check "runbook:execute" (a defense-in-depth
// capability check) on every iteration. Phase 12 already removed that
// (FAILURE_PATTERNS.md #66: the check was an invariant identical for
// every device on every call, one boundary-level check away from being
// redundant with api.RequireScope, not real defense in depth), and there
// is now no per-device loop here at all left for such a check to guard;
// lifecycle and capability admission for each device happen inside
// internal/dispatch.Worker instead, alongside the rest of the fan-out
// logic they gate.
//
// bus is the event.Bus port, not a raw jetstream.JetStream: this is the
// fix for a real, previously silent production bug. DispatchRunbook used
// to publish straight to a bare "runbooks.dispatch" literal via
// jetstream.JetStream.PublishMsg, a subject no stream's configured filter
// ever covered (FAILURE_PATTERNS.md #17's bug family), so every dispatch
// failed with its error swallowed into errCount. Publishing through Bus
// and topology.JobRequestedSubject keeps that fix intact for the one
// event this handler now publishes.
func NewDispatcher(runbooks runbook.Source, jobs dispatch.JobStore, bus event.Bus, opts ...DispatcherOption) *Dispatcher {
	d := &Dispatcher{
		runbooks: runbooks,
		jobs:     jobs,
		bus:      bus,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DispatcherOption configures optional collaborators.
type DispatcherOption func(*Dispatcher)

// WithTemplates supplies the port LaunchTemplate reads from.
//
// Optional rather than positional so the several existing harnesses that
// build a Dispatcher keep compiling. It is a convenience, not a permission:
// LaunchTemplate refuses outright when it is absent, rather than falling
// back to a launch that names no template and therefore no tenant.
func WithTemplates(templates TemplateReader) DispatcherOption {
	return func(d *Dispatcher) { d.templates = templates }
}

// WithLaunchConfigs supplies the store a launch records its configuration
// in, and a relaunch reads back.
//
// cmd/controller passes the same concrete launch.Store it passes to
// WithTemplates. They are two options rather than one because they are two
// authorities, and a caller that wired only the first gets told so at the
// first launch that supplies anything, rather than silently launching jobs
// that can never be relaunched.
func WithLaunchConfigs(configs LaunchConfigStore) DispatcherOption {
	return func(d *Dispatcher) { d.configs = configs }
}

// jobRequestedPayload is the small event body this handler publishes to
// topology.JobRequestedSubject: just enough for a Worker to look the job
// back up. Everything else about the job (RunbookID, GroupName, Actor)
// already lives in the Job row itself, so the event does not need to
// duplicate it. This is the publisher-side half of the identical wire
// shape internal/dispatch.Worker's own unexported jobRequestedPayload
// decodes on the receiving end; the two must stay in sync by hand, since
// this package cannot import internal/dispatch's unexported type and
// pkg/wire does not yet hold this particular payload (only
// wire.DispatchPayload, moved there in this same phase).
type jobRequestedPayload struct {
	// JobID identifies the job to fan out.
	JobID string `json:"job_id"`
}

// launchRequestDTO is the body a launch accepts.
//
// Every field is optional, so launching a template that opens nothing is
// POST with no body at all. Sparse in the same way launch.Config is: an
// absent key inherits what the template decided, which is a different
// instruction from a key present with an empty value.
type launchRequestDTO struct {
	// Config names a stored launch configuration to launch from. It is
	// applied beneath Overrides, so an operator standing at the form still
	// beats a configuration saved months ago.
	Config int `json:"config"`

	// Overrides are this launch's own values, keyed by launch field name.
	// Every one the template did not open is reported rather than applied.
	Overrides map[string]any `json:"overrides"`

	// Answers are this launch's survey answers, keyed by variable.
	Answers map[string]any `json:"answers"`
}

// ignoredFieldDTO is one value a caller supplied that was not applied.
type ignoredFieldDTO struct {
	Name   string `json:"name"`
	Layer  string `json:"layer"`
	Reason string `json:"reason"`
}

// launchAcceptedDTO is the body a successful launch returns.
//
// IgnoredFields is always present, never omitempty, and that is the whole
// contract this response exists to keep. A launch that silently applied an
// unopened override would be a privilege escalation and one that silently
// dropped it would be a lie about what ran, so the third option is to say
// so; a key that vanished when the list was empty would make "nothing was
// refused" indistinguishable from "this server does not report refusals".
type launchAcceptedDTO struct {
	LinkSet

	Status        string            `json:"status"`
	JobID         string            `json:"job_id"`
	IgnoredFields []ignoredFieldDTO `json:"ignored_fields"`
}

func toIgnoredDTOs(ignored []launch.IgnoredField) []ignoredFieldDTO {
	out := make([]ignoredFieldDTO, 0, len(ignored))
	for _, f := range ignored {
		out = append(out, ignoredFieldDTO{Name: f.Name, Layer: f.Layer, Reason: f.Reason})
	}
	return out
}

// LaunchFromTemplate serves POST /templates/{id}/launch: run a saved
// definition, with whatever of it this caller is permitted to vary.
//
// It answers 202 with the ignored fields rather than 400, and that is the
// decision the whole endpoint is shaped around. A locked field is the
// template author's decision, not the launching operator's mistake:
// refusing the launch would make one person's policy look like another
// person's error, and would leave an operator unable to run a template at
// all because their client sent a field they were never allowed to set.
func (d *Dispatcher) LaunchFromTemplate(w http.ResponseWriter, r *http.Request) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	templateID, ok := parseTemplateID(w, r)
	if !ok {
		return
	}

	var body launchRequestDTO
	if r.ContentLength > 0 && !decodeJSON(w, r, &body) {
		return
	}

	cfg := launch.Config{Overrides: launch.Fields(body.Overrides), Answers: body.Answers}
	if body.Config > 0 {
		saved, err := d.savedConfigFor(r.Context(), templateID, body.Config)
		if err != nil {
			d.respondLaunchError(w, r, templateID, err)
			return
		}
		cfg.Saved = saved.Fields
		// The stored answers sit beneath this launch's own, so a
		// configuration can carry the routine answers and the operator
		// still supplies or replaces whichever ones they mean to.
		cfg.Answers = mergeAnswers(saved.Answers, body.Answers)
	}

	jobID, ignored, err := d.LaunchTemplate(r.Context(), identity.Subject, templateID, cfg)
	if err != nil {
		d.respondLaunchError(w, r, templateID, err)
		return
	}

	// Location must be set before Respond, which calls WriteHeader.
	w.Header().Set("Location", APIVersionPrefix+"/jobs/"+jobID)
	resp := launchAcceptedDTO{Status: "accepted", JobID: jobID, IgnoredFields: toIgnoredDTOs(ignored)}
	Respond(w, r, http.StatusAccepted, &resp)
}

// RelaunchJob serves POST /jobs/{id}/relaunch: run a job's template again,
// with the configuration that job ran with.
func (d *Dispatcher) RelaunchJob(w http.ResponseWriter, r *http.Request) {
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

	newJobID, ignored, err := d.Relaunch(r.Context(), identity.Subject, jobID)
	if err != nil {
		switch {
		case errors.Is(err, dispatch.ErrJobNotFound):
			RespondError(w, r, http.StatusNotFound, "job not found")
		case errors.Is(err, launch.ErrNotFound):
			// The job is there; the template it ran is not. A 404 here
			// would name the wrong resource: the one the caller asked for
			// exists.
			RespondError(w, r, http.StatusUnprocessableEntity, "the template this job ran no longer exists")
		case errors.Is(err, ErrNotRelaunchable):
			RespondError(w, r, http.StatusUnprocessableEntity, err.Error())
		default:
			loggerFrom(r).ErrorContext(r.Context(), "failed to relaunch job",
				slog.String("job_id", jobID),
				slog.String("error", err.Error()))
			RespondError(w, r, http.StatusInternalServerError, "internal error")
		}
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/jobs/"+newJobID)
	resp := launchAcceptedDTO{Status: "accepted", JobID: newJobID, IgnoredFields: toIgnoredDTOs(ignored)}
	Respond(w, r, http.StatusAccepted, &resp)
}

// savedConfigFor loads a stored configuration and refuses one belonging to
// a different template.
//
// The check is not bookkeeping. A configuration is only meaningful against
// the prompts its own template declared, so applying one template's saved
// bundle to another would carry survey answers into a template whose author
// never wrote those questions, across a tenancy boundary if the two
// templates belong to different organizations.
func (d *Dispatcher) savedConfigFor(ctx context.Context, templateID, configID int) (launch.SavedConfig, error) {
	if d.configs == nil {
		return launch.SavedConfig{}, fmt.Errorf("launching from a saved configuration is not wired on this controller")
	}

	stored, err := d.configs.GetConfig(ctx, configID)
	if err != nil {
		return launch.SavedConfig{}, fmt.Errorf("resolve saved configuration %d: %w", configID, err)
	}
	if stored.TemplateID != templateID {
		return launch.SavedConfig{}, fmt.Errorf("%w: saved configuration %d belongs to another template",
			launch.ErrNotFound, configID)
	}
	return stored, nil
}

// mergeAnswers layers this launch's answers over a stored configuration's.
func mergeAnswers(saved, supplied map[string]any) map[string]any {
	if len(saved) == 0 {
		return supplied
	}
	merged := make(map[string]any, len(saved)+len(supplied))
	for name, value := range saved {
		merged[name] = value
	}
	for name, value := range supplied {
		merged[name] = value
	}
	return merged
}

// respondLaunchError maps a launch failure onto the status it deserves.
//
// A survey answer that violates its own schema is 422 rather than 400: the
// request is well formed and the caller is authorized, the value is simply
// not one the template's author said was acceptable. It is also the one
// launch failure whose message is written for a person and names nothing
// internal, which is why it is the one that crosses the boundary intact.
func (d *Dispatcher) respondLaunchError(w http.ResponseWriter, r *http.Request, templateID int, err error) {
	switch {
	case errors.Is(err, launch.ErrNotFound):
		RespondError(w, r, http.StatusNotFound, "template not found")
	case errors.Is(err, launch.ErrSurveyAnswer):
		RespondError(w, r, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, launch.ErrUnknownKind):
		// The template names a kind this Controller no longer registers,
		// which is a deployment fact rather than a caller's mistake, so it
		// says so instead of reporting the template as malformed.
		RespondError(w, r, http.StatusUnprocessableEntity, "this controller cannot run that kind of template")
	default:
		loggerFrom(r).ErrorContext(r.Context(), "failed to launch template",
			slog.Int("template_id", templateID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}

func (d *Dispatcher) LaunchTemplate(ctx context.Context, actor string, templateID int, cfg launch.Config) (string, []launch.IgnoredField, error) {
	if d.templates == nil {
		return "", nil, fmt.Errorf("launching by template is not wired on this controller")
	}

	// 1. Load the template and fold the caller's configuration over it.
	// Resolve is what decides what actually runs: it applies the fields
	// this template opened, reports every value it did not, and refuses
	// only a survey answer that violates its own schema.
	tmpl, err := d.templates.Get(ctx, templateID)
	if err != nil {
		return "", nil, fmt.Errorf("resolve template %d: %w", templateID, err)
	}

	resolved, ignored, err := tmpl.Resolve(ctx, cfg)
	if err != nil {
		return "", ignored, fmt.Errorf("resolve template %d: %w", templateID, err)
	}

	// 2. Record what this launch was configured with, before the job, so
	// the job can point at it. A launch that supplied nothing records
	// nothing: there is no configuration to repeat, and a relaunch of such
	// a job correctly re-runs the template's own defaults.
	configID, err := d.recordConfig(ctx, tmpl.ID, cfg)
	if err != nil {
		return "", ignored, err
	}

	// 3. Mint a server-generated job id and persist the Job row. This
	// happens before anything is published: a durable record must exist
	// before a Worker could ever be told to look one up, so a crash
	// between these two steps leaves, at worst, a Job stuck in "pending"
	// with nothing having fanned out yet, never a job.requested event
	// naming a Job that was never actually saved.
	//
	// This is also where a job finally gets a tenant. OrganizationID comes
	// off the template, which derived it from its inventory's required
	// organization edge; before Phase 21 a dispatch named a free-text
	// group, which has no tenant to inherit, so Job.organization_id had
	// existed since Phase 14 with nothing ever writing it.
	jobID := uuid.New().String()
	job := &dispatch.Job{
		JobID:          jobID,
		RunbookID:      resolved.Definition,
		Actor:          actor,
		Kind:           resolved.Kind,
		InventoryID:    resolved.InventoryID,
		OrganizationID: resolved.OrganizationID,
		TemplateID:     tmpl.ID,
		TemplateName:   tmpl.Name,
		LaunchConfigID: configID,
		// Captured on the record now, ahead of the wire and either
		// adapter actually reading them back: see dispatch.Job.Fields'
		// own doc comment for what still has to be built before these
		// values reach a real ansible-playbook invocation or a native
		// runbook's variable context.
		Fields:    resolved.Fields,
		ExtraVars: resolved.ExtraVars,
	}
	if err := d.jobs.Create(ctx, job); err != nil {
		return "", ignored, fmt.Errorf("create job %s: %w", jobID, err)
	}

	if err := d.publishRequested(ctx, actor, jobID); err != nil {
		return "", ignored, err
	}

	return jobID, ignored, nil
}

// recordConfig stores what a launch supplied, returning the id a job
// records, or zero when there was nothing to store.
//
// Stored before the job rather than after, and that ordering is the whole
// point: the job carries the id, so a configuration written afterwards
// would need the job updated, and a crash between the two would leave a job
// claiming to have been launched with nothing. Failing here fails the
// launch, having persisted at most an orphaned configuration row that
// nothing points at, which is the harmless direction.
//
// A caller who supplied something on a Dispatcher with no configuration
// store is refused rather than launched. The alternative is a job that ran
// with overrides and has no record of them, which reads as a job launched
// with the template's defaults: a lie about what ran, told to whoever reads
// it next.
func (d *Dispatcher) recordConfig(ctx context.Context, templateID int, cfg launch.Config) (int, error) {
	if len(cfg.Saved) == 0 && len(cfg.Overrides) == 0 && len(cfg.Answers) == 0 {
		return 0, nil
	}
	if d.configs == nil {
		return 0, fmt.Errorf("recording the configuration of a launch is not wired on this controller")
	}

	// Saved and Overrides collapse into one stored bundle, overrides last.
	// What is recorded is what this launch ran with, not which layer each
	// value arrived from: a relaunch repeats the run, and reconstructing
	// the layering would give a value a precedence it did not have the
	// first time.
	fields := launch.Fields{}
	for _, layer := range []launch.Fields{cfg.Saved, cfg.Overrides} {
		for name, value := range layer {
			fields[name] = value
		}
	}

	stored, err := d.configs.SaveConfig(ctx, launch.SavedConfig{
		TemplateID: templateID,
		Fields:     fields,
		Answers:    cfg.Answers,
	})
	if err != nil {
		return 0, fmt.Errorf("record the configuration of a launch of template %d: %w", templateID, err)
	}
	return stored.ID, nil
}

// Relaunch runs a job's template again, with the configuration that job
// ran with.
//
// It resolves everything from the job rather than from the caller, which is
// what makes it a repeat rather than a new launch that happens to look
// similar. The actor is the caller's own: a relaunch is a new decision by
// whoever made it, and attributing it to whoever launched the original
// would put somebody else's name on a dispatch they did not ask for.
func (d *Dispatcher) Relaunch(ctx context.Context, actor, jobID string) (string, []launch.IgnoredField, error) {
	job, _, err := d.jobs.Get(ctx, jobID)
	if err != nil {
		return "", nil, fmt.Errorf("load job %s: %w", jobID, err)
	}

	if job.TemplateID == 0 {
		// A pre-Phase-21 job, launched by naming a group and a runbook.
		// There is no template to run again, and inventing one would mean
		// guessing which saved definition somebody meant.
		return "", nil, fmt.Errorf("%w: it was not launched from a template", ErrNotRelaunchable)
	}
	if d.templates == nil {
		return "", nil, fmt.Errorf("launching by template is not wired on this controller")
	}

	tmpl, err := d.templates.Get(ctx, job.TemplateID)
	if err != nil {
		return "", nil, fmt.Errorf("resolve template %d: %w", job.TemplateID, err)
	}

	cfg, err := d.configFor(ctx, job, tmpl)
	if err != nil {
		return "", nil, err
	}

	return d.LaunchTemplate(ctx, actor, job.TemplateID, cfg)
}

// configFor reads back the configuration a job ran with, refusing the two
// cases where repeating it would not repeat the job.
func (d *Dispatcher) configFor(ctx context.Context, job *dispatch.Job, tmpl launch.Template) (launch.Config, error) {
	if job.LaunchConfigID == 0 {
		// Nothing was supplied, so the template's own defaults are exactly
		// what ran.
		return launch.Config{}, nil
	}
	if d.configs == nil {
		return launch.Config{}, fmt.Errorf("reading the configuration of a launch is not wired on this controller")
	}

	stored, err := d.configs.GetConfig(ctx, job.LaunchConfigID)
	if err != nil {
		if errors.Is(err, launch.ErrNotFound) {
			// The template was edited or replaced and took its saved
			// configurations with it. Launching without them would run
			// something other than the job being repeated, so this is
			// reported rather than silently downgraded to a plain launch.
			return launch.Config{}, fmt.Errorf("%w: the configuration it ran with no longer exists", ErrNotRelaunchable)
		}
		return launch.Config{}, fmt.Errorf("read the configuration of job %s: %w", job.JobID, err)
	}

	// A secret answer is not reused, and this is the security decision in
	// this file. A survey password is a value one operator typed at one
	// moment; replaying it would let anybody who can relaunch cause a
	// secret they have never seen, and may not be entitled to, to be used
	// again under their own name. AWX prompts for it again, and refusing
	// here is the same answer in the medium available: an API cannot
	// prompt, so it says what is missing and points at the launch endpoint.
	for _, name := range tmpl.Survey.SecretVariables() {
		if _, answered := stored.Answers[name]; answered {
			return launch.Config{}, fmt.Errorf("%w: it answered %q, which this platform will not replay; launch the template with a fresh answer",
				ErrNotRelaunchable, name)
		}
	}

	return stored.Config(), nil
}

// publishRequested publishes the single job.requested event that hands
// fan-out off to internal/dispatch.Worker.
//
// Shared by both launch entry points, because the ordering and the context
// handling here are the load-bearing part and two copies would drift on
// exactly the details that matter: the durable record exists before the
// event, the publish survives a client disconnecting, and the job id is the
// idempotency key.
func (d *Dispatcher) publishRequested(ctx context.Context, actor, jobID string) error {
	evt, err := event.WrapPayload(uuid.New().String(), "job.requested", jobRequestedPayload{JobID: jobID})
	if err != nil {
		return fmt.Errorf("build job.requested event for %s: %w", jobID, err)
	}

	// Background context for publishing: a client disconnecting
	// mid-request should not cancel a launch that has already been
	// durably persisted. Actor and TraceID are stamped explicitly onto
	// that background context rather than inherited through
	// cancellation, so the published envelope still carries who and which
	// trace triggered it.
	pubCtx := event.WithActor(context.Background(), actor)
	if traceID, ok := TraceIDFromContext(ctx); ok {
		pubCtx = event.WithTraceID(pubCtx, traceID)
	}
	// The envelope's TraceID field is for a human reading an audit row.
	// The machine-readable W3C trace context that actually lets
	// internal/dispatch.Worker (and, further downstream, the Runner)
	// continue this trace rides in the NATS message headers, injected by
	// the Bus adapter, and needs the live span from the caller's context
	// rather than the detached background one, so it is grafted back on
	// here.
	pubCtx = trace.ContextWithSpan(pubCtx, trace.SpanFromContext(ctx))
	// One job.requested event per job, so the job id alone is a natural,
	// stable idempotency key for a retry of this exact publish.
	pubCtx = event.WithIdempotencyKey(pubCtx, jobID)

	if err := d.bus.Publish(pubCtx, topology.JobRequestedSubject(), *evt); err != nil {
		return fmt.Errorf("publish job.requested for %s: %w", jobID, err)
	}

	return nil
}
