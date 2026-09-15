// This file exposes the Job resource GET /api/v1/jobs/{id} owns: the
// asynchronous dispatch launch record internal/api/dispatcher.go creates
// and internal/dispatch.Worker fans out in the background. It is the
// client-facing read side of Phase 14's own asynchronous dispatch shape:
// a launch returns 202 Accepted immediately (dispatcher.go), and a caller
// polls this resource for progress and the final per-device tallies.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// JobRepository is the narrow slice of dispatch.JobStore the job handlers
// need: read-only access to a job and its per-device task outcomes.
//
// This mirrors devices.go's own DeviceRepository precedent exactly:
// depending on the one method actually called, rather than the whole
// dispatch.JobStore port (which also carries Create, BeginFanOut,
// RecordTask, Complete, and Fail, none of which a read-only HTTP handler
// ever needs), is the Interface Segregation shape this package already
// uses, and it is what lets a test supply a small double instead of a
// full store.
type JobRepository interface {
	// Get returns the job identified by jobID together with every
	// JobTask recorded against it so far, or an error satisfying
	// errors.Is(err, dispatch.ErrJobNotFound) if no such job exists.
	Get(ctx context.Context, jobID string) (*dispatch.Job, []dispatch.JobTask, error)

	// List returns up to limit jobs, newest first, resuming after the
	// given opaque cursor. It carries no JobTask rows: a list view does
	// not display per-device outcomes.
	List(ctx context.Context, after string, limit int) ([]*dispatch.Job, error)
}

// JobCanceler is the one write a job handler performs, kept separate from
// JobRepository above rather than widening it.
//
// The split is the point: every other job handler is read-only, and a
// reader that happened to hold this interface could stop a run. It also
// keeps the read handlers testable with a double that cannot write, which
// is the same Interface Segregation shape JobRepository's own doc comment
// describes.
type JobCanceler interface {
	// Cancel stops jobID on behalf of canceledBy, returning an error
	// satisfying errors.Is(err, dispatch.ErrJobNotFound) if no such job
	// exists, or dispatch.ErrNotCancelable if it has already finished.
	Cancel(ctx context.Context, jobID string, canceledBy string) error
}

// defaultJobListLimit and maxJobListLimit bound a job list page, matching
// the device list's own bounds and existing for the same reason: the cap
// is the server's, so ?limit=100000 is not a supported way to ask it to
// hold the entire job history in memory.
const (
	defaultJobListLimit = 50
	maxJobListLimit     = 200
)

// JobHandler serves the Job resource.
type JobHandler struct {
	jobs JobRepository
	// canceler may be nil on a Controller that wires no cancellation.
	// Cancel answers 501 in that case rather than dereferencing it, the
	// same way every other optional collaborator in this package is
	// treated.
	canceler JobCanceler
}

// NewJobHandler builds the Job resource's handlers over jobs, with
// canceler supplying the one write. Pass a nil canceler to mount the
// read-only handlers alone.
func NewJobHandler(jobs JobRepository, canceler JobCanceler) *JobHandler {
	return &JobHandler{jobs: jobs, canceler: canceler}
}

// jobTaskDTO is the wire projection of one dispatch.JobTask: a single
// device's outcome within a job's fan-out.
type jobTaskDTO struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Outcome    string `json:"outcome"`

	// Reason explains a skipped or failed outcome. It carries omitempty
	// deliberately: it is legitimately absent for a dispatched outcome,
	// since dispatch.JobTask.Reason is never populated for a device that
	// was successfully handed off, and a client should be able to tell
	// "no reason recorded" apart from "recorded as the empty string"
	// without special-casing the dispatched case itself.
	Reason string `json:"reason,omitempty"`
}

// jobResponse is the wire projection of a dispatch.Job together with
// every JobTask recorded against it so far.
type jobResponse struct {
	LinkSet

	JobID     string `json:"job_id"`
	RunbookID string `json:"runbook_id"`
	State     string `json:"state"`

	// Template, TemplateName, Inventory and Organization are what this job
	// was launched from and whose it is, captured at launch rather than
	// resolved live: a job is a historical record, and a rename or a
	// deletion afterwards must not rewrite what it says it ran.
	//
	// They replace the group name a job used to carry. A free-text group
	// had no tenant, so a job launched that way belonged to no
	// organization and named no saved definition, which is the state Phase
	// 21 exists to end.
	Template     int    `json:"template,omitempty"`
	TemplateName string `json:"template_name,omitempty"`
	Inventory    int    `json:"inventory,omitempty"`
	Organization int    `json:"organization,omitempty"`

	// Kind is which registered launch kind ran, and therefore which
	// execution adapter handled it.
	Kind string `json:"kind,omitempty"`

	// FailureReason explains a failed state, and is empty for every other
	// one. It carries only facts a job-resource reader may see, never a
	// raw internal error.
	FailureReason string `json:"failure_reason,omitempty"`

	// Dispatched, Skipped, and Failed are the terminal per-device
	// tallies (dispatch.Job's own DispatchedCount/SkippedCount/
	// FailedCount field comments): they read 0 before State reaches
	// "completed", regardless of how much fan-out work has actually
	// happened. Tasks below is where a caller reads live progress
	// instead.
	Dispatched int `json:"dispatched"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`

	Tasks []jobTaskDTO `json:"tasks"`
}

// toJobResponse projects a hydrated job and its tasks onto the wire
// shape.
//
// Tasks is initialized rather than left nil, so the JSON carries []
// instead of null for a job with no tasks recorded yet (every job passes
// through exactly that state between being created and its Worker
// claiming fan-out), mirroring toDeviceDTO's identical treatment of Tags
// in devices.go.
func toJobResponse(job *dispatch.Job, tasks []dispatch.JobTask) jobResponse {
	dtos := make([]jobTaskDTO, 0, len(tasks))
	for _, t := range tasks {
		dtos = append(dtos, jobTaskDTO{
			DeviceID:   t.DeviceID,
			DeviceName: t.DeviceName,
			Outcome:    t.Outcome.String(),
			Reason:     t.Reason,
		})
	}

	return jobResponse{
		JobID:         job.JobID,
		RunbookID:     job.RunbookID,
		State:         job.State,
		Template:      job.TemplateID,
		TemplateName:  job.TemplateName,
		Inventory:     job.InventoryID,
		Organization:  job.OrganizationID,
		Kind:          job.Kind,
		FailureReason: job.FailureReason,
		Dispatched:    job.DispatchedCount,
		Skipped:       job.SkippedCount,
		Failed:        job.FailedCount,
		Tasks:         dtos,
	}
}

// Get serves a single job by id.
//
// The {id} URL parameter is validated as a UUID before it is used for
// anything, the identical idiom and reasoning internal/api/logs.go's own
// StreamLogs already uses for its own {id} param: every job id this
// platform mints is a UUID (api.Dispatcher's own uuid.New()), so requiring
// one closes the same class of hole a caller-controlled id reaching a
// backing lookup could otherwise open, with no loss of function, and a
// second, independently invented validation idiom for the identical
// {id} pattern would be exactly the drift this codebase's own
// single-owner conventions (e.g. internal/topology's own doc comment)
// exist to prevent.
func (h *JobHandler) Get(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(jobID); err != nil {
		RespondError(w, r, http.StatusBadRequest, "job id must be a UUID")
		return
	}

	job, tasks, err := h.jobs.Get(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, dispatch.ErrJobNotFound) {
			RespondError(w, r, http.StatusNotFound, "job not found")
			return
		}
		// The store's own error text is logged, not returned: it can name
		// tables and columns, which an authenticated caller holding only
		// job:read has no business reading, the identical posture
		// devices.go's writeRepositoryError already takes for
		// inventory.Repository failures.
		loggerFrom(r).ErrorContext(r.Context(), "failed to load job",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := toJobResponse(job, tasks)
	Respond(w, r, http.StatusOK, &dto)
}

// jobTerminalStates are the states a job can no longer be stopped from.
//
// Listed positively, matching internal/dispatch's own Cancel guard and
// internal/ui/resources/jobs's terminalStates map, and for the same
// reason: a state added later is not terminal until somebody decides it
// is. A negative list would quietly make every future state terminal,
// which is the direction that fails silently.
var jobTerminalStates = map[string]bool{
	"completed": true,
	"failed":    true,
	"canceled":  true,
}

// AllowsRel implements LinkFilter, withdrawing the cancel affordance from
// a job that has already finished.
//
// Without this the _links array would advertise cancel on every job any
// runbook:execute caller can see, including ones that finished last week,
// and a client following it would get a 409. The relation is the key: a
// filter that ignored it would withdraw every affordance and leave the
// payload looking as though the caller could do nothing at all.
func (j jobResponse) AllowsRel(rel auth.LinkRel) bool {
	if rel != auth.RelCancel {
		return true
	}
	return !jobTerminalStates[j.State]
}

// Cancel stops a job that is still running.
//
// It answers 202 rather than 200, and the distinction is not ceremony.
// What this call settles synchronously is the record and the fan-out: the
// job is canceled, and no device it has not already reached will be
// dispatched to. Work already running on a device is signalled separately
// and best-effort, so "accepted" is the honest word for what happened.
func (h *JobHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(jobID); err != nil {
		RespondError(w, r, http.StatusBadRequest, "job id must be a UUID")
		return
	}

	if h.canceler == nil {
		RespondError(w, r, http.StatusNotImplemented, "cancelling is not wired on this controller")
		return
	}

	// The caller's own identity, read from the request rather than from
	// the job. Stopping a run is a new decision by whoever made it, and
	// stamping the job's original actor would put somebody else's name on
	// a choice they did not make. Relaunch takes the identity the same way
	// and for the same reason.
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity == nil {
		RespondError(w, r, http.StatusUnauthorized, "no identity on the request context")
		return
	}

	switch err := h.canceler.Cancel(r.Context(), jobID, identity.Subject); {
	case errors.Is(err, dispatch.ErrJobNotFound):
		RespondError(w, r, http.StatusNotFound, "job not found")
		return
	case errors.Is(err, dispatch.ErrNotCancelable):
		RespondError(w, r, http.StatusConflict, "job has already finished")
		return
	case err != nil:
		// The store's own text is logged rather than returned, the same
		// posture Get above takes: it can name tables and columns a caller
		// holding runbook:execute has no business reading.
		loggerFrom(r).ErrorContext(r.Context(), "failed to cancel job",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// Re-read, unlike the project sync cancel's own handler, which answers
	// with the record as it was a moment before. Here the write has already
	// landed by the time this returns, so the caller can be handed the
	// canceled job itself rather than the running one it just stopped. A
	// read failure now is not worth failing the cancel over: it succeeded,
	// and saying otherwise would invite a retry that would then 409.
	job, tasks, err := h.jobs.Get(r.Context(), jobID)
	if err != nil {
		loggerFrom(r).WarnContext(r.Context(), "job was canceled but could not be read back",
			slog.String("job_id", jobID),
			slog.String("error", err.Error()))
		Respond(w, r, http.StatusAccepted, &jobResponse{JobID: jobID, State: "canceled"})
		return
	}

	dto := toJobResponse(job, tasks)
	Respond(w, r, http.StatusAccepted, &dto)
}

// jobSummaryDTO is one job in a list: identity, state, and tallies,
// without the per-device task rows a detail view shows. A list of a
// thousand jobs must not carry a hundred thousand task rows nobody
// rendered.
type jobSummaryDTO struct {
	JobID        string `json:"job_id"`
	RunbookID    string `json:"runbook_id"`
	State        string `json:"state"`
	TemplateName string `json:"template_name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Actor        string `json:"actor"`
	Dispatched   int    `json:"dispatched"`
	Skipped      int    `json:"skipped"`
	Failed       int    `json:"failed"`
	CreatedAt    string `json:"created_at"`
}

// jobListDTO is one page of jobs, newest first.
type jobListDTO struct {
	LinkSet

	Jobs       []jobSummaryDTO `json:"jobs"`
	NextCursor string          `json:"next_cursor"`
}

// List serves a bounded, keyset-paginated page of jobs, newest first.
func (h *JobHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := defaultJobListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			RespondError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(parsed, maxJobListLimit)
	}

	after := r.URL.Query().Get("after")
	if after != "" {
		// The cursor is a job id, and a job id is a UUID. Validating it
		// here is the same guard StreamLogs applies to {id}: the value
		// reaches a storage query, and accepting arbitrary text would mean
		// a caller choosing what that query compares against.
		if _, err := uuid.Parse(after); err != nil {
			RespondError(w, r, http.StatusBadRequest, "after must be a UUID")
			return
		}
	}

	// One more than asked for, so the presence of a next page is observed
	// rather than inferred from a full page.
	jobs, err := h.jobs.List(r.Context(), after, limit+1)
	if err != nil {
		loggerFrom(r).ErrorContext(r.Context(), "failed to list jobs",
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := jobListDTO{Jobs: make([]jobSummaryDTO, 0, limit)}
	for _, j := range jobs {
		if len(dto.Jobs) == limit {
			dto.NextCursor = dto.Jobs[limit-1].JobID
			break
		}
		dto.Jobs = append(dto.Jobs, jobSummaryDTO{
			JobID:        j.JobID,
			RunbookID:    j.RunbookID,
			State:        j.State,
			TemplateName: j.TemplateName,
			Kind:         j.Kind,
			Actor:        j.Actor,
			Dispatched:   j.DispatchedCount,
			Skipped:      j.SkippedCount,
			Failed:       j.FailedCount,
			CreatedAt:    j.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	Respond(w, r, http.StatusOK, &dto)
}
