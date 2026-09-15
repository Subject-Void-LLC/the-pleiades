// Package apispec declares the control-plane HTTP API's route table as
// data, once, the way internal/clispec does for the CLI command tree and
// pkg/collection.Manifest does for the module catalog: cmd/controller's
// real router and tools/gendocs' generated OpenAPI document both build
// from the Endpoint values declared here, so a route present in both
// cannot drift in its method, pattern, required scope, or link relation.
// Route below is the whole mechanism: the router receives those four
// fields copied off the same value the generated document renders.
//
// Set membership is a different question, and Routes below is what
// answers it. This package used to document the gap rather than close it:
// only tools/gendocs ranged over Endpoints, while cmd/controller named
// each Endpoint one at a time in its own hand-written Routes table, so an
// Endpoint added to this slice and never registered there built clean,
// vetted clean, and tripped no test, while the generated document
// advertised a route the server did not serve.
//
// Routes implements exactly the fix that gap description called for --
// build the route table by ranging over Endpoints, pair each one with a
// handler by Name, and refuse to start if any Endpoint has none -- and
// adds the reverse check, so a handler registered under a name no
// Endpoint declares is refused too. Phase 19's web UI computes which
// buttons to render from these same Endpoint values, which turns an
// unmounted Endpoint from a documentation defect into a rendered control
// that 404s, so the check became load-bearing rather than tidy.
//
// Living under internal/ rather than inside cmd/controller is what lets
// tools/gendocs (a separate main package) read it too, the same reason
// internal/clispec does.
package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
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
		"result": map[string]any{"type": "string", "enum": []string{"succeeded", "failed"},
			"description": "What the Runner reported once the runbook ran on this device, as distinct from outcome above, which is whether the fan-out handed it off. Absent for a device that was skipped, and absent for a dispatched device that has not reported back yet, which is what a job still in \"running\" is waiting on."},
		"result_reason": map[string]any{"type": "string", "description": "Present only for a failed result."},
		"finished_at":   map[string]any{"type": "string", "format": "date-time", "description": "When this device reported back. Absent until it does."},
	},
}

var jobResponseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"job_id":         map[string]any{"type": "string", "format": "uuid"},
		"runbook_id":     map[string]any{"type": "string"},
		"state":          map[string]any{"type": "string"},
		"template":       map[string]any{"type": "integer", "description": "The saved definition this job was launched from."},
		"template_name":  stringSchema("That template's name as it was at launch. Captured rather than resolved live: a job's history has to outlive the template."),
		"inventory":      map[string]any{"type": "integer", "description": "The device set it targeted."},
		"organization":   map[string]any{"type": "integer", "description": "The tenant it belongs to, inherited from that inventory."},
		"kind":           stringSchema("Which registered launch kind ran, and therefore which execution adapter handled it."),
		"failure_reason": stringSchema("Why a failed job could not run. Empty for every other state."),
		"dispatched":     map[string]any{"type": "integer", "description": "Reads 0 until state reaches \"completed\", regardless of live fan-out progress."},
		"skipped":        map[string]any{"type": "integer"},
		"failed":         map[string]any{"type": "integer"},
		"tasks":          map[string]any{"type": "array", "items": jobTaskSchema},
		"_links":         linksSchema(),
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

// CancelJob is POST /jobs/{id}/cancel: stop a job that is still running.
//
// It takes runbook:execute, the scope that starts a run, rather than a
// job:write of its own. Stopping a run and starting one are the two ends
// of the same authority, and RelaunchJob on this same resource already
// reads that way.
var CancelJob = Endpoint{
	Name:    "cancel_job",
	Method:  http.MethodPost,
	Pattern: "/jobs/{id}/cancel",
	Scope:   auth.ScopeRunbookExecute,
	Rel:     auth.RelCancel,
	Summary: "Cancel a running job",
	Description: "Stops a job and returns at once. What this guarantees is the record and the fan-out: the " +
		"job settles to \"canceled\" naming whoever stopped it, and no device the job has not already reached " +
		"is dispatched to. What it cannot guarantee is work already running on a device. That is signalled " +
		"best-effort to whichever Runner holds it, a task declaring itself un-interruptible runs to " +
		"completion by design, and one device may already have been dispatched to in the instant the cancel " +
		"landed. A job that has already finished is a 409 rather than a success, because reporting success " +
		"for having stopped nothing would tell a caller it had done something it had not.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The job ID, a UUID."},
	},
	Responses: []Response{
		{Status: http.StatusAccepted, Description: "The job was canceled. It is returned as it now stands; poll it for the final tallies, which the stopping fan-out records.", Schema: jobResponseSchema},
		{Status: http.StatusBadRequest, Description: "id is not a UUID.", Schema: errorSchema("")},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "The job has already finished, so there was nothing to stop.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No job with that ID exists.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The job could not be canceled.", Schema: errorSchema("")},
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

var runbookResponseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id": map[string]any{"type": "string"},
		"required_capabilities": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Deduplicated union of what every task in this runbook requires of its target.",
		},
		"interruptible": map[string]any{"type": "boolean"},
		"_links":        linksSchema(),
	},
}

// ListRunbooks is GET /runbooks: browse the runbook catalog.
var ListRunbooks = Endpoint{
	Name:    "list_runbooks",
	Method:  http.MethodGet,
	Pattern: "/runbooks",
	Scope:   auth.ScopeRunbookRead,
	Rel:     auth.RelCollection,
	Summary: "List available runbooks",
	Description: "Returns every runbook id this control plane can resolve, sorted. Ids only: compiling " +
		"the whole library to render a list of names would make opening the catalog cost more the more " +
		"automation an organization has written. Read GET /runbooks/{id} for one runbook's requirements.",
	Responses: []Response{
		{Status: http.StatusOK, Description: "The catalog.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"runbooks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"_links":   linksSchema(),
			},
		}},
		{Status: http.StatusInternalServerError, Description: "The runbook source could not be listed.", Schema: errorSchema("")},
	},
}

// GetRunbook is GET /runbooks/{id}: read one runbook's requirements.
var GetRunbook = Endpoint{
	Name:    "get_runbook",
	Method:  http.MethodGet,
	Pattern: "/runbooks/{id}",
	Scope:   auth.ScopeRunbookRead,
	Rel:     auth.RelSelf,
	Summary: "Get one runbook's compiled requirements",
	Description: "Compiles the runbook and reports the deduplicated capabilities its tasks require of a " +
		"target, plus whether the engine may interrupt it. This is what lets a caller tell, before " +
		"dispatching, whether a group can actually run it.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The runbook id: 1-64 characters, letters, digits, hyphens and underscores."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The runbook.", Schema: runbookResponseSchema},
		{Status: http.StatusBadRequest, Description: "id is malformed, or the runbook does not compile.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No runbook with that id exists.", Schema: errorSchema("")},
	},
}

// jobListResponseSchema is a page of job summaries plus the cursor that
// fetches the next one.
var jobListResponseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"jobs": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"job_id":        map[string]any{"type": "string"},
				"runbook_id":    map[string]any{"type": "string"},
				"state":         stringSchema("One of \"pending\", \"fanning_out\", \"completed\", or \"failed\"."),
				"template_name": stringSchema("The saved definition this job was launched from."),
				"kind":          stringSchema("Which registered launch kind ran."),
				"actor":         stringSchema("The identity subject that requested the job."),
				"dispatched":    map[string]any{"type": "integer"},
				"skipped":       map[string]any{"type": "integer"},
				"failed":        map[string]any{"type": "integer"},
				"created_at":    stringSchema("RFC 3339 timestamp."),
			},
		}},
		"next_cursor": stringSchema("Opaque keyset cursor. Pass it back as ?after= to fetch the " +
			"next page. Empty when this is the last page."),
		"_links": linksSchema(),
	},
}

// ListJobs is GET /jobs: page through dispatch history, newest first.
var ListJobs = Endpoint{
	Name:    "list_jobs",
	Method:  http.MethodGet,
	Pattern: "/jobs",
	Scope:   auth.ScopeJobRead,
	Rel:     auth.RelCollection,
	Summary: "List dispatch jobs",
	Description: "Returns a bounded page of jobs, newest first, ordered on the job id. Job ids are " +
		"UUIDv7, so that ordering is chronological and the id doubles as a keyset cursor with no second " +
		"index and no tiebreaker. Per-device task outcomes are not included; read GET /jobs/{id} for " +
		"those. Tallies read 0 until a job's state reaches \"completed\".",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "string", Description: "Keyset cursor from a previous page's next_cursor. A job id, so a UUID."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum jobs to return. Defaults to 50, capped at 200."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of jobs.", Schema: jobListResponseSchema},
		{Status: http.StatusBadRequest, Description: "limit is not a positive integer, or after is not a UUID.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The job store could not be read.", Schema: errorSchema("")},
	},
}

// deviceListResponseSchema is a page of devices plus the cursor that
// fetches the next one.
var deviceListResponseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"devices": map[string]any{"type": "array", "items": deviceResponseSchema},
		"next_cursor": stringSchema("Opaque keyset cursor. Pass it back as ?after= to fetch the " +
			"next page. Empty when this is the last page."),
		"_links": linksSchema(),
	},
}

// deviceWriteSchema is the JSON body create and update accept.
//
// It carries no properties field, and that omission is the same hardening
// decision deviceDTO makes on the way out: cmd/controller installs
// crypto.DeviceEnvelopePropertiesInterceptor, so the property bag holds
// decrypted enable secrets and API keys, and the masking ruleset that
// would make them safe to serve belongs to a phase that does not exist
// yet (see docs/12-web-ui.md, which records the same omission on the web
// UI's device form for the same reason). An endpoint that
// wrote properties would also have to read them back to be usable, and
// that is the surface being deliberately deferred.
var deviceWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "type"},
	"properties": map[string]any{
		"name":  stringSchema("The device's unique name."),
		"type":  stringSchema("A registered device type, e.g. \"linux_server\"."),
		"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"state": stringSchema("One of the eight lifecycle states. Defaults to \"active\" on create."),
	},
}

// ListDevices is GET /inventory/devices: page through the inventory.
var ListDevices = Endpoint{
	Name:    "list_devices",
	Method:  http.MethodGet,
	Pattern: "/inventory/devices",
	Scope:   auth.ScopeInventoryRead,
	Rel:     auth.RelCollection,
	Summary: "List inventory devices",
	Description: "Streams a bounded page of devices in DeviceID order. Paging is keyset rather than " +
		"offset: an offset over a table being written to skips and repeats rows, which on an inventory " +
		"list means a device silently missing from a page while another is shown twice. Pass the " +
		"previous page's next_cursor as ?after= to continue.",
	Params: []Param{
		{Name: "group", In: "query", Required: false, Type: "string", Description: "Restrict to devices in this inventory group."},
		{Name: "after", In: "query", Required: false, Type: "string", Description: "Keyset cursor from a previous page's next_cursor."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum devices to return. Defaults to 50, capped at 200."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of devices.", Schema: deviceListResponseSchema},
		{Status: http.StatusBadRequest, Description: "limit is not a positive integer, or a parameter contains a control character.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The inventory could not be read.", Schema: errorSchema("")},
	},
}

// CreateDevice is POST /inventory/devices: onboard a new device.
var CreateDevice = Endpoint{
	Name:    "create_device",
	Method:  http.MethodPost,
	Pattern: "/inventory/devices",
	Scope:   auth.ScopeInventoryWrite,
	Rel:     auth.RelCreate,
	Summary: "Create an inventory device",
	Description: "Onboards a device the platform did not discover through a sync plugin. Never an " +
		"upsert: a name already in the inventory is a conflict rather than a silent overwrite, so a " +
		"caller can always tell a first-time onboard from a re-sync.",
	RequestContentType: "application/json",
	RequestSchema:      deviceWriteSchema,
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created device.", Schema: deviceResponseSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or name or type is missing or unusable.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "A device with that name already exists, or the inventory is read-only.", Schema: errorSchema("")},
		{Status: http.StatusUnprocessableEntity, Description: "type names no registered device type.", Schema: errorSchema("")},
	},
}

// UpdateDevice is PATCH /inventory/devices/{name}: modify a device in
// place.
var UpdateDevice = Endpoint{
	Name:    "update_device",
	Method:  http.MethodPatch,
	Pattern: "/inventory/devices/{name}",
	Scope:   auth.ScopeInventoryWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update an inventory device",
	Description: "Applies tag and lifecycle-state changes to an existing device. The write is guarded " +
		"by the stored version token, so two callers editing the same device concurrently cannot lose " +
		"one another's change: the later write is refused with 409 and must reload and reapply.",
	Params: []Param{
		{Name: "name", In: "path", Required: true, Type: "string", Description: "The device's name."},
	},
	RequestContentType: "application/json",
	RequestSchema:      deviceWriteSchema,
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated device.", Schema: deviceResponseSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or name is unusable.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No device with that name exists.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "The device was modified concurrently, or the inventory is read-only.", Schema: errorSchema("")},
		{Status: http.StatusUnprocessableEntity, Description: "state names no known lifecycle state.", Schema: errorSchema("")},
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

// inventorySchema describes one Inventory: a named, shareable set of
// devices a runbook can be dispatched against.
//
// It carries counts rather than the members themselves. A listing exists to
// answer "which inventories are there and how big are they", and inlining
// every device id would make opening a list of twenty inventories cost
// twenty fleet reads for data nothing on that page renders.
var inventorySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":           map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"name":         stringSchema("Unique within its organization, not globally."),
		"description":  stringSchema("What this inventory is for."),
		"organization": map[string]any{"type": "integer", "description": "The tenancy boundary this inventory belongs to."},
		"owner":        stringSchema("The subject that created it. Authorship, never authority."),
		"group_count":  map[string]any{"type": "integer"},
		"device_count": map[string]any{"type": "integer", "description": "Devices attached directly, not counting those reached through a group."},
		"_links":       linksSchema(),
	},
}

var inventoryWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "organization"},
	"properties": map[string]any{
		"name":         stringSchema("Unique within its organization."),
		"description":  stringSchema("What this inventory is for."),
		"organization": map[string]any{"type": "integer", "description": "Required. An inventory belonging to no organization could be resolved against no organization scope."},
		"groups":       map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Group ids this inventory contains."},
		"devices":      map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Device ids attached directly, with no intervening group."},
	},
}

// ListInventories is GET /inventories.
var ListInventories = Endpoint{
	Name:    "list_inventories",
	Method:  http.MethodGet,
	Pattern: "/inventories",
	Scope:   auth.ScopeInventoryRead,
	Rel:     auth.RelCollection,
	Summary: "List inventories",
	Description: "Returns the inventories this caller may reach, keyset-paged. An inventory is a named, " +
		"shareable set of devices; sharing one with another team is a role binding at inventory scope, " +
		"not a field on this resource.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the last id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum inventories to return."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name filter."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of inventories.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"inventories": map[string]any{"type": "array", "items": inventorySchema},
				"_links":      linksSchema(),
			},
		}},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller lacks inventory:read.", Schema: errorSchema("")},
	},
}

// GetInventory is GET /inventories/{id}.
var GetInventory = Endpoint{
	Name:        "get_inventory",
	Method:      http.MethodGet,
	Pattern:     "/inventories/{id}",
	Scope:       auth.ScopeInventoryRead,
	Rel:         auth.RelSelf,
	Summary:     "Read one inventory",
	Description: "Returns one inventory together with the ids of the groups and devices it contains.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The inventory's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The inventory.", Schema: inventorySchema},
		{Status: http.StatusNotFound, Description: "No inventory with that id, or none this caller may reach.", Schema: errorSchema("")},
	},
}

// CreateInventory is POST /inventories.
var CreateInventory = Endpoint{
	Name:        "create_inventory",
	Method:      http.MethodPost,
	Pattern:     "/inventories",
	Scope:       auth.ScopeInventoryWrite,
	Rel:         auth.RelCreate,
	Summary:     "Create an inventory",
	Description: "Creates a named set of devices within one organization. The name must be unique in that organization.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created inventory.", Schema: inventorySchema},
		{Status: http.StatusBadRequest, Description: "Missing name or organization.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken in that organization.", Schema: errorSchema("")},
	},
	RequestSchema: inventoryWriteSchema,
}

// UpdateInventory is PATCH /inventories/{id}.
var UpdateInventory = Endpoint{
	Name:    "update_inventory",
	Method:  http.MethodPatch,
	Pattern: "/inventories/{id}",
	Scope:   auth.ScopeInventoryWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update an inventory",
	Description: "Replaces an inventory's name, description and membership. The organization is not updatable: " +
		"moving one between tenants would silently re-scope every role binding pointing at it, which is a " +
		"migration rather than an edit.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The inventory's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated inventory.", Schema: inventorySchema},
		{Status: http.StatusNotFound, Description: "No inventory with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken in that organization.", Schema: errorSchema("")},
	},
	RequestSchema: inventoryWriteSchema,
}

// DeleteInventory is DELETE /inventories/{id}.
var DeleteInventory = Endpoint{
	Name:    "delete_inventory",
	Method:  http.MethodDelete,
	Pattern: "/inventories/{id}",
	Scope:   auth.ScopeInventoryWrite,
	Rel:     auth.RelDelete,
	Summary: "Delete an inventory",
	Description: "Removes the inventory. The devices and groups it referenced are untouched: an inventory is a " +
		"view onto the fleet, not its owner, so deleting a shared collection never deletes the hosts in it.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The inventory's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusNotFound, Description: "No inventory with that id.", Schema: errorSchema("")},
	},
}

// announcementSchema describes one operator announcement.
var announcementSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":     map[string]any{"type": "integer"},
		"title":  stringSchema("The headline."),
		"body":   stringSchema("The message."),
		"level":  stringSchema("One of info, changed, warning, critical."),
		"author": stringSchema("Captured at write time and immutable."),
		"organization": map[string]any{
			"type":        "integer",
			"description": "Absent means system-wide, shown to every tenant.",
		},
		"starts_at": stringSchema("RFC 3339. Absent means already showing."),
		"ends_at":   stringSchema("RFC 3339. Absent means until manually retired."),
		"_links":    linksSchema(),
	},
}

var announcementWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"title", "body"},
	"properties": map[string]any{
		"title":        stringSchema("The headline."),
		"body":         stringSchema("The message."),
		"level":        stringSchema("One of info, changed, warning, critical. Defaults to info."),
		"organization": map[string]any{"type": "integer", "description": "Omit for a system-wide announcement."},
		"starts_at":    stringSchema("RFC 3339. Omit to show immediately."),
		"ends_at":      stringSchema("RFC 3339. Omit for no expiry."),
	},
}

// ListAnnouncements is GET /announcements.
var ListAnnouncements = Endpoint{
	Name:    "list_announcements",
	Method:  http.MethodGet,
	Pattern: "/announcements",
	Scope:   auth.ScopeAnnouncementRead,
	Rel:     auth.RelCollection,
	Summary: "List operator announcements",
	Description: "Returns announcements this caller may see, newest first. System-wide announcements are " +
		"returned to everybody; tenant-scoped ones only to callers who can reach that tenant. By default " +
		"only announcements live right now are returned.",
	Params: []Param{
		{Name: "all", In: "query", Required: false, Type: "boolean", Description: "Include announcements outside their active window. For managing them, not for reading them."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum announcements to return."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The announcements.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"announcements": map[string]any{"type": "array", "items": announcementSchema},
				"_links":        linksSchema(),
			},
		}},
	},
}

// CreateAnnouncement is POST /announcements.
var CreateAnnouncement = Endpoint{
	Name:        "create_announcement",
	Method:      http.MethodPost,
	Pattern:     "/announcements",
	Scope:       auth.ScopeAnnouncementWrite,
	Rel:         auth.RelCreate,
	Summary:     "Post an announcement",
	Description: "Puts a message in front of every operator who can see it. The author is taken from the caller's identity and cannot be set.",
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created announcement.", Schema: announcementSchema},
		{Status: http.StatusBadRequest, Description: "Missing title or body.", Schema: errorSchema("")},
	},
	RequestSchema: announcementWriteSchema,
}

// UpdateAnnouncement is PATCH /announcements/{id}.
var UpdateAnnouncement = Endpoint{
	Name:        "update_announcement",
	Method:      http.MethodPatch,
	Pattern:     "/announcements/{id}",
	Scope:       auth.ScopeAnnouncementWrite,
	Rel:         auth.RelUpdate,
	Summary:     "Edit an announcement",
	Description: "Updates the title, body, level or active window. The author is immutable: an attribution somebody can rewrite is one nobody can rely on.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The announcement's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated announcement.", Schema: announcementSchema},
		{Status: http.StatusNotFound, Description: "No announcement with that id.", Schema: errorSchema("")},
	},
	RequestSchema: announcementWriteSchema,
}

// DeleteAnnouncement is DELETE /announcements/{id}.
var DeleteAnnouncement = Endpoint{
	Name:        "delete_announcement",
	Method:      http.MethodDelete,
	Pattern:     "/announcements/{id}",
	Scope:       auth.ScopeAnnouncementWrite,
	Rel:         auth.RelDelete,
	Summary:     "Retire an announcement",
	Description: "Removes it. A hard delete: a retired notice has no audience and no audit value, and a lingering one is another condition every future query has to exclude.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The announcement's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusNotFound, Description: "No announcement with that id.", Schema: errorSchema("")},
	},
}

// Endpoints is every documented endpoint, in the same order
// cmd/controller/main.go registers them.
var Endpoints = []Endpoint{
	ListJobs,
	GetJob,
	StreamJobLogs,
	CancelJob,
	RelaunchJob,
	ListTemplates,
	GetTemplate,
	CreateTemplate,
	UpdateTemplate,
	DeleteTemplate,
	CopyTemplate,
	LaunchTemplate,
	ListTemplateConfigs,
	CreateTemplateConfig,
	ListTemplateCredentials,
	SetTemplateCredentials,
	ListCredentialInputSources,
	SetCredentialInputSources,
	ListSchedules,
	GetSchedule,
	CreateSchedule,
	UpdateSchedule,
	DeleteSchedule,
	ListScheduleOccurrences,
	PreviewSchedule,
	ListZoneinfo,
	ListCredentialTypes,
	GetCredentialType,
	CreateCredentialType,
	UpdateCredentialType,
	DeleteCredentialType,
	TestCredentialType,
	SetCredentialTypeInputs,
	SetCredentialTypeInjectors,
	ListProjects,
	GetProject,
	CreateProject,
	UpdateProject,
	DeleteProject,
	SyncProject,
	CancelProjectSync,
	StreamProjectSyncLogs,

	ListCredentials,
	GetCredential,
	CreateCredential,
	UpdateCredential,
	DeleteCredentialEndpoint,
	ListDevices,
	CreateDevice,
	GetDevice,
	UpdateDevice,
	DeleteDevice,
	ListRunbooks,
	GetRunbook,
	ListInventories,
	GetInventory,
	CreateInventory,
	UpdateInventory,
	DeleteInventory,
	ListAnnouncements,
	CreateAnnouncement,
	UpdateAnnouncement,
	DeleteAnnouncement,
	ListOrganizations,
	GetOrganization,
	CreateOrganization,
	UpdateOrganization,
	DeleteOrganization,
	AttestOrganization,
	ListTeams,
	GetTeam,
	CreateTeam,
	UpdateTeam,
	DeleteTeam,
	AttestTeam,
	ListContacts,
	GetContact,
	CreateContact,
	UpdateContact,
	DeleteContact,
	ListUsers,
	GetUser,
	CreateUser,
	UpdateUser,
	DeleteUser,
	ListBindings,
	GetBinding,
	CreateBinding,
	UpdateBinding,
	DeleteBinding,
	ListActivity,
	GetActivityEntry,
}
