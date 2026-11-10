// This file holds the bodies the inventory and announcement writes accept:
// POST and PATCH /inventories, and POST and PATCH /announcements.
//
// Each PATCH body is a partial update, as every other PATCH route's is: a
// field left out keeps its stored value, a field present is applied, and no
// field may be null.
package apispec

// patchRules is what every PATCH body here says about its shape.
const patchRules = "A partial update. A field left out keeps its stored value and a field present is applied. " +
	"No field may be null: leave it out to keep it. Unknown fields, the read-only ones among them, are " +
	"refused, and so is a field named twice in any mix of upper and lower case."

// inventoryWriteSchema is the body POST /inventories accepts.
var inventoryWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"name", "organization"},
	"properties": map[string]any{
		"name":         stringSchema("Unique within its organization. May not be blank."),
		"description":  stringSchema("What this inventory is for."),
		"organization": map[string]any{"type": "integer", "description": "Required, and must exist. An inventory belonging to no organization could be resolved against no organization scope. Not updatable."},
		"groups":       map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Group ids this inventory contains. Each must exist and hold no device of another organization; a repeated id is kept once."},
		"devices":      map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Device ids attached directly, with no intervening group. Each must exist and belong to this organization or to none; a repeated id is kept once."},
	},
}

// inventoryPatchSchema is the body PATCH /inventories/{id} accepts.
var inventoryPatchSchema = map[string]any{
	"type": "object",
	"description": patchRules + " The inventory as it would be saved is checked whole, so every member is " +
		"checked again. Two PATCHes of one inventory are applied one after the other, each over what the other " +
		"saved, so a PATCH that leaves out groups or devices never puts back a member another removed. A list " +
		"a PATCH names replaces the stored one whole, with no version check.",
	"properties": map[string]any{
		"name":         stringSchema("Unique within its organization. May not be blank."),
		"description":  stringSchema("What this inventory is for. \"\" clears it."),
		"organization": map[string]any{"type": "integer", "description": "Not updatable: accepted only as the stored value, and any other is refused with 400."},
		"groups":       map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Replaces the groups this inventory contains; [] empties them. Each must exist and hold no device of another organization."},
		"devices":      map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Replaces the devices attached directly; [] empties them. Each must exist and belong to this organization or to none."},
	},
}

// announcementWriteSchema is the body POST /announcements accepts.
var announcementWriteSchema = map[string]any{
	"type":     "object",
	"required": []any{"title", "body"},
	"properties": map[string]any{
		"title":        stringSchema("The headline. May not be blank."),
		"body":         stringSchema("The message. May not be blank."),
		"level":        stringSchema("One of info, changed, warning, critical. Defaults to info, as does an unrecognized value."),
		"organization": map[string]any{"type": "integer", "description": "Omit for a system-wide announcement. Must exist. Not updatable."},
		"starts_at":    stringSchema("RFC 3339. Omit to show immediately."),
		"ends_at":      stringSchema("RFC 3339, after starts_at. Omit for no expiry."),
	},
}

// announcementPatchSchema is the body PATCH /announcements/{id} accepts.
var announcementPatchSchema = map[string]any{
	"type": "object",
	"description": patchRules + " The window is checked on the announcement as it would be saved, so moving " +
		"one bound past the stored other is refused.",
	"properties": map[string]any{
		"title":        stringSchema("The headline. May not be blank."),
		"body":         stringSchema("The message. May not be blank."),
		"level":        stringSchema("One of info, changed, warning, critical. \"\" or an unrecognized value is info."),
		"organization": map[string]any{"type": "integer", "description": "Not updatable: accepted only as the stored value, 0 for a system-wide announcement, and any other is refused with 400."},
		"starts_at":    stringSchema("RFC 3339. \"\" removes the start, so the announcement shows at once."),
		"ends_at":      stringSchema("RFC 3339, after starts_at. \"\" removes the end, so the announcement shows until retired."),
	},
}
