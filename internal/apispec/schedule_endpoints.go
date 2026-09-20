package apispec

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// This file declares the Schedule surface: when automation runs without
// somebody pressing launch.
//
// The scopes follow the same split the Template surface draws, one step
// further along. Reading and administering a schedule is schedule:read /
// schedule:write, which is deliberately neither template:write nor
// runbook:execute: authoring what a template does, running it once, and
// arranging for it to run unattended forever are three different
// privileges, and the third is the largest of them. See
// auth.ScopeScheduleWrite for the full reasoning.
//
// Two endpoints here exist purely so an operator can be sure before they
// commit. A recurrence rule is the kind of input whose meaning is genuinely
// hard to see by reading it -- "FREQ=MONTHLY;BYDAY=-1FR" is easy to write
// and easy to misread -- and a schedule that is wrong is wrong silently,
// at 3am, repeatedly. The preview endpoint answers "what will this actually
// do", and the zoneinfo endpoint answers "which zones may I name".

// scheduleSchema is the wire projection of one schedule.
var scheduleSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"id":          stringSchema("Stable opaque identifier. A UUID, and the value every other endpoint takes."),
		"name":        stringSchema("Unique within its organization, not globally."),
		"description": stringSchema("What this schedule is for."),
		"enabled": map[string]any{
			"type": "boolean",
			"description": "A disabled schedule is kept rather than deleted, so its occurrence history survives. " +
				"Disabling clears next_run: a schedule that will not run must not advertise a time.",
		},
		"rrule": stringSchema("The RFC 5545 recurrence, within this scheduler's supported set. " +
			"FREQ, INTERVAL, COUNT, UNTIL, WKST, BYDAY (with ordinals), BYMONTHDAY, BYMONTH, BYHOUR, " +
			"BYMINUTE and BYSETPOS are accepted; SECONDLY, BYWEEKNO, BYYEARDAY, BYSECOND and RDATE are refused."),
		"exclusions": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "EXRULE recurrences and EXDATE instants subtracted from the rule's occurrences.",
		},
		"timezone": stringSchema("An IANA zone name. Carried beside the rule rather than inside it, because it " +
			"is what decides the rule's meaning: a daily rule preserves its wall-clock hour across a daylight " +
			"saving transition, so two consecutive runs can be 23 or 25 hours apart."),
		"dtstart": stringSchema("The recurrence anchor, RFC 3339 in UTC. RFC 5545 takes from it every field the " +
			"rule leaves unspecified, so it is part of what the recurrence means."),
		"dtend":    stringSchema("When the schedule stops, RFC 3339 in UTC. Absent for an open-ended schedule."),
		"next_run": stringSchema("The next occurrence, RFC 3339 in UTC. Absent when there are no further occurrences."),
		"next_run_local": stringSchema("The same instant in the schedule's own zone, which is the reading an " +
			"operator recognises. Both are returned because either alone is ambiguous to somebody."),
		"last_fired": stringSchema("The occurrence time of the most recent firing, RFC 3339 in UTC."),
		"unified_job_template": map[string]any{
			"type": "integer",
			"description": "What this launches: the id of a job template, or of a project whose run is a sync. " +
				"One id space across every sort of launchable thing, so a schedule needs to know nothing about " +
				"which sort it points at.",
		},
		"unified_job_template_name": stringSchema("That thing's name, carried so a list does not render a bare " +
			"foreign key."),
		"unified_job_template_type": stringSchema("What sort of thing it is: job_template, or project. Not to be " +
			"confused with unified_job_type on an occurrence, which says what sort of RUN was started."),
		"saved_config": map[string]any{"type": "integer", "description": "The saved launch configuration this runs with, if any."},
		"organization": map[string]any{"type": "integer", "description": "The tenancy boundary, derived from what the schedule launches and never submitted."},
		"created_at":   stringSchema("RFC 3339."),
		"updated_at":   stringSchema("RFC 3339."),
		"_links":       linksSchema(),
	},
}

// scheduleWriteSchema is what a create or update accepts.
var scheduleWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "unified_job_template", "rrule", "dtstart"},
	"properties": map[string]any{
		"name":        stringSchema("Unique within the organization the template belongs to."),
		"description": stringSchema("What this schedule is for."),
		"enabled":     map[string]any{"type": "boolean", "description": "Defaults to true on create."},
		"rrule":       stringSchema("The RFC 5545 recurrence. Validated at save time, never at run time."),
		"exclusions": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
			"description": "EXRULE recurrences and EXDATE instants. An unprefixed line is read as an EXRULE, " +
				"which is the shape a rule copied out of AWX has.",
		},
		"timezone": stringSchema("An IANA zone name. Defaults to UTC. Must be one GET /zoneinfo lists."),
		"dtstart":  stringSchema("RFC 3339. The recurrence anchor."),
		"dtend":    stringSchema("RFC 3339. Omit for an open-ended schedule."),
		"unified_job_template": map[string]any{
			"type": "integer",
			"description": "What to launch: the id of a job template, or of a project whose run is a sync. Its " +
				"organization becomes the schedule's. Launching it requires the scope that sort of thing " +
				"declares (runbook:execute for a job template, project:write for a project), so writing a " +
				"schedule is never a way around the permission to run what it launches.",
		},
		"template": map[string]any{
			"type": "integer",
			"description": "Deprecated: the id of a TEMPLATE, resolved to its unified_job_template. Accepted " +
				"because it is what this API took before anything but a template could be scheduled. Sending " +
				"both fields naming different things is refused rather than resolved one way.",
			"deprecated": true,
		},
		"saved_config": map[string]any{
			"type": "integer",
			"description": "A saved launch configuration belonging to that same template. Refused for a sort of " +
				"launchable that takes no launch-time overrides, such as a project sync.",
		},
	},
}

// occurrenceSchema is one row of a schedule's history.
var occurrenceSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"occurrence_at": stringSchema("The recurrence instant this row is about, RFC 3339 in UTC. Not the moment " +
			"the row was written: after an outage the two differ by the length of the outage."),
		"outcome": stringSchema("fired, skipped, or claimed. A row left at claimed means a controller won the " +
			"right to run this occurrence and stopped before recording what happened; it is shown rather than " +
			"hidden because only an operator can decide whether to re-run it."),
		"reason": stringSchema("Why a skipped occurrence did not run: missed_window, missed_window_truncated, " +
			"launch_failed, or already_running when what it launches was still running from an earlier " +
			"occurrence."),
		"suppressed_count": map[string]any{
			"type": "integer",
			"description": "How many further occurrences a truncated row stands for. Non-zero only when a " +
				"backlog was too large to record one row each.",
		},
		"job": stringSchema("What this occurrence started, for a fired one, in the vocabulary of whatever it " +
			"launched: a job's own id, or a project sync attempt's. AWX calls both a unified job, which is why " +
			"one field carries either."),
		"unified_job_type": stringSchema("Which sort of run the job field names: job, or project_update. They " +
			"are looked up in different places, so a reader needs this to resolve the id."),
	},
}

// ListSchedules is GET /schedules.
var ListSchedules = Endpoint{
	Name:        "list_schedules",
	Method:      http.MethodGet,
	Pattern:     "/schedules",
	Scope:       auth.ScopeScheduleRead,
	Rel:         auth.RelCollection,
	Summary:     "List schedules",
	Description: "Returns a page of this organization's schedules, by name.",
	Params: []Param{
		{Name: "after", In: "query", Required: false, Type: "string", Description: "Keyset cursor: the id of the last schedule on the previous page."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum schedules to return. Defaults to 50, capped at 200."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of schedules.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"schedules": map[string]any{"type": "array", "items": scheduleSchema},
				"_links":    linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "limit is not a valid integer.", Schema: errorSchema("")},
	},
}

// GetSchedule is GET /schedules/{id}.
var GetSchedule = Endpoint{
	Name:        "get_schedule",
	Method:      http.MethodGet,
	Pattern:     "/schedules/{id}",
	Scope:       auth.ScopeScheduleRead,
	Rel:         auth.RelSelf,
	Summary:     "Get one schedule",
	Description: "Returns one schedule with its next run in both UTC and its own zone.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The schedule's opaque id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The schedule.", Schema: scheduleSchema},
		{Status: http.StatusNotFound, Description: "No schedule with that id in this organization.", Schema: errorSchema("")},
	},
}

// CreateSchedule is POST /schedules.
var CreateSchedule = Endpoint{
	Name:    "create_schedule",
	Method:  http.MethodPost,
	Pattern: "/schedules",
	Scope:   auth.ScopeScheduleWrite,
	Rel:     auth.RelCreate,
	Summary: "Create a schedule",
	Description: "Saves a recurrence against something launchable. The organization is derived from that thing and " +
		"cannot be submitted. The recurrence is validated here, not at run time: a rule outside the supported " +
		"set, naming an unknown zone, or naming a date that never occurs is refused at the write, because a " +
		"schedule that can never fire is indistinguishable from one that simply has not fired yet.",
	RequestContentType: "application/json",
	RequestSchema:      scheduleWriteSchema,
	Responses: []Response{
		{Status: http.StatusCreated, Description: "The created schedule.", Schema: scheduleSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed, the recurrence is not supported, the zone is unknown, or the recurrence names no real instant.", Schema: errorSchema("")},
		{Status: http.StatusForbidden, Description: "The caller may not launch what the schedule names, or it belongs to another organization.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "Nothing launchable with that id.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "A schedule with that name already exists in that organization.", Schema: errorSchema("")},
	},
}

// UpdateSchedule is PATCH /schedules/{id}.
var UpdateSchedule = Endpoint{
	Name:    "update_schedule",
	Method:  http.MethodPatch,
	Pattern: "/schedules/{id}",
	Scope:   auth.ScopeScheduleWrite,
	Rel:     auth.RelUpdate,
	Summary: "Update a schedule",
	Description: "Replaces the supplied fields and recomputes the next run. next_run is always recomputed and " +
		"never accepted from the caller: every field an edit can touch changes what it should be, so a stale " +
		"form post could otherwise pin a schedule to a time its own rule no longer produces.",
	RequestContentType: "application/json",
	RequestSchema:      scheduleWriteSchema,
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The schedule's opaque id."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The updated schedule.", Schema: scheduleSchema},
		{Status: http.StatusBadRequest, Description: "The body is malformed or the recurrence is not valid.", Schema: errorSchema("")},
		{Status: http.StatusNotFound, Description: "No schedule with that id in this organization.", Schema: errorSchema("")},
		{Status: http.StatusConflict, Description: "Another schedule in this organization already has that name.", Schema: errorSchema("")},
	},
}

// DeleteSchedule is DELETE /schedules/{id}.
var DeleteSchedule = Endpoint{
	Name:        "delete_schedule",
	Method:      http.MethodDelete,
	Pattern:     "/schedules/{id}",
	Scope:       auth.ScopeScheduleWrite,
	Rel:         auth.RelDelete,
	Summary:     "Delete a schedule",
	Description: "Removes a schedule and its occurrence history. To stop a schedule while keeping that history, set enabled to false instead.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The schedule's opaque id."},
	},
	Responses: []Response{
		{Status: http.StatusNoContent, Description: "Deleted."},
		{Status: http.StatusNotFound, Description: "No schedule with that id in this organization.", Schema: errorSchema("")},
	},
}

// ListScheduleOccurrences is GET /schedules/{id}/occurrences.
var ListScheduleOccurrences = Endpoint{
	Name:    "list_schedule_occurrences",
	Method:  http.MethodGet,
	Pattern: "/schedules/{id}/occurrences",
	Scope:   auth.ScopeScheduleRead,
	Rel:     auth.RelCollection,
	Summary: "List a schedule's occurrences",
	Description: "Returns what this schedule has and has not run, most recent first. An occurrence that did " +
		"not run is a row rather than a gap, which is what makes the history auditable: a missing row and a " +
		"row reading skipped/missed_window are the same absence of a job and completely different answers.",
	Params: []Param{
		{Name: "id", In: "path", Required: true, Type: "string", Description: "The schedule's opaque id."},
		{Name: "limit", In: "query", Required: false, Type: "integer", Description: "Maximum occurrences to return. Defaults to 50, capped at 200."},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "A page of occurrences.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"occurrences": map[string]any{"type": "array", "items": occurrenceSchema},
				"_links":      linksSchema(),
			},
		}},
		{Status: http.StatusNotFound, Description: "No schedule with that id in this organization.", Schema: errorSchema("")},
	},
}

// PreviewSchedule is POST /schedules/preview.
var PreviewSchedule = Endpoint{
	Name:    "preview_schedule",
	Method:  http.MethodPost,
	Pattern: "/schedules/preview",
	Scope:   auth.ScopeScheduleRead,
	Rel:     auth.RelCollection,
	Summary: "Preview a recurrence",
	Description: "Expands a recurrence without saving it, returning upcoming occurrences in both the named zone " +
		"and UTC so an operator can confirm intent before committing. Both readings are returned because " +
		"either alone hides the case that matters: a daily rule across a daylight saving transition keeps its " +
		"local hour while its UTC hour moves, and that is exactly the behaviour somebody is checking for. " +
		"It is a POST rather than a GET because a recurrence, its exclusions and its anchor do not fit " +
		"comfortably or unambiguously in a query string, not because it changes anything: it writes nothing.",
	RequestContentType: "application/json",
	RequestSchema: map[string]any{
		"type":     "object",
		"required": []any{"rrule", "dtstart"},
		"properties": map[string]any{
			"rrule":      stringSchema("The recurrence to expand."),
			"exclusions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "EXRULE and EXDATE lines."},
			"timezone":   stringSchema("An IANA zone name. Defaults to UTC."),
			"dtstart":    stringSchema("RFC 3339. The recurrence anchor."),
			"dtend":      stringSchema("RFC 3339. Occurrences after it are not returned."),
			"from":       stringSchema("RFC 3339. Where to start listing. Defaults to now."),
			"count":      map[string]any{"type": "integer", "description": "How many occurrences to return. Defaults to 10, capped at 100."},
		},
	},
	Responses: []Response{
		{Status: http.StatusOK, Description: "The upcoming occurrences.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone": stringSchema("The zone the local readings are in."),
				"occurrences": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"local": stringSchema("RFC 3339 in the named zone, with its offset. The offset changes across a daylight saving transition, which is the point."),
							"utc":   stringSchema("RFC 3339 in UTC. The same instant."),
						},
					},
				},
				"_links": linksSchema(),
			},
		}},
		{Status: http.StatusBadRequest, Description: "The recurrence is malformed or unsupported, the zone is unknown, or the recurrence names no real instant.", Schema: errorSchema("")},
	},
}

// ListZoneinfo is GET /zoneinfo.
var ListZoneinfo = Endpoint{
	Name:    "list_zoneinfo",
	Method:  http.MethodGet,
	Pattern: "/zoneinfo",
	Scope:   auth.ScopeScheduleRead,
	Rel:     auth.RelCollection,
	Summary: "List time zones",
	Description: "Returns every IANA zone a schedule may name, plus a short list of common ones to offer first. " +
		"The list is generated from the same archive this binary embeds, so a zone it offers is a zone that " +
		"will load -- a picker that could offer a zone the server then refuses is worse than no picker.",
	Responses: []Response{
		{Status: http.StatusOK, Description: "The zones.", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"zones":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"common": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "A short list to offer above the full one."},
				"_links": linksSchema(),
			},
		}},
	},
}
