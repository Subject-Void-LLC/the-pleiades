package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// This file serves role bindings: the grants themselves.
//
// It carries one thing the other three collections do not, and it is the
// reason this file is separate. A binding is the only record here whose
// meaning is not evident from its own columns: "team 4, operator, inventory
// 7, allow" says nothing to a reader who does not already know what
// inventory 7 is. The response therefore carries a rendered provenance
// string alongside the raw fields, so a permissions table is auditable
// rather than merely readable.

// bindingDTO is the wire projection of one grant.
type bindingDTO struct {
	LinkSet

	ID   int    `json:"id"`
	Team int    `json:"team"`
	Role string `json:"role"`

	// TeamName and ScopeName accompany the ids rather than replacing them.
	// A client needs the id to write and the name to render, and making it
	// choose costs a second request per row. ScopeName is absent for a
	// system grant, which names no target, and for a target that has been
	// deleted, which GrantedAt says in words.
	TeamName  string `json:"team_name,omitempty"`
	ScopeName string `json:"scope_name,omitempty"`

	ScopeType string `json:"scope_type"`

	// ScopeID is omitted for a system-scope grant, which names no target.
	// Rendering it as 0 would suggest a record with that id.
	ScopeID *int   `json:"scope_id,omitempty"`
	Effect  string `json:"effect"`

	// GrantedAt is the provenance line: where this grant sits, in words.
	// Spacelift calls the equivalent column "Granted via", and it is the
	// difference between a permissions table somebody can audit and one they
	// can only read.
	GrantedAt string `json:"granted_at"`
}

type bindingListDTO struct {
	LinkSet
	Bindings []bindingDTO `json:"bindings"`
}

type bindingWriteDTO struct {
	Team      int    `json:"team"`
	Role      string `json:"role"`
	ScopeType string `json:"scope_type"`
	ScopeID   int    `json:"scope_id"`
	Effect    string `json:"effect"`
}

func toBindingDTO(b access.Binding) bindingDTO {
	dto := bindingDTO{
		ID:        b.ID,
		Team:      b.TeamID,
		TeamName:  b.TeamName,
		ScopeName: b.ScopeName,
		Role:      string(b.Role),
		ScopeType: string(b.ScopeType),
		Effect:    string(b.Effect),
		GrantedAt: grantedAt(b),
	}
	if !b.SystemWide() {
		id := b.ScopeID
		dto.ScopeID = &id
	}
	return dto
}

// grantedAt renders where a grant sits, in words.
//
// It names the target rather than numbering it, for the reason
// FAILURE_PATTERNS.md #107 records: a provenance line reading "inventory 7"
// tells a reader nothing they did not already have, and the store resolves
// the page's targets in one query per scope type.
//
// A deleted target is named as deleted, keeping its id, because the scope
// column carries no foreign key and a grant outliving its target is a real
// state that somebody has to be able to find and remove.
func grantedAt(b access.Binding) string {
	switch {
	case b.SystemWide():
		return "Everywhere (system)"
	case b.ScopeName != "":
		return string(b.ScopeType) + " " + b.ScopeName
	default:
		return string(b.ScopeType) + " " + strconv.Itoa(b.ScopeID) + " (deleted)"
	}
}

// ListBindings serves a page of grants.
func (h *AccessHandler) ListBindings(w http.ResponseWriter, r *http.Request) {
	listContext[access.Binding]{
		handler: h,
		op:      "list role bindings",
		load: func(ctx context.Context, q access.Query) ([]access.Binding, error) {
			bq := access.BindingQuery{Query: q}
			// Two facets, because the two questions an auditor actually asks
			// are "what does this team reach" and "who holds system scope".
			if raw := r.URL.Query().Get("team"); raw != "" {
				team, err := strconv.Atoi(raw)
				if err != nil || team < 1 {
					return nil, access.ErrNotFound
				}
				bq.TeamIDs = []int{team}
			}
			if raw := r.URL.Query().Get("scope_type"); raw != "" {
				bq.ScopeTypes = []auth.ScopeType{auth.ScopeType(raw)}
			}
			return h.store.ListBindings(ctx, bq)
		},
	}.serve(w, r, func(bindings []access.Binding) {
		dto := bindingListDTO{Bindings: make([]bindingDTO, 0, len(bindings))}
		for _, b := range bindings {
			dto.Bindings = append(dto.Bindings, toBindingDTO(b))
		}
		Respond(w, r, http.StatusOK, &dto)
	})
}

// GetBinding serves one grant.
func (h *AccessHandler) GetBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	b, err := h.store.GetBinding(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "get role binding", id, err)
		return
	}
	dto := toBindingDTO(b)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateBinding issues a grant.
func (h *AccessHandler) CreateBinding(w http.ResponseWriter, r *http.Request) {
	var body bindingWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.store.CreateBinding(r.Context(), access.Binding{
		TeamID:    body.Team,
		Role:      auth.Role(body.Role),
		ScopeType: auth.ScopeType(body.ScopeType),
		ScopeID:   body.ScopeID,
		Effect:    auth.Effect(body.Effect),
	})
	if err != nil {
		h.respondAccessError(w, r, "create role binding", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/bindings/"+strconv.Itoa(created.ID))
	dto := toBindingDTO(created)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateBinding changes a grant's role or effect.
//
// The scope and the team are carried forward from storage. Re-pointing a
// grant is indistinguishable from revoking one and issuing another, and an
// audit trail showing a grant quietly changing what it covers, or who holds
// it, is one nobody can reconstruct afterwards.
func (h *AccessHandler) UpdateBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	var body bindingWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	if err := h.store.UpdateBinding(r.Context(), access.Binding{
		ID:     id,
		Role:   auth.Role(body.Role),
		Effect: auth.Effect(body.Effect),
	}); err != nil {
		h.respondAccessError(w, r, "update role binding", id, err)
		return
	}

	updated, err := h.store.GetBinding(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read role binding", id, err)
		return
	}
	dto := toBindingDTO(updated)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteBinding revokes a grant, refusing to remove the last system-scope
// Allow in the deployment.
func (h *AccessHandler) DeleteBinding(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteBinding(r.Context(), id); err != nil {
		h.respondAccessError(w, r, "delete role binding", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
