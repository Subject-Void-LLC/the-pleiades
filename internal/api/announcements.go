// This file exposes operator announcements over the API: the messages an
// administrator puts in front of the people about to dispatch automation.
//
// Read and write are scoped separately and deliberately far apart.
// announcement:read has the widest natural audience this platform has --
// anybody who can sign in should see a change freeze, including identities
// that may read nothing else -- while announcement:write is the right to
// issue an instruction to every operator with the platform's own authority,
// which is worth granting and auditing on its own.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/go-chi/chi/v5"
)

// AnnouncementRepository is the announce.Store slice these handlers need.
type AnnouncementRepository interface {
	Create(ctx context.Context, a announce.Announcement) (announce.Announcement, error)
	Get(ctx context.Context, id int) (announce.Announcement, error)
	List(ctx context.Context, q announce.Query) ([]announce.Announcement, error)
	Update(ctx context.Context, a announce.Announcement) error
	Delete(ctx context.Context, id int) error
}

// AnnouncementHandler serves operator announcements.
type AnnouncementHandler struct {
	announcements AnnouncementRepository
	logger        *slog.Logger

	// now is the clock liveness is judged against, injectable so a test can
	// reason about an active window without sleeping through one.
	now func() time.Time
}

// NewAnnouncementHandler builds the handlers. A nil logger falls back to
// the process default.
func NewAnnouncementHandler(announcements AnnouncementRepository, logger *slog.Logger) *AnnouncementHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AnnouncementHandler{announcements: announcements, logger: logger, now: time.Now}
}

type announcementDTO struct {
	LinkSet

	ID           int    `json:"id"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Level        string `json:"level"`
	Author       string `json:"author"`
	Organization int    `json:"organization,omitempty"`
	StartsAt     string `json:"starts_at,omitempty"`
	EndsAt       string `json:"ends_at,omitempty"`
}

type announcementListDTO struct {
	LinkSet
	Announcements []announcementDTO `json:"announcements"`
}

// announcementWriteDTO is the request body create and update accept.
//
// There is no author field, and that is the point: it is taken from the
// caller's identity. An attribution a submitter could name is an
// attribution nobody can rely on, on a message instructing every operator
// how to run production.
type announcementWriteDTO struct {
	Title        string `json:"title"`
	Body         string `json:"body"`
	Level        string `json:"level"`
	Organization int    `json:"organization"`
	StartsAt     string `json:"starts_at"`
	EndsAt       string `json:"ends_at"`
}

func toAnnouncementDTO(a announce.Announcement) announcementDTO {
	dto := announcementDTO{
		ID:           a.ID,
		Title:        a.Title,
		Body:         a.Body,
		Level:        string(a.Level),
		Author:       a.Author,
		Organization: a.OrganizationID,
	}
	if a.StartsAt != nil {
		dto.StartsAt = a.StartsAt.UTC().Format(time.RFC3339)
	}
	if a.EndsAt != nil {
		dto.EndsAt = a.EndsAt.UTC().Format(time.RFC3339)
	}
	return dto
}

// List serves the announcements this caller may see.
//
// Live-only by default. An operator reading the dashboard must not be shown
// a change freeze that ended last month, and defaulting the other way would
// make that the common case rather than the exceptional one. ?all=true is
// for managing them.
func (h *AnnouncementHandler) List(w http.ResponseWriter, r *http.Request) {
	q := announce.Query{LiveAt: h.now()}

	if raw := r.URL.Query().Get("all"); raw != "" {
		all, err := strconv.ParseBool(raw)
		if err != nil {
			RespondError(w, r, http.StatusBadRequest, "all must be a boolean")
			return
		}
		if all {
			q.LiveAt = time.Time{}
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			RespondError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		q.Limit = limit
	}

	found, err := h.announcements.List(r.Context(), q)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to list announcements",
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
		return
	}

	dto := announcementListDTO{Announcements: make([]announcementDTO, 0, len(found))}
	for _, a := range found {
		dto.Announcements = append(dto.Announcements, toAnnouncementDTO(a))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// Create posts a new announcement.
func (h *AnnouncementHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body announcementWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	identity, ok := IdentityFromContext(r.Context())
	if !ok {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	a, ok := h.fromWrite(w, r, body)
	if !ok {
		return
	}
	a.Author = identity.Subject

	created, err := h.announcements.Create(r.Context(), a)
	if err != nil {
		h.respondStoreError(w, r, "create", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/announcements/"+strconv.Itoa(created.ID))
	dto := toAnnouncementDTO(created)
	Respond(w, r, http.StatusCreated, &dto)
}

// Update edits an existing announcement.
func (h *AnnouncementHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	var body announcementWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	// Read first, so the immutable fields come from what is stored rather
	// than from what was submitted, and so editing something that does not
	// exist is a 404 rather than a store error.
	existing, err := h.announcements.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}

	updated, ok := h.fromWrite(w, r, body)
	if !ok {
		return
	}
	updated.ID = existing.ID
	// Author and organization are carried forward, never taken from the
	// body. Rewriting an attribution is exactly what makes one worthless,
	// and moving an announcement between tenants would silently change who
	// it reaches.
	updated.Author = existing.Author
	updated.OrganizationID = existing.OrganizationID

	if err := h.announcements.Update(r.Context(), updated); err != nil {
		h.respondStoreError(w, r, "update", id, err)
		return
	}

	stored, err := h.announcements.Get(r.Context(), id)
	if err != nil {
		h.respondStoreError(w, r, "read", id, err)
		return
	}
	dto := toAnnouncementDTO(stored)
	Respond(w, r, http.StatusOK, &dto)
}

// Delete retires an announcement.
func (h *AnnouncementHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseID(w, r)
	if !ok {
		return
	}
	if err := h.announcements.Delete(r.Context(), id); err != nil {
		h.respondStoreError(w, r, "delete", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fromWrite parses a request body into the domain type, answering the
// caller itself on a malformed timestamp.
func (h *AnnouncementHandler) fromWrite(w http.ResponseWriter, r *http.Request, body announcementWriteDTO) (announce.Announcement, bool) {
	a := announce.Announcement{
		Title:          body.Title,
		Body:           body.Body,
		Level:          announce.ParseLevel(body.Level),
		OrganizationID: body.Organization,
	}

	starts, ok := h.parseInstant(w, r, "starts_at", body.StartsAt)
	if !ok {
		return announce.Announcement{}, false
	}
	ends, ok := h.parseInstant(w, r, "ends_at", body.EndsAt)
	if !ok {
		return announce.Announcement{}, false
	}
	a.StartsAt, a.EndsAt = starts, ends

	// Refused rather than silently swapped or accepted. A window that ends
	// before it starts is never live, so accepting it would mean posting an
	// urgent notice that is invisible from the moment it is written -- the
	// worst possible failure for a control whose entire job is being seen.
	if a.StartsAt != nil && a.EndsAt != nil && !a.EndsAt.After(*a.StartsAt) {
		RespondError(w, r, http.StatusBadRequest, "ends_at must be after starts_at")
		return announce.Announcement{}, false
	}
	return a, true
}

// parseInstant reads an optional RFC 3339 timestamp.
func (h *AnnouncementHandler) parseInstant(w http.ResponseWriter, r *http.Request, field, raw string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, field+" must be an RFC 3339 timestamp")
		return nil, false
	}
	return &parsed, true
}

// parseID reads and validates the {id} path parameter.
func (h *AnnouncementHandler) parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, "announcement id must be a positive integer")
		return 0, false
	}
	return id, true
}

// respondStoreError maps a store error onto its status, logging anything it
// cannot classify rather than passing the store's own text to a caller.
func (h *AnnouncementHandler) respondStoreError(w http.ResponseWriter, r *http.Request, op string, id int, err error) {
	if errors.Is(err, announce.ErrNotFound) {
		RespondError(w, r, http.StatusNotFound, "announcement not found")
		return
	}
	h.logger.ErrorContext(r.Context(), "announcement store operation failed",
		slog.String("op", op),
		slog.Int("announcement_id", id),
		slog.String("error", err.Error()))
	RespondError(w, r, http.StatusInternalServerError, "internal error")
}
