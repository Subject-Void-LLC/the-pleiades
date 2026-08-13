package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// activityEntrySchema is the wire projection of one recorded change.
var activityEntrySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":          map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"actor":       stringSchema("The authenticated subject that made the change, captured when it happened."),
		"action":      stringSchema("created, updated, deleted or attested."),
		"object_kind": stringSchema("What kind of object changed: organization, team, user, role binding or contact."),
		"object_id":   map[string]any{"type": "integer", "description": "That object's id, kept even after the object itself is deleted."},
		"object_name": stringSchema("The object's name as it was at the moment of the change, not as it is now. Absent when the object had none."),
		"at":          stringSchema("When the change happened, RFC 3339."),
		"summary":     stringSchema("The change as one sentence, the same wording the web UI renders."),
		"_links":      linksSchema(),
	},
}

// ListActivity returns a page of the activity stream.
//
// Scoped to access:read rather than to a new activity:read, and the
// reasoning has a trigger attached. Everything this stream records today is
// a change to one of the five objects access:read already covers, so a
// separate scope would grant nothing a holder of access:read could not
// already reconstruct by reading those objects, while adding a scope that
// no token is issued and no role grants -- which is how a view ends up
// rendering for nobody (FAILURE_PATTERNS.md #100). The moment a phase
// starts recording activity on objects outside that set, templates and
// inventories among them, this decision has to be made again: at that point
// access:read would be granting sight of changes to things it has nothing
// to do with.
var ListActivity = Endpoint{
	Name:        "list_activity",
	Method:      http.MethodGet,
	Pattern:     "/activity",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelCollection,
	Summary:     "List activity",
	Description: "Returns the activity stream newest first, keyset-paged: who changed which managed object, and when.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the lowest id of the previous page. The stream reads newest first, so paging walks downward."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum records to return."},
		{Name: "actor", In: "query", Required: false, Type: "string", Description: "Narrow to one subject's changes."},
		{Name: "object_kind", In: "query", Required: false, Type: "string", Description: "Narrow to one kind of object."},
		{Name: "object_id", In: "query", Required: false, Type: "integer", Description: "Narrow to one object, applied only alongside object_kind since an id alone means nothing across tables."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of the activity stream.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"activity": map[string]any{"type": "array", "items": activityEntrySchema},
				"_links":   linksSchema(),
			},
		}},
	},
}

// GetActivityEntry returns one recorded change.
var GetActivityEntry = Endpoint{
	Name:        "get_activity_entry",
	Method:      http.MethodGet,
	Pattern:     "/activity/{id}",
	Scope:       auth.ScopeAccessRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one activity entry",
	Description: "Returns one recorded change: who changed which managed object, and when.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The record's id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The activity entry.", Schema: activityEntrySchema},
		{Status: http.StatusNotFound, Description: "No record with that id.", Schema: errorSchema("")},
	},
}

// There is deliberately no create, update or delete endpoint here.
//
// The stream is append-only and its only writer is internal/access's
// audited store. An API that could write an entry would let a caller forge
// history; one that could delete an entry would let a caller erase their
// own. Both are exactly what an audit trail exists to prevent, so the
// absence of the routes is the enforcement, the same way the Jobs view
// carries no update or delete handler.
