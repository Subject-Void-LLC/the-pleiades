// This file exposes the access administration surface over the API:
// organizations, teams, users and the role bindings that grant reach.
//
// It is the surface whose absence made the tenancy axis unusable. All four
// entities have been in the schema since Phase 1 or Phase 8, with a working
// resolver over them, and nothing could create one. See internal/access for
// the ports and for why administration lives apart from evaluation.
//
// Every route here is gated on access:read or access:write. Write is the one
// scope in this vocabulary that can be used to grant itself, which is why it
// is separated from every other write scope rather than folded into an admin
// catch-all.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/go-chi/chi/v5"
)

// AccessRepository is the slice of access.Store these handlers need, which
// is all of it: this is the one consumer that administers every collection.
type AccessRepository interface {
	access.Store
}

// AccessHandler serves the access administration resources.
type AccessHandler struct {
	store  AccessRepository
	logger *slog.Logger
}

// NewAccessHandler builds the handlers over store. A nil logger falls back
// to the process default.
func NewAccessHandler(store AccessRepository, logger *slog.Logger) *AccessHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AccessHandler{store: store, logger: logger}
}

// accessID reads and validates the {id} path parameter.
//
// Constrained to a positive integer at the boundary for the same reason
// every other numeric id here is: the value reaches a primary-key lookup,
// and checking its shape once is cheaper than trusting every layer beneath.
func (h *AccessHandler) accessID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

// accessQuery reads the shared paging and search parameters.
func (h *AccessHandler) accessQuery(w http.ResponseWriter, r *http.Request) (access.Query, bool) {
	q := access.Query{Search: r.URL.Query().Get("q")}

	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err := strconv.Atoi(raw)
		if err != nil || after < 0 {
			RespondError(w, r, http.StatusBadRequest, "after must be a non-negative integer")
			return access.Query{}, false
		}
		q.After = after
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			RespondError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return access.Query{}, false
		}
		q.Limit = limit
	}
	return q, true
}

// respondAccessError maps a store error onto the status it deserves.
//
// The store's own text can name a column or a constraint, which a caller has
// no business reading, so only the category crosses the boundary. The one
// exception is the last-grant refusal, whose whole value is that it explains
// itself: an operator who just tried to delete their own last key needs to
// know that is what they did.
func (h *AccessHandler) respondAccessError(w http.ResponseWriter, r *http.Request, op string, id int, err error) {
	switch {
	case errors.Is(err, access.ErrNotFound):
		RespondError(w, r, http.StatusNotFound, "not found")
	case errors.Is(err, access.ErrInUse):
		RespondError(w, r, http.StatusConflict, "something still references this record")
	case errors.Is(err, access.ErrExists):
		RespondError(w, r, http.StatusConflict, "a record with that name already exists")
	case errors.Is(err, access.ErrInvalidInput):
		RespondError(w, r, http.StatusBadRequest, "that is not a usable record")
	case errors.Is(err, access.ErrInvalidBinding):
		// 400 rather than 422: the submission is not a coherent grant at
		// all, rather than a coherent one that failed a business rule.
		RespondError(w, r, http.StatusBadRequest, "that is not a resolvable role binding")
	case errors.Is(err, access.ErrLastSystemBinding):
		RespondError(w, r, http.StatusConflict,
			"refusing to remove the last system-scope grant: nobody would be able to administer this deployment")
	default:
		h.logger.ErrorContext(r.Context(), "access store operation failed",
			slog.String("op", op), slog.Int("record_id", id), slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}

// organizationDTO is the wire projection of a tenancy boundary.
type organizationDTO struct {
	LinkSet

	ID             int    `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	Classification string `json:"classification,omitempty"`
	ChangeWindow   string `json:"change_window,omitempty"`
	Frozen         bool   `json:"frozen"`
	FreezeReason   string `json:"freeze_reason,omitempty"`
	CostCentre     string `json:"cost_centre,omitempty"`
	TicketKey      string `json:"ticket_key,omitempty"`
	CMDBID         string `json:"cmdb_id,omitempty"`

	// The attestation is read-only on this surface. It appears here so a
	// caller can see whether the tenant's ownership information has been
	// confirmed and when; it is set only by POST .../attest, which takes
	// the subject from the caller's token.
	AttestedBy string `json:"attested_by,omitempty"`
	AttestedAt string `json:"attested_at,omitempty"`
}

type organizationListDTO struct {
	LinkSet
	Organizations []organizationDTO `json:"organizations"`
}

// organizationWriteDTO carries no attestation fields at all, so there is no
// key a caller could send that would be quietly ignored. A write DTO that
// accepted and discarded them would look like it worked.
type organizationWriteDTO struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Classification string `json:"classification"`
	ChangeWindow   string `json:"change_window"`
	Frozen         bool   `json:"frozen"`
	FreezeReason   string `json:"freeze_reason"`
	CostCentre     string `json:"cost_centre"`
	TicketKey      string `json:"ticket_key"`
	CMDBID         string `json:"cmdb_id"`
}

// organization builds the domain value from a submission.
func (d organizationWriteDTO) organization() access.Organization {
	return access.Organization{
		Name:           d.Name,
		Description:    d.Description,
		Classification: access.Classification(d.Classification),
		ChangeWindow:   d.ChangeWindow,
		Frozen:         d.Frozen,
		FreezeReason:   d.FreezeReason,
		CostCentre:     d.CostCentre,
		TicketKey:      d.TicketKey,
		CMDBID:         d.CMDBID,
	}
}

func toOrganizationDTO(org access.Organization) organizationDTO {
	dto := organizationDTO{
		ID:             org.ID,
		Name:           org.Name,
		Description:    org.Description,
		Classification: string(org.Classification),
		ChangeWindow:   org.ChangeWindow,
		Frozen:         org.Frozen,
		FreezeReason:   org.FreezeReason,
		CostCentre:     org.CostCentre,
		TicketKey:      org.TicketKey,
		CMDBID:         org.CMDBID,
		AttestedBy:     org.Attested.By,
	}
	if org.Attested.At != nil {
		dto.AttestedAt = org.Attested.At.UTC().Format(time.RFC3339)
	}
	return dto
}

// ListOrganizations serves a page of tenancy boundaries.
func (h *AccessHandler) ListOrganizations(w http.ResponseWriter, r *http.Request) {
	q, ok := h.accessQuery(w, r)
	if !ok {
		return
	}
	orgs, err := h.store.ListOrganizations(r.Context(), q)
	if err != nil {
		h.respondAccessError(w, r, "list organizations", 0, err)
		return
	}

	dto := organizationListDTO{Organizations: make([]organizationDTO, 0, len(orgs))}
	for _, org := range orgs {
		dto.Organizations = append(dto.Organizations, toOrganizationDTO(org))
	}
	Respond(w, r, http.StatusOK, &dto)
}

// GetOrganization serves one tenancy boundary.
func (h *AccessHandler) GetOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	org, err := h.store.GetOrganization(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "get organization", id, err)
		return
	}
	dto := toOrganizationDTO(org)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateOrganization persists a new tenancy boundary.
func (h *AccessHandler) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	var body organizationWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.store.CreateOrganization(r.Context(), body.organization())
	if err != nil {
		h.respondAccessError(w, r, "create organization", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/organizations/"+strconv.Itoa(created.ID))
	dto := toOrganizationDTO(created)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateOrganization edits a tenancy boundary.
//
// The attestation is untouched: it is not on the write DTO at all, so an
// edit cannot carry a claim about who confirmed what.
func (h *AccessHandler) UpdateOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	var body organizationWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	org := body.organization()
	org.ID = id
	if err := h.store.UpdateOrganization(r.Context(), org); err != nil {
		h.respondAccessError(w, r, "update organization", id, err)
		return
	}

	updated, err := h.store.GetOrganization(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read organization", id, err)
		return
	}
	dto := toOrganizationDTO(updated)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteOrganization removes a tenancy boundary.
//
// The devices, teams and inventories that pointed at it are not deleted:
// deleting an administrative grouping must never delete somebody's hardware.
func (h *AccessHandler) DeleteOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteOrganization(r.Context(), id); err != nil {
		h.respondAccessError(w, r, "delete organization", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listContext is the shared shape of a paged read, so the three remaining
// collections do not each re-implement the same six lines.
type listContext[T any] struct {
	handler *AccessHandler
	load    func(context.Context, access.Query) ([]T, error)
	op      string
}

// serve runs one paged read and hands the results to render.
func (l listContext[T]) serve(w http.ResponseWriter, r *http.Request, render func([]T)) {
	q, ok := l.handler.accessQuery(w, r)
	if !ok {
		return
	}
	items, err := l.load(r.Context(), q)
	if err != nil {
		l.handler.respondAccessError(w, r, l.op, 0, err)
		return
	}
	render(items)
}

// optionalID reads a query parameter that narrows a listing to one record,
// where absence means no narrowing.
//
// A malformed one is refused rather than ignored. Silently dropping an
// unparseable owner filter would answer "every contact in the deployment"
// to a request that asked for one organization's, which is the shape of
// answer nobody checks because it looks like data.
func (h *AccessHandler) optionalID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		RespondError(w, r, http.StatusBadRequest, name+" must be a positive integer")
		return 0, false
	}
	return id, true
}
