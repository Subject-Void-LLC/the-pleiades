// This file exposes the runbook catalog over the API: what this platform
// can be asked to run, and what each entry requires of a target device.
//
// It is the read side of the resource api.Dispatcher already writes
// against. Before it, a caller could dispatch a runbook by id but had no
// way to discover which ids existed -- the id had to be known out of band,
// which is precisely the hardcoded client-side knowledge hypermedia exists
// to remove.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/go-chi/chi/v5"
)

// RunbookRepository is the narrow slice of runbook.Source the catalog
// handlers need: the two read methods, never GetDAG.
//
// GetDAG's omission is the point. It returns a fully compiled execution
// graph, which is what an executor needs and what an HTTP reader has no
// use for; depending on it here would let a future handler serve one by
// accident. This is the same Interface Segregation shape DeviceRepository
// and JobRepository already use in this package.
type RunbookRepository interface {
	Get(ctx context.Context, id string) (*runbook.Runbook, error)
	List(ctx context.Context) ([]string, error)
}

// RunbookHandler serves the runbook catalog.
type RunbookHandler struct {
	runbooks RunbookRepository
	logger   *slog.Logger
}

// NewRunbookHandler builds the catalog handlers over runbooks. A nil
// logger falls back to the process default.
func NewRunbookHandler(runbooks RunbookRepository, logger *slog.Logger) *RunbookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RunbookHandler{runbooks: runbooks, logger: logger}
}

// runbookDTO is the wire projection of one catalog entry.
type runbookDTO struct {
	LinkSet

	ID string `json:"id"`

	// Required is the deduplicated union of capabilities every task in
	// this runbook needs of its target. It is what lets a caller tell,
	// before dispatching, whether a group can actually run this.
	Required []string `json:"required_capabilities"`

	// Interruptible reports whether the engine may stop this runbook
	// part-way through.
	Interruptible bool `json:"interruptible"`
}

// runbookListDTO is the catalog listing.
//
// It carries ids alone, not compiled runbooks. Compiling the whole library
// to render a list of names would make opening the catalog cost more the
// more automation an organization has written, which is exactly backwards.
type runbookListDTO struct {
	LinkSet

	Runbooks []string `json:"runbooks"`
}

// List serves the runbook catalog.
func (h *RunbookHandler) List(w http.ResponseWriter, r *http.Request) {
	ids, err := h.runbooks.List(r.Context())
	if err != nil {
		// The source's own error text can name filesystem paths, which a
		// caller holding only runbook:read has no business reading -- the
		// same posture devices.go and jobs.go already take.
		h.logger.ErrorContext(r.Context(), "failed to list runbooks",
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	// Initialized rather than left nil, so the JSON carries [] instead of
	// null for an empty catalog: a client should not have to guess which
	// one null meant.
	if ids == nil {
		ids = []string{}
	}
	dto := runbookListDTO{Runbooks: ids}
	Respond(w, r, http.StatusOK, &dto)
}

// Get serves one catalog entry, compiling it to report what it requires.
func (h *RunbookHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	rb, err := h.runbooks.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, runbook.ErrNotFound) {
			RespondError(w, r, http.StatusNotFound, "runbook not found")
			return
		}
		// Everything else -- a malformed id, a traversal attempt, a file
		// that will not compile -- is reported as a bad request without
		// echoing the id back. The source treats id as untrusted and
		// refuses it before touching the filesystem; reflecting a rejected
		// value into the response is the shape FAILURE_PATTERNS.md #72
		// records.
		h.logger.WarnContext(r.Context(), "failed to resolve runbook",
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusBadRequest, "invalid runbook id")
		return
	}

	required := make([]string, 0, len(rb.Required))
	for _, c := range rb.Required {
		required = append(required, string(c))
	}

	dto := runbookDTO{
		ID:            rb.ID,
		Required:      required,
		Interruptible: rb.Interruptible,
	}
	Respond(w, r, http.StatusOK, &dto)
}
