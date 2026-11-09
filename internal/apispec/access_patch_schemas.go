// This file holds the bodies the four access PATCH routes accept:
// organizations, teams, contacts and role bindings.
//
// Each is a partial update, as PATCH /templates/{id} is: a field left out
// keeps its stored value, a field present is applied, and a null on any
// field is refused. The fields no update can change are listed on each, and
// a value for one is accepted only when it equals the stored one.
package apispec

// accessPatchRules is the sentence every access PATCH body opens with, so
// the four cannot describe the rule differently.
const accessPatchRules = "A partial update. A field left out keeps its stored value and a field present is " +
	"applied; an empty value clears a field that can be empty. No field may be null: leave it out to keep it. " +
	"Unknown fields are refused, so a GET response cannot be sent back as it is; send the fields a create takes."

// fixedFieldNote describes a field no update can change.
const fixedFieldNote = "Not updatable: accepted only as the stored value, and any other is refused with 400."

// organizationPatchSchema is the body PATCH /organizations/{id} accepts.
var organizationPatchSchema = map[string]any{
	"type":        "object",
	"description": accessPatchRules + " frozen: false is the explicit unfreeze. The attestation cannot be sent.",
	"properties": map[string]any{
		"name":           stringSchema("Unique across the deployment, trimmed on write. May not be blank."),
		"description":    stringSchema("What this tenant is, in a sentence. \"\" clears it."),
		"classification": stringSchema("One of " + classificationList + ", or \"\" for unmarked. An environment marking is refused."),
		"change_window":  stringSchema("When this tenant permits automation to run, written for a human. \"\" clears it."),
		"frozen":         map[string]any{"type": "boolean", "description": "An operator-declared stop on this tenant. false lifts it."},
		"freeze_reason":  stringSchema("Why the freeze is in place. Kept when frozen changes, unless sent too."),
		"cost_centre":    stringSchema("Opaque reference into the customer's own system of record. \"\" clears it."),
		"ticket_key":     stringSchema("Opaque reference into the customer's own ticketing system. \"\" clears it."),
		"cmdb_id":        stringSchema("Opaque reference into the customer's own CMDB. \"\" clears it."),
	},
}

// teamPatchSchema is the body PATCH /teams/{id} accepts.
var teamPatchSchema = map[string]any{
	"type":        "object",
	"description": accessPatchRules,
	"properties": map[string]any{
		"name":         stringSchema("Unique within its organization. May not be blank."),
		"description":  stringSchema("What this team is responsible for. \"\" clears it."),
		"organization": map[string]any{"type": "integer", "description": fixedFieldNote + " Moving a team between tenants would silently re-scope every grant it holds."},
		"users":        map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Member user ids, replacing the membership wholesale. [] empties it; leaving the key out keeps it."},
	},
}

// contactPatchSchema is the body PATCH /contacts/{id} accepts.
var contactPatchSchema = map[string]any{
	"type": "object",
	"description": accessPatchRules + " The contact as it would be saved is checked whole, so clearing its only " +
		"way to reach somebody is refused.",
	"properties": map[string]any{
		"name":         stringSchema("The person or, preferably, the rota. May not be blank."),
		"role":         stringSchema("One of owner, escalation, security, billing."),
		"email":        stringSchema("\"\" clears it, while at least one of email, phone or url remains."),
		"phone":        stringSchema("See email."),
		"url":          stringSchema("See email."),
		"notes":        stringSchema("The conditions under which this is the right contact. \"\" clears it."),
		"order":        map[string]any{"type": "integer", "description": "Who is tried first. Lower is earlier, and 0 is first."},
		"organization": map[string]any{"type": "integer", "description": fixedFieldNote + " An accountability record cannot change what it is accountable for. 0 is the stored value of a team's contact."},
		"team":         map[string]any{"type": "integer", "description": fixedFieldNote + " 0 is the stored value of an organization's contact."},
	},
}

// bindingPatchSchema is the body PATCH /bindings/{id} accepts.
var bindingPatchSchema = map[string]any{
	"type": "object",
	"description": accessPatchRules + " Only the role and the effect can change: re-pointing a grant is revoking one " +
		"and issuing another, which leaves two legible events.",
	"properties": map[string]any{
		"role":       stringSchema("viewer, operator or admin."),
		"effect":     stringSchema("allow or deny."),
		"team":       map[string]any{"type": "integer", "description": fixedFieldNote},
		"scope_type": stringSchema(fixedFieldNote),
		"scope_id":   map[string]any{"type": "integer", "description": fixedFieldNote + " A system grant's stored value is 0."},
	},
}
