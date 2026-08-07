package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/dispatch"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runbook"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
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
}

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
func NewDispatcher(runbooks runbook.Source, jobs dispatch.JobStore, bus event.Bus) *Dispatcher {
	return &Dispatcher{
		runbooks: runbooks,
		jobs:     jobs,
		bus:      bus,
	}
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

// jobAcceptedResponse is the body a successful dispatch launch returns.
// It carries no per-device counts, since fan-out has not happened yet by
// the time this response is written; a caller polls GET /jobs/{id}
// (jobs.go) for progress and final tallies.
type jobAcceptedResponse struct {
	LinkSet

	Status string `json:"status"`
	JobID  string `json:"job_id"`
}

// DispatchRunbook launches an asynchronous runbook dispatch: it validates
// the request, resolves the runbook, persists a pending Job, and
// publishes one job.requested event. It responds 202 Accepted with a
// Location header naming the new job resource; the actual per-device
// fan-out happens later, in internal/dispatch.Worker, off this request
// entirely.
//
// Authorization (the caller holds auth.ScopeRunbookExecute) is enforced
// once, at the router boundary, by api.RequireScope before this handler
// ever runs; the identity is read from context here only to stamp the
// actor onto the persisted Job and the published event, not to authorize
// anything.
func (d *Dispatcher) DispatchRunbook(w http.ResponseWriter, r *http.Request) {
	// 1. Extract Identity from Context. Its absence here means the
	// request reached this handler with no middleware in front of it at
	// all (a direct unit-test call, or a second router mounting this
	// handler unguarded, PATTERNS.md's Front Controller entry's whole
	// concern) rather than an authorization failure, which RequireScope
	// already turned into a 403 upstream.
	id, ok := IdentityFromContext(r.Context())
	if !ok {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	groupName := r.URL.Query().Get("group")
	runbookID := r.URL.Query().Get("runbook")

	if groupName == "" || runbookID == "" {
		RespondError(w, r, http.StatusBadRequest, "missing 'group' or 'runbook' query parameter")
		return
	}

	// 2. Resolve the runbook before anything is persisted. A missing
	// runbook is a 404 carrying a generic message, never the underlying
	// error: runbook.Source.Get's own doc comment notes id is
	// caller-controlled and an implementation's error can carry a
	// filesystem path or other storage-layer detail that has no business
	// reaching an HTTP response.
	if _, err := d.runbooks.Get(r.Context(), runbookID); err != nil {
		if errors.Is(err, runbook.ErrNotFound) {
			RespondError(w, r, http.StatusNotFound, "runbook not found")
			return
		}
		loggerFrom(r).ErrorContext(r.Context(), "failed to resolve runbook for dispatch",
			slog.String("runbook_id", runbookID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// 3. Mint a server-generated job id and persist the Job row. This
	// happens before anything is published: a durable record must exist
	// before a Worker could ever be told to look one up, so a crash
	// between these two steps leaves, at worst, a Job stuck in "pending"
	// with nothing having fanned out yet, never a job.requested event
	// naming a Job that was never actually saved.
	jobID := uuid.New().String()
	job := &dispatch.Job{
		JobID:     jobID,
		RunbookID: runbookID,
		GroupName: groupName,
		Actor:     id.Subject,
	}
	if err := d.jobs.Create(r.Context(), job); err != nil {
		loggerFrom(r).ErrorContext(r.Context(), "failed to create job",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// 4. Publish exactly one job.requested event, handing fan-out off to
	// internal/dispatch.Worker.
	evt, err := event.WrapPayload(uuid.New().String(), "job.requested", jobRequestedPayload{JobID: jobID})
	if err != nil {
		loggerFrom(r).ErrorContext(r.Context(), "failed to build job.requested event",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// Background context for publishing, same reasoning this handler's
	// own per-device loop already had before this phase: an HTTP client
	// disconnecting mid-request should not cancel a launch that has
	// already been durably persisted. Actor and TraceID are stamped
	// explicitly from the request onto that background context, rather
	// than inherited via cancellation, so the published envelope still
	// carries who and which trace triggered it.
	pubCtx := event.WithActor(context.Background(), id.Subject)
	if traceID, ok := TraceIDFromContext(r.Context()); ok {
		pubCtx = event.WithTraceID(pubCtx, traceID)
	}
	// The envelope's TraceID field is for a human reading an audit row.
	// The machine-readable W3C trace context that actually lets
	// internal/dispatch.Worker (and, further downstream, the Runner)
	// continue this trace rides in the NATS message headers, injected by
	// the Bus adapter, and needs the live span from the request context
	// rather than the detached background one, so it is grafted back on
	// here.
	pubCtx = trace.ContextWithSpan(pubCtx, trace.SpanFromContext(r.Context()))
	// One job.requested event per job, so the job id alone is a natural,
	// stable idempotency key for a retry of this exact publish.
	pubCtx = event.WithIdempotencyKey(pubCtx, jobID)

	if err := d.bus.Publish(pubCtx, topology.JobRequestedSubject(), *evt); err != nil {
		loggerFrom(r).ErrorContext(r.Context(), "failed to publish job.requested event",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// Location must be set on w BEFORE Respond is called: Respond calls
	// w.WriteHeader internally, and a header set after WriteHeader has
	// already been called is silently dropped by net/http.
	w.Header().Set("Location", APIVersionPrefix+"/jobs/"+jobID)

	resp := jobAcceptedResponse{
		Status: "accepted",
		JobID:  jobID,
	}
	Respond(w, r, http.StatusAccepted, &resp)
}
