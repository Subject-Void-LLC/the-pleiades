// This file declares the project endpoints.
//
// # These are not mounted, and that is deliberate rather than unfinished
//
// They are absent from the Endpoints table below-file in apispec.go, so
// they generate no OpenAPI operation and claim no HTTP surface. What they
// exist for is the UI: view.Ops takes an *Endpoint per operation and reads
// its Scope and Rel to decide which affordances a person's token permits,
// so a view cannot be gated at all without one. internal/ui holds its
// domain ports directly and never dials the API, so the Projects view is
// fully functional with no route mounted anywhere.
//
// Adding them to Endpoints is what makes the REST surface real, and that is
// a separate change with a handler in internal/api and a registration in
// cmd/controller beside it. Listing them there first would publish an
// OpenAPI document describing routes that answer 404, which is the exact
// silent-drift failure CLAUDE.md warns about for the spec-to-router gap.
package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// projectSchema is one project on the wire.
var projectSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":           map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"name":         stringSchema("Unique within its organization, not globally."),
		"description":  stringSchema("What is in this repository."),
		"organization": map[string]any{"type": "integer", "description": "The tenancy boundary this project belongs to."},
		"scm_type":     stringSchema("How the content is reached. Only \"git\" is implemented."),
		"scm_url":      stringSchema("The repository to clone."),
		"scm_branch":   stringSchema("The branch, tag or commit to check out. Empty means the remote's own default."),
		"credential":   map[string]any{"type": "integer", "description": "The credential the clone authenticates as, absent for a public repository."},
		"revision":     stringSchema("The commit the working tree is at. Empty until a sync has succeeded once."),
		"sync_status":  stringSchema("One of never, pending, running, succeeded or failed."),
		"sync_error":   stringSchema("Why the last sync failed, with credential material already stripped."),
		"last_synced_at": map[string]any{
			"type": "string", "format": "date-time",
			"description": "When the last sync ran, absent if none has.",
		},
		"_links": linksSchema(),
	},
}

// projectListSchema is a page of projects.
var projectListSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"projects": map[string]any{"type": "array", "items": projectSchema},
		"_links":   linksSchema(),
	},
}

// ListProjects is GET /projects.
var ListProjects = Endpoint{
	Name:    "list_projects",
	Method:  http.MethodGet,
	Pattern: "/projects",
	Scope:   auth.ScopeProjectRead,
	Rel:     auth.RelCollection,
	Summary: "List projects",
	Responses: []Response{
		{Status: http.StatusOK, Description: "The projects.", Schema: projectListSchema},
	},
	Description: "Returns the source repositories this deployment runs automation out of.",
}

// GetProject is GET /projects/{id}.
var GetProject = Endpoint{
	Name:    "get_project",
	Method:  http.MethodGet,
	Pattern: "/projects/{id}",
	Scope:   auth.ScopeProjectRead,
	Rel:     auth.RelSelf,
	Summary: "Get one project",
	Responses: []Response{
		{Status: http.StatusOK, Description: "The project.", Schema: projectSchema},
		{Status: http.StatusBadRequest, Description: "id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No project with that id.", Schema: errorSchema("")},
	},
	Description: "Returns one project, including the state of its last sync.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The project's numeric id."},
	},
}

// CreateProject is POST /projects.
var CreateProject = Endpoint{
	Name:    "create_project",
	Method:  http.MethodPost,
	Pattern: "/projects",
	Scope:   auth.ScopeProjectWrite,
	Rel:     auth.RelCreate,
	Summary: "Create a project",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The project as stored.", Schema: projectSchema},
		{Status: http.StatusBadRequest, Description: "A required field is missing, or a git project carries no URL.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "The name is taken in that organization.", Schema: errorSchema("")},
	},
	Description: "Registers a source repository. Nothing is fetched until a sync runs.",
}

// UpdateProject is PATCH /projects/{id}.
var UpdateProject = Endpoint{
	Name:    "update_project",
	Method:  http.MethodPatch,
	Pattern: "/projects/{id}",
	Scope:   auth.ScopeProjectWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a project",
	Responses: []Response{
		{Status: http.StatusOK, Description: "The project as stored.", Schema: projectSchema},
		{Status: http.StatusBadRequest, Description: "A required field is missing, or a git project carries no URL.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No project with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "The name is taken in that organization.", Schema: errorSchema("")},
	},
	Description: "Changes a project's name, description or source. The checkout is not refetched until a sync runs.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The project's numeric id."},
	},
}

// DeleteProject is DELETE /projects/{id}.
var DeleteProject = Endpoint{
	Name:    "delete_project",
	Method:  http.MethodDelete,
	Pattern: "/projects/{id}",
	Scope:   auth.ScopeProjectWrite,
	Rel:     auth.RelDelete,
	Summary: "Delete a project",
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusNotFound, Description: "No project with that id.", Schema: errorSchema("")},
	},
	Description: "Removes a project. Its working tree on disk is left in place.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The project's numeric id."},
	},
}

// SyncProject is POST /projects/{id}/sync.
//
// A write scope rather than a read one, even though a sync only fetches:
// it makes the controller open a connection to an address the project
// names, and writes the result to local disk.
//
// RelExecute rather than a rel of its own, because that is the vocabulary
// this codebase already has for "an action, not a mutation of the record",
// and a one-off rel would be a second word for the same idea.
var SyncProject = Endpoint{
	Name:    "sync_project",
	Method:  http.MethodPost,
	Pattern: "/projects/{id}/sync",
	Scope:   auth.ScopeProjectWrite,
	Rel:     auth.RelExecute,
	Summary: "Sync a project",
	Responses: []Response{
		{Status: http.StatusOK, Description: "The project after the attempt. A sync that RAN AND FAILED is also a 200: the outcome is in sync_status and sync_error, because the request succeeded and somebody else's repository did not.", Schema: projectSchema},
		{Status: http.StatusBadRequest, Description: "The project has no fetchable source.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No project with that id.", Schema: errorSchema("")},
	},
	Description: "Clones or fast-forwards the project's working tree and records the commit it ended up at.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The project's numeric id."},
	},
}
