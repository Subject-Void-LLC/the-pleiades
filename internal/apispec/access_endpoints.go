package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// The access administration surface: Organizations, Teams, Users and role
// bindings.
//
// Twenty endpoints for four entities, declared here rather than generated,
// because apispec is the single source both the router and the OpenAPI
// document build from and a generated table would be a second thing to read.
// They share two scopes: access:read and access:write. See internal/auth's
// own scope vocabulary for why the split is by operation rather than by
// entity.

// ListOrganizations returns a page of organizations.
var ListOrganizations = Endpoint{
	Name:        "list_organizations",
	Method:      http.MethodGet,
	Pattern:     "/organizations",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelCollection,
	Summary:     "List organizations",
	Description: "Returns organizations, keyset-paged. Each is the tenancy boundary everything ownable belongs to.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the last id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum records to return."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name filter."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of organizations.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"organizations": map[string]any{"type": "array", "items": organizationSchema},
				"_links":        linksSchema(),
			},
		}},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller lacks access:read.", Schema: errorSchema("")},
	},
}

// GetOrganization returns one record.
var GetOrganization = Endpoint{
	Name:        "get_organization",
	Method:      http.MethodGet,
	Pattern:     "/organizations/{id}",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one organization",
	Description: "Returns one organization: the tenancy boundary everything ownable belongs to.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The organization.", Schema: organizationSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// CreateOrganization persists a new record.
var CreateOrganization = Endpoint{
	Name:        "create_organization",
	Method:      http.MethodPost,
	Pattern:     "/organizations",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelCreate,
	Summary:     "Create a organization",
	Description: "Creates the tenancy boundary everything ownable belongs to.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created record.", Schema: organizationSchema},
		{Status: http.StatusBadRequest, Description: "The submission is not a valid organization.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken.", Schema: errorSchema("")},
	},
	RequestSchema: organizationWriteSchema,
}

// UpdateOrganization edits an existing record.
var UpdateOrganization = Endpoint{
	Name:    "update_organization",
	Method:  http.MethodPatch,
	Pattern: "/organizations/{id}",
	Scope:   auth.ScopeAccessWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a organization",
	Description: "A partial update of one organization. A field left out keeps its stored value and a field " +
		"present is applied, so a PATCH naming only the name leaves the freeze and the change-management fields " +
		"as they are, and frozen: false is the explicit unfreeze. A null on any field is refused. The organization " +
		"as it would be saved is checked whole. Concurrent PATCHes are last-writer-wins: there is no version check.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated record.", Schema: organizationSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed or sets a field to null, or the organization it would leave has a blank name or an unknown classification.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken.", Schema: errorSchema("")},
	},
	RequestSchema: organizationPatchSchema,
}

// DeleteOrganization removes a record.
var DeleteOrganization = Endpoint{
	Name:        "delete_organization",
	Method:      http.MethodDelete,
	Pattern:     "/organizations/{id}",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelDelete,
	Summary:     "Delete a organization",
	Description: "Removes one organization. Deleting the last system-scope grant is refused, because it would leave nobody able to administer the deployment.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "The record was removed."},
		{Status: http.StatusConflict, Description: "Removing this record would leave the deployment unadministrable.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// ListTeams returns a page of teams.
var ListTeams = Endpoint{
	Name:        "list_teams",
	Method:      http.MethodGet,
	Pattern:     "/teams",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelCollection,
	Summary:     "List teams",
	Description: "Returns teams, keyset-paged. Each is a group of users, and the only thing a role is ever granted to.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the last id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum records to return."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name filter."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of teams.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"teams":  map[string]any{"type": "array", "items": teamSchema},
				"_links": linksSchema(),
			},
		}},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller lacks access:read.", Schema: errorSchema("")},
	},
}

// GetTeam returns one record.
var GetTeam = Endpoint{
	Name:        "get_team",
	Method:      http.MethodGet,
	Pattern:     "/teams/{id}",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one team",
	Description: "Returns one team: a group of users, and the only thing a role is ever granted to.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The team.", Schema: teamSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// CreateTeam persists a new record.
var CreateTeam = Endpoint{
	Name:        "create_team",
	Method:      http.MethodPost,
	Pattern:     "/teams",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelCreate,
	Summary:     "Create a team",
	Description: "Creates a group of users, and the only thing a role is ever granted to.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created record.", Schema: teamSchema},
		{Status: http.StatusBadRequest, Description: "The submission is not a valid team.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken.", Schema: errorSchema("")},
	},
	RequestSchema: teamWriteSchema,
}

// UpdateTeam edits an existing record.
var UpdateTeam = Endpoint{
	Name:    "update_team",
	Method:  http.MethodPatch,
	Pattern: "/teams/{id}",
	Scope:   auth.ScopeAccessWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a team",
	Description: "A partial update of one team's name, description and membership. A field left out keeps its " +
		"stored value and a field present is applied; a null on any field is refused. The organization is not " +
		"updatable: a value for it that differs from the stored one is refused, because moving a team between " +
		"tenants would silently re-scope every grant it holds, and the stored value is accepted as an echo. " +
		"Concurrent PATCHes are last-writer-wins: there is no version check.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated record.", Schema: teamSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, sets a field to null or changes the organization, or the team it would leave has a blank name.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken in this organization.", Schema: errorSchema("")},
	},
	RequestSchema: teamPatchSchema,
}

// DeleteTeam removes a record.
var DeleteTeam = Endpoint{
	Name:        "delete_team",
	Method:      http.MethodDelete,
	Pattern:     "/teams/{id}",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelDelete,
	Summary:     "Delete a team",
	Description: "Removes one team. Deleting the last system-scope grant is refused, because it would leave nobody able to administer the deployment.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "The record was removed."},
		{Status: http.StatusConflict, Description: "Removing this record would leave the deployment unadministrable.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// ListUsers returns a page of users.
var ListUsers = Endpoint{
	Name:        "list_users",
	Method:      http.MethodGet,
	Pattern:     "/users",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelCollection,
	Summary:     "List users",
	Description: "Returns users, keyset-paged. Each is an identity, joined to a token's subject by its email address.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the last id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum records to return."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name filter."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of users.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"users":  map[string]any{"type": "array", "items": userSchema},
				"_links": linksSchema(),
			},
		}},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller lacks access:read.", Schema: errorSchema("")},
	},
}

// GetUser returns one record.
var GetUser = Endpoint{
	Name:        "get_user",
	Method:      http.MethodGet,
	Pattern:     "/users/{id}",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one user",
	Description: "Returns one user: an identity, joined to a token's subject by its email address.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The user.", Schema: userSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// CreateUser persists a new record.
var CreateUser = Endpoint{
	Name:        "create_user",
	Method:      http.MethodPost,
	Pattern:     "/users",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelCreate,
	Summary:     "Create a user",
	Description: "Creates an identity, joined to a token's subject by its email address.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created record.", Schema: userSchema},
		{Status: http.StatusBadRequest, Description: "The submission is not a valid user.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken.", Schema: errorSchema("")},
	},
	RequestSchema: userWriteSchema,
}

// UpdateUser edits an existing record.
var UpdateUser = Endpoint{
	Name:        "update_user",
	Method:      http.MethodPatch,
	Pattern:     "/users/{id}",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelUpdate,
	Summary:     "Update a user",
	Description: "Edits one user. Fields that would re-scope an existing grant are never updatable.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated record.", Schema: userSchema},
		{Status: http.StatusBadRequest, Description: "The submission is not valid.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
	RequestSchema: userWriteSchema,
}

// DeleteUser removes a record.
var DeleteUser = Endpoint{
	Name:        "delete_user",
	Method:      http.MethodDelete,
	Pattern:     "/users/{id}",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelDelete,
	Summary:     "Delete a user",
	Description: "Removes one user. Deleting the last system-scope grant is refused, because it would leave nobody able to administer the deployment.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "The record was removed."},
		{Status: http.StatusConflict, Description: "Removing this record would leave the deployment unadministrable.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// ListBindings returns a page of bindings.
var ListBindings = Endpoint{
	Name:        "list_bindings",
	Method:      http.MethodGet,
	Pattern:     "/bindings",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelCollection,
	Summary:     "List bindings",
	Description: "Returns bindings, keyset-paged. Each is one grant: a team, a role, a place in the containment hierarchy, and allow or deny.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the last id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum records to return."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name filter."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of bindings.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bindings": map[string]any{"type": "array", "items": bindingSchema},
				"_links":   linksSchema(),
			},
		}},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller lacks access:read.", Schema: errorSchema("")},
	},
}

// GetBinding returns one record.
var GetBinding = Endpoint{
	Name:        "get_role_binding",
	Method:      http.MethodGet,
	Pattern:     "/bindings/{id}",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one role binding",
	Description: "Returns one role binding: one grant: a team, a role, a place in the containment hierarchy, and allow or deny.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The role binding.", Schema: bindingSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// CreateBinding persists a new record.
var CreateBinding = Endpoint{
	Name:        "create_role_binding",
	Method:      http.MethodPost,
	Pattern:     "/bindings",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelCreate,
	Summary:     "Create a role binding",
	Description: "Creates one grant: a team, a role, a place in the containment hierarchy, and allow or deny.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created record.", Schema: bindingSchema},
		{Status: http.StatusBadRequest, Description: "The submission is not a valid role binding.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken.", Schema: errorSchema("")},
	},
	RequestSchema: bindingWriteSchema,
}

// UpdateBinding edits an existing record.
var UpdateBinding = Endpoint{
	Name:    "update_role_binding",
	Method:  http.MethodPatch,
	Pattern: "/bindings/{id}",
	Scope:   auth.ScopeAccessWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a role binding",
	Description: "A partial update of one grant's role or effect. A field left out keeps its stored value and a " +
		"field present is applied; a null on any field is refused. The team, scope_type and scope_id are not " +
		"updatable: a value for one that differs from the stored one is refused, because re-pointing a grant is " +
		"revoking one and issuing another, and the stored value is accepted as an echo. Editing the last " +
		"system-scope allow into a deny is refused. Concurrent PATCHes are last-writer-wins: there is no version check.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated record.", Schema: bindingSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, sets a field to null or changes the team or the scope, or the grant it would leave names an unknown role or effect.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "The edit would leave the deployment with no system-scope allow, so nobody could administer it.", Schema: errorSchema("")},
	},
	RequestSchema: bindingPatchSchema,
}

// DeleteBinding removes a record.
var DeleteBinding = Endpoint{
	Name:        "delete_role_binding",
	Method:      http.MethodDelete,
	Pattern:     "/bindings/{id}",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelDelete,
	Summary:     "Delete a role binding",
	Description: "Removes one role binding. Deleting the last system-scope grant is refused, because it would leave nobody able to administer the deployment.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "The record was removed."},
		{Status: http.StatusConflict, Description: "Removing this record would leave the deployment unadministrable.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// AttestOrganization records that the caller confirmed a tenant's ownership
// information is current.
//
// A POST to its own sub-resource rather than a field on the update, because
// the two are different acts. An edit changes what the record says; an
// attestation is a claim about the record made by a named person at a named
// time, and it is only worth anything if that name cannot be chosen by
// whoever is writing. There is deliberately no request body: the subject
// comes from the caller's identity.
var AttestOrganization = Endpoint{
	Name:        "attest_organization",
	Method:      http.MethodPost,
	Pattern:     "/organizations/{id}/attest",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelExecute,
	Summary:     "Attest an organization's ownership information",
	Description: "Records that the calling identity confirmed this tenant's contacts and escalation path are current, as of now. The subject is taken from the caller's token and cannot be supplied.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The attested organization.", Schema: organizationSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// AttestTeam is AttestOrganization for a team, and matters more: a team is
// what a role is granted to, so a team with no attested owner is a live set
// of permissions with nobody accountable for it.
var AttestTeam = Endpoint{
	Name:        "attest_team",
	Method:      http.MethodPost,
	Pattern:     "/teams/{id}/attest",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelExecute,
	Summary:     "Attest a team's ownership information",
	Description: "Records that the calling identity confirmed this team's owner and escalation path are current, as of now. The subject is taken from the caller's token and cannot be supplied.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The attested team.", Schema: teamSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// ListContacts returns a page of accountability records.
var ListContacts = Endpoint{
	Name:        "list_contacts",
	Method:      http.MethodGet,
	Pattern:     "/contacts",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelCollection,
	Summary:     "List contacts",
	Description: "Returns contacts, keyset-paged, in escalation order. Narrow to one owner with the organization or team parameter.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the last id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum records to return."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name or email filter."},
		{Name: "organization", In: "query", Required: false, Type: "integer", Description: "Only contacts accountable for this organization."},
		{Name: "team", In: "query", Required: false, Type: "integer", Description: "Only contacts accountable for this team."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of contacts.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"contacts": map[string]any{"type": "array", "items": contactSchema},
				"_links":   linksSchema(),
			},
		}},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller lacks access:read.", Schema: errorSchema("")},
	},
}

// GetContact returns one record.
var GetContact = Endpoint{
	Name:        "get_contact",
	Method:      http.MethodGet,
	Pattern:     "/contacts/{id}",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one contact",
	Description: "Returns one contact: who is accountable for an organization or a team, and how to reach them.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The contact.", Schema: contactSchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// CreateContact persists a new record.
var CreateContact = Endpoint{
	Name:        "create_contact",
	Method:      http.MethodPost,
	Pattern:     "/contacts",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelCreate,
	Summary:     "Create a contact",
	Description: "Records who is accountable for one organization or one team, and how to reach them. Exactly one owner, and at least one of email, phone or url.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created record.", Schema: contactSchema},
		{Status: http.StatusBadRequest, Description: "The submission names no owner, names two, or gives no way to reach anybody.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "The named owner does not exist.", Schema: errorSchema("")},
	},
	RequestSchema: contactWriteSchema,
}

// UpdateContact is a partial update of a record's details.
var UpdateContact = Endpoint{
	Name:    "update_contact",
	Method:  http.MethodPatch,
	Pattern: "/contacts/{id}",
	Scope:   auth.ScopeAccessWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a contact",
	Description: "A partial update of a contact's details. A field left out keeps its stored value and a field " +
		"present is applied; a null on any field is refused. The contact as it would be saved is checked whole, " +
		"so clearing its only channel is refused. The owner (organization and team) is not updatable: a value " +
		"that differs from the stored one is refused, because re-pointing an accountability record is deleting " +
		"one and creating another, and the stored value is accepted as an echo. Concurrent PATCHes are " +
		"last-writer-wins: there is no version check.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated record.", Schema: contactSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, sets a field to null or changes the owner, or the contact it would leave has no name, an unknown role or no way to reach anybody.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
	RequestSchema: contactPatchSchema,
}

// DeleteContact removes a record.
var DeleteContact = Endpoint{
	Name:        "delete_contact",
	Method:      http.MethodDelete,
	Pattern:     "/contacts/{id}",
	Scope:       auth.ScopeAccessWrite,
	Rel:         auth.RelDelete,
	Summary:     "Delete a contact",
	Description: "Removes one contact. Removing the last one is permitted: an owner-less record is a visible and fixable state, and the attestation beside it is what goes stale and says so.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "The record was removed."},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// organizationSchema is the wire projection of a tenancy boundary.
var organizationSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":             map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"name":           stringSchema("Unique across the deployment."),
		"description":    stringSchema("What this tenant is, in a sentence."),
		"classification": stringSchema("This tenant's own marking, one of " + classificationList + ". Empty means unmarked, which is not the same as unclassified."),
		"change_window":  stringSchema("When this tenant permits automation to run, written for a human. Recorded, not yet enforced."),
		"frozen":         map[string]any{"type": "boolean", "description": "An operator-declared stop on this tenant."},
		"freeze_reason":  stringSchema("Why the freeze is in place."),
		"cost_centre":    stringSchema("Opaque reference into the customer's own system of record."),
		"ticket_key":     stringSchema("Opaque reference into the customer's own ticketing system."),
		"cmdb_id":        stringSchema("Opaque reference into the customer's own CMDB."),
		"attested_by":    stringSchema("Who last confirmed this tenant's ownership information is current. Set only by the attest endpoint, never by a write."),
		"attested_at":    map[string]any{"type": "string", "format": "date-time", "description": "When that confirmation was made. Absent means never."},
		"_links":         linksSchema(),
	},
}

// organizationWriteSchema carries no attestation on purpose. The attest
// endpoint is the only way to set one, and it takes the subject from the
// caller's identity: an attestation a request body could name somebody else
// in is a rumour with a date on it, not a statement anybody is accountable
// for.
var organizationWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name"},
	"properties": map[string]any{
		"name":           stringSchema("Unique across the deployment. Trimmed on write, so two tenants cannot differ only by surrounding whitespace."),
		"description":    stringSchema("What this tenant is, in a sentence."),
		"classification": stringSchema("One of " + classificationList + ", or empty for unmarked. An environment marking is refused: an organization is not \"staging\", the deployment is."),
		"change_window":  stringSchema("When this tenant permits automation to run, written for a human."),
		"frozen":         map[string]any{"type": "boolean", "description": "An operator-declared stop on this tenant."},
		"freeze_reason":  stringSchema("Why the freeze is in place."),
		"cost_centre":    stringSchema("Opaque reference into the customer's own system of record."),
		"ticket_key":     stringSchema("Opaque reference into the customer's own ticketing system."),
		"cmdb_id":        stringSchema("Opaque reference into the customer's own CMDB."),
	},
}

// classificationList is the vocabulary, written once, so the read and write
// schemas cannot describe different sets.
const classificationList = "unclassified, cui, confidential, secret, topsecret, topsecret-sci"

// contactSchema is the wire projection of an accountability record.
var contactSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":              map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"name":            stringSchema("The person or, preferably, the rota."),
		"role":            stringSchema("One of owner, escalation, security, billing."),
		"email":           stringSchema("One of three ways to reach them; at least one is required."),
		"phone":           stringSchema("One of three ways to reach them; at least one is required."),
		"url":             stringSchema("One of three ways to reach them; at least one is required."),
		"notes":           stringSchema("The conditions under which this is the right contact."),
		"order":           map[string]any{"type": "integer", "description": "Who is tried first. Lower is earlier."},
		"organization":    map[string]any{"type": "integer", "description": "The tenancy boundary this contact is accountable for. Exactly one of this and team is set."},
		"team":            map[string]any{"type": "integer", "description": "The team this contact is accountable for. Exactly one of this and organization is set."},
		"accountable_for": stringSchema("Which record this contact belongs to, in words."),
		"_links":          linksSchema(),
	},
}

var contactWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "role"},
	"properties": map[string]any{
		"name":         stringSchema("The person or, preferably, the rota. An individual's name in an escalation field is a page that goes unanswered the week they are on leave."),
		"role":         stringSchema("One of owner, escalation, security, billing."),
		"email":        stringSchema("At least one of email, phone or url is required: a contact nobody can reach records that somebody is responsible without recording how to tell them."),
		"phone":        stringSchema("See email."),
		"url":          stringSchema("See email."),
		"notes":        stringSchema("The conditions under which this is the right contact."),
		"order":        map[string]any{"type": "integer", "description": "Who is tried first. Lower is earlier."},
		"organization": map[string]any{"type": "integer", "description": "Set exactly one of this and team. Not updatable: an accountability record cannot change what it is accountable for."},
		"team":         map[string]any{"type": "integer", "description": "Set exactly one of this and organization. Not updatable."},
	},
}

// teamSchema is the wire projection of a team.
var teamSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":                map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"name":              stringSchema("Unique within its organization."),
		"organization":      map[string]any{"type": "integer", "description": "The tenancy boundary this team belongs to."},
		"organization_name": stringSchema("That tenant's name, so a client can render the reference without a second request."),
		"description":       stringSchema("What this team is responsible for, as opposed to who is currently in it."),
		"users":             map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Member user ids."},
		"attested_by":       stringSchema("Who last confirmed this team's ownership information is current. Set only by the attest endpoint, never by a write."),
		"attested_at":       map[string]any{"type": "string", "format": "date-time", "description": "When that confirmation was made. Absent means never, which an access review must treat as stale."},
		"_links":            linksSchema(),
	},
}

var teamWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "organization"},
	"properties": map[string]any{
		"name":         stringSchema("What this team is called."),
		"organization": map[string]any{"type": "integer", "description": "Required, and not updatable: moving a team between tenants would silently re-scope every grant it holds."},
		"description":  stringSchema("What this team is responsible for. The name is a noun and the grants hanging off it are consequences; neither says why the team exists."),
		"users":        map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Member user ids. Omitted or [], the team starts with no members."},
	},
}

// userSchema is the wire projection of an identity.
var userSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":     map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"email":  stringSchema("The join key against a token's subject. Lowercased on write."),
		"teams":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Team ids this identity belongs to."},
		"_links": linksSchema(),
	},
}

var userWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"email"},
	"properties": map[string]any{
		"email": stringSchema("Lowercased on write, because two rows differing only in case would be two identities for one person."),
	},
}

// bindingSchema is the wire projection of one grant.
var bindingSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":         map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"team":       map[string]any{"type": "integer", "description": "The team this grant is held by. Roles are never granted to a user directly."},
		"team_name":  stringSchema("That team's name, beside its id, so a client can render the reference without a second request."),
		"scope_name": stringSchema("The named record this grant covers. Absent for system scope, which names no target, and for a target that has been deleted."),
		"role":       stringSchema("viewer, operator or admin."),
		"scope_type": stringSchema("system, organization, inventory, group or device."),
		"scope_id":   map[string]any{"type": "integer", "description": "The record this grant covers. Absent for system scope, which names no target."},
		"effect":     stringSchema("allow or deny. An explicit deny at any level beats an allow at a broader one."),
		"granted_at": stringSchema("Where this grant sits, rendered for a reader: the scope type and the named record."),
		"_links":     linksSchema(),
	},
}

var bindingWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"team", "role", "scope_type", "effect"},
	"properties": map[string]any{
		"team":       map[string]any{"type": "integer", "description": "Required."},
		"role":       stringSchema("viewer, operator or admin."),
		"scope_type": stringSchema("system, organization, inventory, group or device."),
		"scope_id":   map[string]any{"type": "integer", "description": "Required and positive at every scope except system, where it must be absent. A zero at a real scope would match every request naming nothing at that level."},
		"effect":     stringSchema("allow or deny."),
	},
}
