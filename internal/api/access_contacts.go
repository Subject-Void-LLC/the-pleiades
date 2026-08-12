package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
)

// This file serves the accountability surface: contacts, and the two attest
// endpoints that say somebody confirmed a record's contacts are current.

// contactDTO is the wire projection of an accountability record.
type contactDTO struct {
	LinkSet

	ID    int    `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
	URL   string `json:"url,omitempty"`
	Notes string `json:"notes,omitempty"`
	Order int    `json:"order"`

	Organization int `json:"organization,omitempty"`
	Team         int `json:"team,omitempty"`

	// AccountableFor renders the owner in words, for the reason the grants
	// view's own provenance column exists: "organization 7" is readable
	// where a bare integer beside two other integers is not.
	AccountableFor string `json:"accountable_for"`
}

type contactListDTO struct {
	LinkSet
	Contacts []contactDTO `json:"contacts"`
}

// contactWriteDTO is a submitted contact.
//
// Order is a pointer for the reason every other partial field on this
// surface is: zero is a meaningful order, so a plain int cannot distinguish
// "put this first" from "the caller said nothing about ordering" and a PATCH
// renaming a contact would silently promote it to the top of the escalation
// path.
type contactWriteDTO struct {
	Name         string `json:"name"`
	Role         string `json:"role"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	URL          string `json:"url"`
	Notes        string `json:"notes"`
	Order        *int   `json:"order"`
	Organization int    `json:"organization"`
	Team         int    `json:"team"`
}

// order reads a submitted position, reporting whether the caller mentioned
// it at all.
func (d contactWriteDTO) order() (int, bool) {
	if d.Order == nil {
		return 0, false
	}
	return *d.Order, true
}

func toContactDTO(c access.Contact) contactDTO {
	return contactDTO{
		ID:             c.ID,
		Name:           c.Name,
		Role:           string(c.Role),
		Email:          c.Email,
		Phone:          c.Phone,
		URL:            c.URL,
		Notes:          c.Notes,
		Order:          c.Order,
		Organization:   c.OrganizationID,
		Team:           c.TeamID,
		AccountableFor: c.Owner(),
	}
}

// ListContacts serves a page of contacts, narrowed to one owner when asked.
func (h *AccessHandler) ListContacts(w http.ResponseWriter, r *http.Request) {
	// Both owner filters are read before the shared paging helper runs, so
	// a malformed one is a 400 rather than a silent listing of every
	// contact in the deployment.
	orgID, ok := h.optionalID(w, r, "organization")
	if !ok {
		return
	}
	teamID, ok := h.optionalID(w, r, "team")
	if !ok {
		return
	}

	listContext[access.Contact]{
		handler: h,
		op:      "list contacts",
		load: func(ctx context.Context, q access.Query) ([]access.Contact, error) {
			return h.store.ListContacts(ctx, access.ContactQuery{
				Query: q, OrganizationID: orgID, TeamID: teamID,
			})
		},
	}.serve(w, r, func(contacts []access.Contact) {
		dto := contactListDTO{Contacts: make([]contactDTO, 0, len(contacts))}
		for _, c := range contacts {
			dto.Contacts = append(dto.Contacts, toContactDTO(c))
		}
		Respond(w, r, http.StatusOK, &dto)
	})
}

// GetContact serves one contact.
func (h *AccessHandler) GetContact(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	contact, err := h.store.GetContact(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "get contact", id, err)
		return
	}
	dto := toContactDTO(contact)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateContact persists a new contact against exactly one owner.
func (h *AccessHandler) CreateContact(w http.ResponseWriter, r *http.Request) {
	var body contactWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	position, _ := body.order()
	created, err := h.store.CreateContact(r.Context(), access.Contact{
		Name:           body.Name,
		Role:           access.ContactRole(body.Role),
		Email:          body.Email,
		Phone:          body.Phone,
		URL:            body.URL,
		Notes:          body.Notes,
		Order:          position,
		OrganizationID: body.Organization,
		TeamID:         body.Team,
	})
	if err != nil {
		h.respondAccessError(w, r, "create contact", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/contacts/"+strconv.Itoa(created.ID))
	dto := toContactDTO(created)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateContact changes a contact's details.
//
// The owner is not read from the body at all. The store carries it forward
// from storage, and sending one here would look like it worked.
func (h *AccessHandler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	var body contactWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	existing, err := h.store.GetContact(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read contact", id, err)
		return
	}
	existing.Name = body.Name
	existing.Role = access.ContactRole(body.Role)
	existing.Email, existing.Phone, existing.URL = body.Email, body.Phone, body.URL
	existing.Notes = body.Notes
	if position, mentioned := body.order(); mentioned {
		existing.Order = position
	}

	if err := h.store.UpdateContact(r.Context(), existing); err != nil {
		h.respondAccessError(w, r, "update contact", id, err)
		return
	}
	updated, err := h.store.GetContact(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read contact", id, err)
		return
	}
	dto := toContactDTO(updated)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteContact removes a contact.
func (h *AccessHandler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteContact(r.Context(), id); err != nil {
		h.respondAccessError(w, r, "delete contact", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AttestOrganization records that the caller confirmed a tenant's ownership
// information is current.
//
// There is no request body, deliberately, and this handler could not honour
// one if it were sent: the subject comes from the authenticated identity.
// That is the entire value of the record. An attestation naming somebody
// chosen by whoever is writing tells you only that a request was made.
func (h *AccessHandler) AttestOrganization(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity == nil {
		// Unauthenticated requests do not reach here through the real
		// router, which runs auth before any handler. Checked anyway,
		// because the failure if it ever did would be a stored
		// attestation signed by nobody, which reads as confirmed.
		RespondError(w, r, http.StatusUnauthorized, "no identity on the request")
		return
	}
	if err := h.store.AttestOrganization(r.Context(), id, identity.Subject); err != nil {
		h.respondAccessError(w, r, "attest organization", id, err)
		return
	}

	org, err := h.store.GetOrganization(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read organization", id, err)
		return
	}
	dto := toOrganizationDTO(org)
	Respond(w, r, http.StatusOK, &dto)
}

// AttestTeam is AttestOrganization for a team.
func (h *AccessHandler) AttestTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity == nil {
		RespondError(w, r, http.StatusUnauthorized, "no identity on the request")
		return
	}
	if err := h.store.AttestTeam(r.Context(), id, identity.Subject); err != nil {
		h.respondAccessError(w, r, "attest team", id, err)
		return
	}

	team, err := h.store.GetTeam(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read team", id, err)
		return
	}
	dto := toTeamDTO(team, true)
	Respond(w, r, http.StatusOK, &dto)
}
