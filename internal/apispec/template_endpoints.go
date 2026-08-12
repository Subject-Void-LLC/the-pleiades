package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// This file declares the Template surface: the saved, reusable definitions
// of what this platform runs, where, and how.
//
// The scopes are split along the line the resource itself is split along.
// Reading and administering a template is template:read / template:write;
// launching one is runbook:execute, the same scope every other dispatch
// path checks. That is what keeps the two privileges separable: an operator
// who may run what somebody else saved cannot edit what it does, and an
// author who may edit it does not thereby acquire the right to run it.

// surveyQuestionSchema is one question a launching operator is asked.
var surveyQuestionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"variable": stringSchema("The extra-variable name the answer is written to."),
		"label":    stringSchema("What the form asks."),
		"help":     stringSchema("The line under the label."),
		"type": stringSchema("One of text, textarea, password, integer, float, multiplechoice, multiselect. " +
			"Ansible's own question types, so a survey imported from AWX means the same thing here."),
		"required": map[string]any{"type": "boolean", "description": "Refuses a launch that leaves it blank."},
		"default":  stringSchema("Used when an answer is absent and the question is not required."),
		"choices":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The permitted values for the two choice types."},
		"min":      map[string]any{"type": "integer", "description": "Bounds a numeric answer, or the length of a text one. Zero means unbounded."},
		"max":      map[string]any{"type": "integer", "description": "See min."},
	},
}

var surveySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"enabled": map[string]any{
			"type": "boolean",
			"description": "Whether the survey prompts. Separate from having no questions: a survey written and " +
				"turned off says something different from no survey at all.",
		},
		"questions": map[string]any{"type": "array", "items": surveyQuestionSchema},
	},
}

// templateSchema is the wire projection of one template.
var templateSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":                map[string]any{"type": "integer", "description": "Stable numeric identifier."},
		"name":              stringSchema("Unique within its organization, not globally."),
		"description":       stringSchema("What this template is for."),
		"kind":              stringSchema("The registry key of what this runs: runbook, playbook."),
		"kind_label":        stringSchema("That kind's display label. Absent when this controller no longer registers the kind."),
		"definition":        stringSchema("The reference the kind resolves: a runbook id, a playbook path."),
		"inventory":         map[string]any{"type": "integer", "description": "The device set this runs against."},
		"inventory_name":    stringSchema("That inventory's name, carried so a list does not render a bare foreign key."),
		"organization":      map[string]any{"type": "integer", "description": "The tenancy boundary, derived from the inventory and never submitted."},
		"organization_name": stringSchema("That organization's name."),
		"defaults": map[string]any{
			"type":        "object",
			"description": "How to run it: the values this template was saved with, keyed by launch field name.",
		},
		"prompts": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
			"description": "The fields a launch may override. Everything else is locked to what defaults says, " +
				"and a launch supplying a locked field is told so by name rather than having it applied or dropped.",
		},
		"survey":             surveySchema,
		"allow_simultaneous": map[string]any{"type": "boolean", "description": "Permits more than one job from this template to run at once."},
		"required_capabilities": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "A plan-time hint recorded when the template was saved. The executor acquires capabilities for real at run time.",
		},
		"_links": linksSchema(),
	},
}

var templateWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "kind", "definition", "inventory"},
	"properties": map[string]any{
		"name":        stringSchema("Unique within the organization the inventory belongs to."),
		"description": stringSchema("What this template is for."),
		"kind":        stringSchema("A registered kind: runbook, playbook. Read on create, ignored on update."),
		"definition":  stringSchema("A runbook id or a playbook path, whichever the kind resolves. Read on create, ignored on update."),
		"inventory": map[string]any{
			"type": "integer",
			"description": "The device set to run against. Required, and it is what gives a launched job its " +
				"organization. Read on create, ignored on update: re-pointing a saved definition at another " +
				"fleet while it keeps its name, its grants and its job history is a copy, not an edit.",
		},
		"defaults": map[string]any{"type": "object", "description": "The values to save this template with, keyed by launch field name."},
		"prompts": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Which of those fields a launch may override. A field this kind does not declare is refused rather than stored.",
		},
		"survey":             surveySchema,
		"allow_simultaneous": map[string]any{"type": "boolean"},
		"required_capabilities": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
			"description": "An optional plan-time hint. This API does not compute it: read GET /runbooks/{id} for a " +
				"runbook's real compiled requirements and submit those.",
		},
	},
}

// savedConfigSchema is one stored bundle of launch-time overrides.
var savedConfigSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":       map[string]any{"type": "integer"},
		"template": map[string]any{"type": "integer", "description": "The template it belongs to. A configuration is only meaningful against that template's declared prompts."},
		"name":     stringSchema("What a reader picks it by. Absent for the anonymous configuration a launch stores so it can be relaunched."),
		"fields":   map[string]any{"type": "object", "description": "The launch overrides, keyed by launch field name."},
		"answers": map[string]any{
			"type": "object",
			"description": "The survey answers. An answer to a password question reads as \"$encrypted$\" rather than " +
				"its value: it is encrypted at rest and redacted on the wire, which are separate controls for separate exposures.",
		},
		"_links": linksSchema(),
	},
}

// launchAcceptedSchema is what a launch and a relaunch both answer with.
var launchAcceptedSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status": stringSchema("Always \"accepted\"."),
		"job_id": stringSchema("The server-generated job id. Poll GET /jobs/{job_id} with it."),
		"ignored_fields": map[string]any{
			"type": "array",
			"description": "Every value the caller supplied that was not applied. Always present, empty when " +
				"everything was applied: a key that vanished when the list was empty would make \"nothing was " +
				"refused\" indistinguishable from \"this server does not report refusals\".",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   stringSchema("The field the caller supplied."),
					"layer":  stringSchema("Where it was supplied: the saved configuration, or this launch."),
					"reason": stringSchema("Why it was not applied, in words a caller can act on."),
				},
			},
		},
		"_links": linksSchema(),
	},
}

// ListTemplates is GET /templates.
var ListTemplates = Endpoint{
	Name:    "list_templates",
	Method:  http.MethodGet,
	Pattern: "/templates",
	Scope:   auth.ScopeTemplateRead,
	Rel:     auth.RelCollection,
	Summary: "List templates",
	Description: "Returns a page of saved definitions, oldest id first. The survey is not included: a list " +
		"renders a template's name, kind and inventory, and loading every question for every row would be a " +
		"query per page for data no column shows. Read GET /templates/{id} for one template's survey.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "integer", Description: "Keyset cursor: the highest id of the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum templates to return. Defaults to 50, capped at 200."},
		{Name: "q", In: "query", Required: false, Type: "string", Description: "Case-insensitive name filter."},
		{Name: "organization", In: "query", Required: false, Type: "integer", Description: "Narrow to one tenant's templates."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of templates.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"templates": map[string]any{"type": "array", "items": templateSchema},
				"_links":    linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "after, limit or organization is not a valid integer.", Schema: errorSchema("")},
	},
}

// GetTemplate is GET /templates/{id}.
var GetTemplate = Endpoint{
	Name:        "get_template",
	Method:      http.MethodGet,
	Pattern:     "/templates/{id}",
	Scope:       auth.ScopeTemplateRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one template",
	Description: "Returns one template with its survey, its defaults, and the set of fields a launch may override.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The template.", Schema: templateSchema},
		{Status: http.StatusBadRequest, Description: "id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
	},
}

// CreateTemplate is POST /templates.
var CreateTemplate = Endpoint{
	Name:    "create_template",
	Method:  http.MethodPost,
	Pattern: "/templates",
	Scope:   auth.ScopeTemplateWrite,
	Rel:     auth.RelCreate,
	Summary: "Create a template",
	Description: "Saves what to run, where to run it, and how. The organization is derived from the named " +
		"inventory's own organization and cannot be submitted: a caller who could set it directly could tag " +
		"their jobs with somebody else's tenant. A template that could not be launched by anybody is refused " +
		"at the write rather than at the launch.",
	RequestContentType: "application/json",
	RequestSchema:      templateWriteSchema,
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created template.", Schema: templateSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, names no registered kind, or declares a default or a prompt the kind has no field for.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The named inventory belongs to another organization.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No inventory with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "A template with that name already exists in that organization.", Schema: errorSchema("")},
	},
}

// UpdateTemplate is PATCH /templates/{id}.
var UpdateTemplate = Endpoint{
	Name:    "update_template",
	Method:  http.MethodPatch,
	Pattern: "/templates/{id}",
	Scope:   auth.ScopeTemplateWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a template",
	Description: "Replaces a template's name, description, defaults, prompts, survey and flags. The kind, the " +
		"definition and the inventory are not updatable: re-pointing a template at different code or a " +
		"different fleet, while it keeps its name, its access grants and its job history, is how a reviewed " +
		"thing quietly becomes an unreviewed one. Copy it instead, which leaves two legible records.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema:      templateWriteSchema,
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated template.", Schema: templateSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or declares a default or a prompt the kind has no field for.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken in this organization.", Schema: errorSchema("")},
	},
}

// DeleteTemplate is DELETE /templates/{id}.
var DeleteTemplate = Endpoint{
	Name:    "delete_template",
	Method:  http.MethodDelete,
	Pattern: "/templates/{id}",
	Scope:   auth.ScopeTemplateWrite,
	Rel:     auth.RelDelete,
	Summary: "Delete a template",
	Description: "Removes the template, its survey and its saved configurations. The jobs it launched are " +
		"untouched: a job is a historical record, and \"what did this template run\" is precisely the question " +
		"somebody has once it is gone.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusBadRequest, Description: "id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
	},
}

// CopyTemplate is POST /templates/{id}/copy.
var CopyTemplate = Endpoint{
	Name:    "copy_template",
	Method:  http.MethodPost,
	Pattern: "/templates/{id}/copy",
	Scope:   auth.ScopeTemplateWrite,
	Rel:     auth.RelCopy,
	Summary: "Copy a template",
	Description: "Duplicates a template under a new name, defaulting to \"<name> (copy)\". The survey travels " +
		"with it, because the questions are part of how the template runs; the saved launch configurations do " +
		"not, because those are one operator's answers, secrets among them, and duplicating them would move a " +
		"stored password onto an object with its own separate access grants.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template to copy."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": stringSchema("The copy's name. Omit for \"<name> (copy)\".")},
	},
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The new template.", Schema: templateSchema},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "That name is already taken in this organization.", Schema: errorSchema("")},
	},
}

// LaunchTemplate is POST /templates/{id}/launch.
var LaunchTemplate = Endpoint{
	Name:    "launch_template",
	Method:  http.MethodPost,
	Pattern: "/templates/{id}/launch",
	Scope:   auth.ScopeRunbookExecute,
	Rel:     auth.RelExecute,
	Summary: "Launch a template",
	Description: "Runs a saved definition, applying whichever of its fields the template opened and reporting " +
		"every value it did not. Responds 202 immediately; the per-device fan-out happens later, off this " +
		"request entirely, and the job carries the template's organization so a dispatch is tenanted rather " +
		"than unowned. A locked field is reported in ignored_fields rather than failing the launch: applying " +
		"it would be a privilege escalation, dropping it silently would be a lie about what ran, and refusing " +
		"the whole launch would make a template author's decision look like the operator's mistake. A survey " +
		"answer that violates its own schema is the one thing that does fail, because there the run would " +
		"proceed with a variable the author said was unacceptable.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":        "object",
		"description": "Every field is optional: launching a template that opens nothing is a POST with no body.",
		"properties": map[string]any{
			"config":    map[string]any{"type": "integer", "description": "A stored launch configuration to launch from, applied beneath overrides. Must belong to this template."},
			"overrides": map[string]any{"type": "object", "description": "This launch's own values, keyed by launch field name."},
			"answers":   map[string]any{"type": "object", "description": "This launch's survey answers, keyed by variable."},
		},
	},
	Responses: []Response{
		{Status: http.StatusAccepted, Description: "The job was persisted and will fan out asynchronously.", Schema: launchAcceptedSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or id is not a positive integer.", Schema: errorSchema("")},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id, or no such saved configuration on it.", Schema: errorSchema("")},
		{Status: http.StatusUnprocessableEntity, Description: "A survey answer violates its question's schema, or this controller does not register the template's kind.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The job could not be persisted or published.", Schema: errorSchema("")},
	},
}

// ListTemplateConfigs is GET /templates/{id}/configs.
var ListTemplateConfigs = Endpoint{
	Name:    "list_template_configs",
	Method:  http.MethodGet,
	Pattern: "/templates/{id}/configs",
	Scope:   auth.ScopeTemplateRead,
	Rel:     auth.RelCollection,
	Summary: "List a template's saved launch configurations",
	Description: "Returns the stored override bundles for one template: the named ones somebody saved on " +
		"purpose, and the anonymous one each launch records so it can be relaunched. Password answers read as " +
		"a redaction marker rather than their value.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The saved configurations.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"configs": map[string]any{"type": "array", "items": savedConfigSchema},
				"_links":  linksSchema(),
			},
		}},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
	},
}

// CreateTemplateConfig is POST /templates/{id}/configs.
var CreateTemplateConfig = Endpoint{
	Name:    "create_template_config",
	Method:  http.MethodPost,
	Pattern: "/templates/{id}/configs",
	Scope:   auth.ScopeTemplateWrite,
	Rel:     auth.RelCreate,
	Summary: "Save a launch configuration",
	Description: "Stores a named bundle of overrides and answers against a template, for launching from later. " +
		"Supplied answers are validated against the survey before anything is stored, but absent required " +
		"answers are not: a saved configuration is legitimately partial, since it may carry the routine " +
		"overrides while a required answer arrives at launch.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "integer", Description: "The template's numeric id."},
	},
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":     "object",
		"required": []any{"name"},
		"properties": map[string]any{
			"name":    stringSchema("What a reader picks it by. Required: the anonymous configurations are written by the launch path itself."),
			"fields":  map[string]any{"type": "object", "description": "Launch overrides, keyed by launch field name."},
			"answers": map[string]any{"type": "object", "description": "Survey answers, keyed by variable."},
		},
	},
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The stored configuration, with secret answers redacted.", Schema: savedConfigSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, or carries no name.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No template with that id.", Schema: errorSchema("")},
		{Status: http.StatusUnprocessableEntity, Description: "A supplied answer violates its question's schema.", Schema: errorSchema("")},
	},
}

// RelaunchJob is POST /jobs/{id}/relaunch.
var RelaunchJob = Endpoint{
	Name:    "relaunch_job",
	Method:  http.MethodPost,
	Pattern: "/jobs/{id}/relaunch",
	Scope:   auth.ScopeRunbookExecute,
	Rel:     auth.RelExecute,
	Summary: "Relaunch a job",
	Description: "Runs a job's template again with the configuration that job ran with, resolving everything " +
		"from the job rather than from the caller. The new job is attributed to whoever asked for it, not to " +
		"whoever launched the original: a relaunch is a new decision. A job that answered a password survey " +
		"question is refused rather than replayed, because that answer is a secret one operator typed at one " +
		"moment and replaying it would let anybody who can relaunch cause a secret they have never seen to be " +
		"used again under their own name.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The job id, a UUID."},
	},
	Responses: []Response{
		{Status: http.StatusAccepted, Description: "A new job was persisted and will fan out asynchronously.", Schema: launchAcceptedSchema},
		{Status: http.StatusBadRequest, Description: "id is not a UUID.", Schema: errorSchema("")},
		{Status: http.StatusUnauthorized, Description: "No identity on the request context.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No job with that id.", Schema: errorSchema("")},
		{Status: http.StatusUnprocessableEntity, Description: "The job was not launched from a template, its template or configuration is gone, or it answered a question this platform will not replay.", Schema: errorSchema("")},
		{Status: http.StatusInternalServerError, Description: "The job could not be persisted or published.", Schema: errorSchema("")},
	},
}
