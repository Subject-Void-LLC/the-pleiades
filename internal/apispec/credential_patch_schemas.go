// This file holds the bodies the two credential PATCH routes accept:
// PATCH /credential-types/{id} and PATCH /credentials/{id}.
//
// Each is a partial update, as the access and project PATCHes are: a field
// left out keeps its stored value, a field present is applied, and a null on
// any field is refused. The fields no update can change are listed on each,
// and a value for one is accepted only when it equals the stored one.
package apispec

import (
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// credentialTypePatchSchema is the body PATCH /credential-types/{id} accepts.
var credentialTypePatchSchema = map[string]any{
	"type": "object",
	"description": patchBodyRules + " inputs and injectors, when present, each replace the stored document whole, " +
		"as PUT .../inputs and PUT .../injectors do. The type as it would be saved is validated whole, so an " +
		"inputs document dropping an input an injector still reads is refused. " + typeEditRule,
	"properties": map[string]any{
		"name":         stringSchema("Unique within the organization. May not be blank."),
		"description":  stringSchema("\"\" clears it."),
		"kind":         stringSchema("One of " + kindList + "."),
		"namespace":    stringSchema(fixedFieldNote + " It is the stable identifier an import keys on."),
		"organization": map[string]any{"type": "integer", "description": fixedFieldNote},
		"inputs":       inputsSchema,
		"injectors":    injectorsSchema,
	},
}

// credentialPatchSchema is the body PATCH /credentials/{id} accepts.
var credentialPatchSchema = map[string]any{
	"type": "object",
	"description": patchBodyRules + " The credential as it would be saved is checked whole against its type, so " +
		"removing the only value or reference a required input has is refused.",
	"properties": map[string]any{
		"name":            stringSchema("Unique within the organization. May not be blank."),
		"description":     stringSchema("\"\" clears it."),
		"credential_type": map[string]any{"type": "integer", "description": fixedFieldNote},
		"organization":    map[string]any{"type": "integer", "description": fixedFieldNote},
		"inputs": map[string]any{
			"type": "object",
			"description": "Values keyed by input id, merged over the stored ones: an input left out keeps its value, " +
				"one sent as \"$encrypted$\" keeps the stored secret, which is what lets a form that shows the marker " +
				"send it back, and \"\" clears an input. Send a real value to rotate a secret. No value may be null.",
		},
		"external": map[string]any{
			"type": "object",
			"description": "External secret-manager references keyed by input id, each written <source>:<reference>. " +
				"When present it replaces the stored set whole: an input left out of it no longer reads from an " +
				"external source, and {} removes every reference. Leave it out to keep them. No value may be null.",
		},
	},
}

// kindList is the closed kind vocabulary written out for a description,
// built from credtype.Kinds so the published API cannot name fewer kinds
// than the store accepts, or state a count that has gone stale.
var kindList = func() string {
	kinds := credtype.Kinds()
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = string(kind)
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}()
