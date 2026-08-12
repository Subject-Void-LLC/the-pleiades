package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
)

// This file serves the activity stream: who changed which managed object,
// and when.
//
// Read-only, and that is enforced by there being nothing else here rather
// than by a check inside a write handler. An endpoint that could append an
// entry would let a caller forge history; one that could remove an entry
// would let a caller erase their own. The only writer is internal/access's
// audited store, reached by making a change rather than by describing one.

// activityDTO is the wire projection of one recorded change.
type activityDTO struct {
	LinkSet

	ID     int    `json:"id"`
	Actor  string `json:"actor"`
	Action string `json:"action"`

	ObjectKind string `json:"object_kind"`
	ObjectID   int    `json:"object_id"`

	// ObjectName is the name the object carried when the change happened,
	// omitted when it had none. It is not resolved live: a rename after the
	// fact must not rewrite what the record says was changed, and a deleted
	// object must not blank the line describing its own deletion.
	ObjectName string `json:"object_name,omitempty"`

	At string `json:"at"`

	// Summary is the entry as one sentence, rendered by the domain type so
	// this API and the web UI cannot word the same event two ways.
	Summary string `json:"summary"`
}

type activityListDTO struct {
	LinkSet
	Activity []activityDTO `json:"activity"`
}

func toActivityDTO(e activity.Entry) activityDTO {
	return activityDTO{
		ID:         e.ID,
		Actor:      e.Actor,
		Action:     string(e.Action),
		ObjectKind: e.ObjectKind,
		ObjectID:   e.ObjectID,
		ObjectName: e.ObjectName,
		At:         e.At.UTC().Format(time.RFC3339),
		Summary:    e.Describe(),
	}
}

// ActivityHandler serves the activity stream.
type ActivityHandler struct {
	store  activity.Store
	logger *slog.Logger
}

// NewActivityHandler builds a handler over the stream.
func NewActivityHandler(store activity.Store, logger *slog.Logger) *ActivityHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ActivityHandler{store: store, logger: logger}
}

// ListActivity serves a page of the stream, newest first.
func (h *ActivityHandler) ListActivity(w http.ResponseWriter, r *http.Request) {
	q := activity.Query{Actor: r.URL.Query().Get("actor"), ObjectKind: r.URL.Query().Get("object_kind")}

	// after and limit are refused rather than ignored when malformed, for
	// the reason optionalID states: silently dropping a narrowing answers a
	// different question than the one asked, in a shape nobody rechecks.
	for _, param := range []struct {
		name string
		into *int
	}{
		{"after", &q.After},
		{"limit", &q.Limit},
		{"object_id", &q.ObjectID},
	} {
		raw := r.URL.Query().Get(param.name)
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			RespondError(w, r, http.StatusBadRequest, param.name+" must be a positive integer")
			return
		}
		*param.into = value
	}

	entries, err := h.store.List(r.Context(), q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "listing the activity stream failed", slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := activityListDTO{Activity: make([]activityDTO, 0, len(entries))}
	for _, e := range entries {
		dto.Activity = append(dto.Activity, toActivityDTO(e))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// GetActivityEntry serves one recorded change.
func (h *ActivityHandler) GetActivityEntry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	entry, err := h.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, activity.ErrNotFound) {
			RespondError(w, r, http.StatusNotFound, "not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "reading an activity entry failed",
			slog.Int("record_id", id), slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := toActivityDTO(entry)
	Respond(w, r, http.StatusOK, &dto)
}
