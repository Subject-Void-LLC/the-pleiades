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
}

// JobHandler serves the Job resource.
type JobHandler struct {
	jobs JobRepository
}

// NewJobHandler builds the Job resource's handlers over jobs.
func NewJobHandler(jobs JobRepository) *JobHandler {
	return &JobHandler{jobs: jobs}
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
	GroupName string `json:"group_name"`
	State     string `json:"state"`

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
		JobID:      job.JobID,
		RunbookID:  job.RunbookID,
		GroupName:  job.GroupName,
		State:      job.State,
		Dispatched: job.DispatchedCount,
		Skipped:    job.SkippedCount,
		Failed:     job.FailedCount,
		Tasks:      dtos,
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
