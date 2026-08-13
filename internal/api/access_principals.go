package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
)

// This file serves the two principal collections: teams, which roles are
// granted to, and users, which are the identities a token's subject resolves
// against.

// teamDTO is the wire projection of a team.
type teamDTO struct {
	LinkSet

	ID           int    `json:"id"`
	Name         string `json:"name"`
	Organization int    `json:"organization"`

	// OrganizationName accompanies the id rather than replacing it. A
	// client needs the id to write and the name to render, and making it
	// choose one costs it a second request per row.
	OrganizationName string `json:"organization_name,omitempty"`

	Description string `json:"description,omitempty"`

	// Read-only here, like an organization's: set only by POST
	// /teams/{id}/attest, which takes the subject from the caller's token.
	AttestedBy string `json:"attested_by,omitempty"`
	AttestedAt string `json:"attested_at,omitempty"`

	// Users is a pointer for the reason inventoryDTO's membership is:
	// omitempty drops an empty slice as readily as a nil one, so without the
	// indirection a team with no members and a listing that never carried
	// membership would both render as an absent key.
	Users *[]int `json:"users,omitempty"`
}

type teamListDTO struct {
	LinkSet
	Teams []teamDTO `json:"teams"`
}

type teamWriteDTO struct {
	Name         string `json:"name"`
	Organization int    `json:"organization"`
	Description  string `json:"description"`

	// Users is a pointer because PATCH means partial. A plain slice cannot
	// distinguish "the caller said nothing about membership" from "the
	// caller said the membership is now empty", and decoding both as nil
	// meant a PATCH renaming a team silently removed everybody from it.
	Users *[]int `json:"users"`
}

// members reads a submitted membership, reporting whether the caller
// mentioned it at all.
func (d teamWriteDTO) members() ([]int, bool) {
	if d.Users == nil {
		return nil, false
	}
	return *d.Users, true
}

func toTeamDTO(team access.Team, withMembers bool) teamDTO {
	dto := teamDTO{
		ID:               team.ID,
		Name:             team.Name,
		Organization:     team.OrganizationID,
		OrganizationName: team.OrganizationName,
		Description:      team.Description,
		AttestedBy:       team.Attested.By,
	}
	if team.Attested.At != nil {
		dto.AttestedAt = team.Attested.At.UTC().Format(time.RFC3339)
	}
	if withMembers {
		users := team.UserIDs
		if users == nil {
			users = []int{}
		}
		dto.Users = &users
	}
	return dto
}

// ListTeams serves a page of teams.
func (h *AccessHandler) ListTeams(w http.ResponseWriter, r *http.Request) {
	listContext[access.Team]{
		handler: h,
		op:      "list teams",
		load: func(ctx context.Context, q access.Query) ([]access.Team, error) {
			return h.store.ListTeams(ctx, access.TeamQuery{Query: q})
		},
	}.serve(w, r, func(teams []access.Team) {
		dto := teamListDTO{Teams: make([]teamDTO, 0, len(teams))}
		for _, team := range teams {
			dto.Teams = append(dto.Teams, toTeamDTO(team, false))
		}
		Respond(w, r, http.StatusOK, &dto)
	})
}

// GetTeam serves one team with its membership.
func (h *AccessHandler) GetTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	team, err := h.store.GetTeam(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "get team", id, err)
		return
	}
	dto := toTeamDTO(team, true)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateTeam persists a new team.
func (h *AccessHandler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	var body teamWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	members, _ := body.members()
	created, err := h.store.CreateTeam(r.Context(), access.Team{
		Name:           body.Name,
		OrganizationID: body.Organization,
		Description:    body.Description,
		UserIDs:        members,
	})
	if err != nil {
		h.respondAccessError(w, r, "create team", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/teams/"+strconv.Itoa(created.ID))
	dto := toTeamDTO(created, true)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateTeam renames a team and replaces its membership.
//
// The organization is carried forward from storage rather than taken from
// the body: moving a team between tenants would silently re-scope every
// grant it holds, which is a migration rather than an edit.
func (h *AccessHandler) UpdateTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	var body teamWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	existing, err := h.store.GetTeam(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read team", id, err)
		return
	}
	existing.Name = body.Name
	existing.Description = body.Description
	// Only replaced when the caller actually mentioned it. Omitting the key
	// leaves the membership exactly as it was.
	if members, mentioned := body.members(); mentioned {
		existing.UserIDs = members
	}

	if err := h.store.UpdateTeam(r.Context(), existing); err != nil {
		h.respondAccessError(w, r, "update team", id, err)
		return
	}
	updated, err := h.store.GetTeam(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read team", id, err)
		return
	}
	dto := toTeamDTO(updated, true)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteTeam removes a team and the grants it held.
func (h *AccessHandler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteTeam(r.Context(), id); err != nil {
		h.respondAccessError(w, r, "delete team", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// userDTO is the wire projection of an identity.
type userDTO struct {
	LinkSet

	ID    int    `json:"id"`
	Email string `json:"email"`

	Teams *[]int `json:"teams,omitempty"`
}

type userListDTO struct {
	LinkSet
	Users []userDTO `json:"users"`
}

type userWriteDTO struct {
	Email string `json:"email"`
}

func toUserDTO(user access.User, withTeams bool) userDTO {
	dto := userDTO{ID: user.ID, Email: user.Email}
	if withTeams {
		teams := user.TeamIDs
		if teams == nil {
			teams = []int{}
		}
		dto.Teams = &teams
	}
	return dto
}

// ListUsers serves a page of identities.
func (h *AccessHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	listContext[access.User]{handler: h, op: "list users", load: h.store.ListUsers}.
		serve(w, r, func(users []access.User) {
			dto := userListDTO{Users: make([]userDTO, 0, len(users))}
			for _, user := range users {
				dto.Users = append(dto.Users, toUserDTO(user, false))
			}
			Respond(w, r, http.StatusOK, &dto)
		})
}

// GetUser serves one identity with its team membership.
func (h *AccessHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	user, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "get user", id, err)
		return
	}
	dto := toUserDTO(user, true)
	Respond(w, r, http.StatusOK, &dto)
}

// CreateUser persists a new identity.
func (h *AccessHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var body userWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.store.CreateUser(r.Context(), access.User{Email: body.Email})
	if err != nil {
		h.respondAccessError(w, r, "create user", 0, err)
		return
	}

	w.Header().Set("Location", APIVersionPrefix+"/users/"+strconv.Itoa(created.ID))
	dto := toUserDTO(created, true)
	Respond(w, r, http.StatusCreated, &dto)
}

// UpdateUser changes an identity's address.
func (h *AccessHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	var body userWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.store.UpdateUser(r.Context(), access.User{ID: id, Email: body.Email}); err != nil {
		h.respondAccessError(w, r, "update user", id, err)
		return
	}

	updated, err := h.store.GetUser(r.Context(), id)
	if err != nil {
		h.respondAccessError(w, r, "read user", id, err)
		return
	}
	dto := toUserDTO(updated, true)
	Respond(w, r, http.StatusOK, &dto)
}

// DeleteUser removes an identity.
func (h *AccessHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := h.accessID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		h.respondAccessError(w, r, "delete user", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
