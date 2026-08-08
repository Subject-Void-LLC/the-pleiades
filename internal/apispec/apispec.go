// Package apispec declares the control-plane HTTP API's route table as
// data, once, the way internal/clispec does for the CLI command tree and
// pkg/collection.Manifest does for the module catalog: cmd/controller's
// real router and tools/gendocs' generated OpenAPI document both build
// from this same Endpoints slice, so a route's method, pattern, required
// scope, and link relation can never drift between what the server
// actually serves and what a generated page claims it serves.
//
// Living under internal/ rather than inside cmd/controller is what lets
// tools/gendocs (a separate main package) read it too, the same reason
// internal/clispec does.
package apispec

import (
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// Param documents one path or query parameter an Endpoint reads.
type Param struct {
	Name        string
	In          string // "path" or "query"
	Required    bool
	Type        string
	Description string
}

// Response documents one status code an Endpoint can return. Schema is a
// JSON Schema fragment describing the response body, or nil for a
// response with no body (a 204) or a body this document does not
// describe as JSON (StreamJobLogs' text/event-stream body: see its own
// ContentType field instead).
type Response struct {
	Status      int
	Description string
	Schema      map[string]any
}

// Endpoint is one route's full documented shape: everything
// cmd/controller's real api.Route needs to serve it, plus everything
// tools/gendocs needs to describe it in the generated OpenAPI document
// and API reference page.
type Endpoint struct {
	// Name is a stable, human-readable key for this endpoint, used only
	// in generated output (an OpenAPI operationId); never part of the
	// URL.
	Name string

	Method  string
	Pattern string // relative to api.APIVersionPrefix, matching api.Route.Pattern
	Scope   auth.Scope
	Rel     auth.LinkRel

	Summary     string
	Description string

	Params []Param

	// RequestContentType and RequestSchema describe the request body, if
	// this endpoint reads one from the wire. Every endpoint in this
	// table today reads its input from path/query parameters instead
	// (see Params), so both are empty for all of them; the fields exist
	// so a future endpoint with a real JSON body does not need this
	// type extended.
	RequestContentType string
	RequestSchema      map[string]any

	// ResponseContentType overrides the default "application/json" a
	// Response with a non-nil Schema is assumed to carry. Set only by
	// StreamJobLogs, whose body is Server-Sent Events, not a JSON
	// document.
	ResponseContentType string

	Responses []Response
}

// Route builds the real api.Route cmd/controller mounts, pairing this
// Endpoint's documented Method/Pattern/Scope/Rel with h, the real handler
// method value. This is the one place an Endpoint and a live api.Route
// connect: nothing here can silently drift, because both come from the
// same struct fields.
func (e Endpoint) Route(h http.HandlerFunc) api.Route {
	return api.Route{Method: e.Method, Pattern: e.Pattern, Scope: e.Scope, Rel: e.Rel, Handler: h}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func linksSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "Hypermedia affordances available on this resource for the calling identity.",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rel":    map[string]any{"type": "string"},
				"href":   map[string]any{"type": "string"},
				"method": map[string]any{"type": "string"},
			},
		},
	}
}

func errorSchema(description string) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": description,
		"properties": map[string]any{
			"error": map[string]any{"type": "string"},
		},
	}
}

var jobTaskSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"device_id":   map[string]any{"type": "string"},
		"device_name": map[string]any{"type": "string"},
		"outcome":     map[string]any{"type": "string"},
		"reason":      map[string]any{"type": "string", "description": "Present only for a skipped or failed outcome."},
	},
}

var jobResponseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"job_id":     map[string]any{"type": "string", "format": "uuid"},
		"runbook_id": map[string]any{"type": "string"},
		"group_name": map[string]any{"type": "string"},
		"state":      map[string]any{"type": "string"},
		"dispatched": map[string]any{"type": "integer", "description": "Reads 0 until state reaches \"completed\", regardless of live fan-out progress."},
		"skipped":    map[string]any{"type": "integer"},
		"failed":     map[string]any{"type": "integer"},
		"tasks":      map[string]any{"type": "array", "items": jobTaskSchema},
		"_links":     linksSchema(),
	},
}

var deviceResponseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":           map[string]any{"type": "string"},
		"name":         map[string]any{"type": "string"},
		"state":        map[string]any{"type": "string", "description": "One of the eight lifecycle states; see docs/10-running-in-production.md."},
		"version":      map[string]any{"type": "integer"},
		"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"capabilities": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"executable":   map[string]any{"type": "boolean"},
		"_links":       linksSchema(),
	},
}

// DispatchRunbook is POST /jobs/dispatch: launch an asynchronous runbook
// dispatch.
var DispatchRunbook = Endpoint{
	Name:    "dispatch_runbook",
	Method:  http.MethodPost,
	Pattern: "/jobs/dispatch",
	Scope:   auth.ScopeRunbookExecute,
	Rel:     auth.RelExecute,
	Summary: "Dispatch a runbook asynchronously",
	Description: "Validates the request, resolves the runbook, persists a pending Job, and publishes " +
		"one job.requested event. Responds immediately; the actual per-device fan-out happens later, off " +
		"this request entirely. Poll GET /jobs/{id} for progress and final per-device tallies.",
	Params: []Param{
		{Name: "group", In: "query", Required: true, Type: "string", Description: "Inventory group name to target."},
		{Name: "runbook", In: "query", Required: true, Type: "string", Description: "Runbook ID to dispatch."},
	},
	Responses: []Response{
		{Status: http.StatusAccepted, Description: "The job was persisted and will fan out asynchronously.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": stringSchema("Always \"accepted\"."),
				"job_id": stringSchema("The server-generated job ID. Poll GET /jobs/{job_id} with it."),
				"_links": linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "Missing 'group' or 'runbook' query parameter.", Schema: errorSchema("")},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No runbook with that ID exists.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The job could not be persisted or published.", Schema: errorSchema("")},
	},
}

// GetJob is GET /jobs/{id}: read a job's current state and per-device
// task outcomes.
var GetJob = Endpoint{
	Name:    "get_job",
	Method:  http.MethodGet,
	Pattern: "/jobs/{id}",
	Scope:   auth.ScopeJobRead,
	Rel:     auth.RelSelf,
	Summary: "Get a job's current state",
	Description: "Reads a dispatch.Job together with every JobTask recorded against it so far. " +
		"Dispatched/skipped/failed tallies read 0 until state reaches \"completed\"; the tasks array is " +
		"where a caller reads live per-device progress before then.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The job ID, a UUID."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The job's current state.", Schema: jobResponseSchema},
		{Status: http.StatusBadRequest, Description: "id is not a UUID.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No job with that ID exists.", Schema: errorSchema("")},
	},
}

// StreamJobLogs is GET /jobs/{id}/logs: a Server-Sent Events stream of a
// job's live progress.
var StreamJobLogs = Endpoint{
	Name:    "stream_job_logs",
	Method:  http.MethodGet,
	Pattern: "/jobs/{id}/logs",
	Scope:   auth.ScopeJobRead,
	Rel:     auth.RelLogs,
	Summary: "Stream a job's live progress over Server-Sent Events",
	Description: "id is validated as a UUID before use: it is concatenated into a NATS subject, and an " +
		"unvalidated value could widen a wildcard subject to stream every job in the system. A fresh, " +
		"ephemeral JetStream consumer scoped to exactly this job ID is created per request, so each " +
		"concurrent viewer sees only the job it asked for. Each SSE frame's data is the raw JSON event " +
		"payload the engine publishes per task status change, best-effort masked through every secret " +
		"known at the moment that event was published (see docs/10-running-in-production.md's data " +
		"handling section for the precise, non-retroactive scope of that masking).",
	ResponseContentType: "text/event-stream",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The job ID, a UUID."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "An open SSE stream. Never terminates on its own; the client closes it."},
		{Status: http.StatusBadRequest, Description: "id is not a UUID.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The JetStream consumer could not be created.", Schema: errorSchema("")},
	},
}

// GetDevice is GET /inventory/devices/{name}: read one inventory device.
var GetDevice = Endpoint{
	Name:        "get_device",
	Method:      http.MethodGet,
	Pattern:     "/inventory/devices/{name}",
	Scope:       auth.ScopeInventoryRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one inventory device",
	Description: "Reads a device's current lifecycle state, version, tags, and capabilities.",
	Params: []Param{
		{Name: "name", In: "path", Required: true, Type: "string", Description: "The device's name."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The device.", Schema: deviceResponseSchema},
		{Status: http.StatusBadRequest, Description: "name is empty, too long, or contains a control character.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No device with that name exists.", Schema: errorSchema("")},
	},
}

// DeleteDevice is DELETE /inventory/devices/{name}: retire one inventory
// device.
var DeleteDevice = Endpoint{
	Name:    "delete_device",
	Method:  http.MethodDelete,
	Pattern: "/inventory/devices/{name}",
	Scope:   auth.ScopeInventoryWrite,
	Rel:     auth.RelDelete,
	Summary: "Retire an inventory device",
	Description: "Moves the device to the decommissioning lifecycle state (\"retires\" it); this is not " +
		"a hard delete of its history.",
	Params: []Param{
		{Name: "name", In: "path", Required: true, Type: "string", Description: "The device's name."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "The device was retired."},
		{Status: http.StatusBadRequest, Description: "name is empty, too long, or contains a control character.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No device with that name exists.", Schema: errorSchema("")},
	},
}

// Endpoints is every documented endpoint, in the same order
// cmd/controller/main.go registers them.
var Endpoints = []Endpoint{
	DispatchRunbook,
	GetJob,
	StreamJobLogs,
	GetDevice,
	DeleteDevice,
}
