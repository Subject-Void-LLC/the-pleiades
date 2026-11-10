// This file is the body PATCH /templates/{id} accepts, and how it is folded
// over the stored template.
//
// PATCH is a partial update, as AWX's is: a template field the body leaves
// out keeps its stored value and a field it names is applied. An empty value
// clears description ("") and defaults ({}), and prompts and
// required_capabilities ([]); allow_simultaneous takes false; a name may not
// be blank. A survey is cleared with {"enabled": false, "questions": []},
// since each of its two fields is kept when left out and "survey": {} keeps
// both. It used to be a full replace, so a client that sent only a new name
// deleted the survey's questions, emptied the defaults, the prompts and the
// capability hint, and turned allow_simultaneous off (FAILURE_PATTERNS 1056).
//
// Presence is tracked the way the device and schedule PATCHes track it
// (deviceWriteRequest, scheduleWriteDTO): a pointer per field, nil when the
// key was absent. Two rules are added on top. A pointer alone reads an
// explicit JSON null as absent, so a null on one of the template's own
// fields, or on survey.enabled or survey.questions, is refused, naming the
// field (see templatePatchDTO for why); the fields inside a question are
// decoded as a create decodes them, so a null there reads as that field's
// zero value. And the kind, definition and inventory, which no update can
// change, are refused when they differ from the stored ones, as the device
// PATCH refuses a changed name or type.
//
// Concurrent PATCHes are last-writer-wins, as they were: each reads the
// stored template, folds its fields over it and writes the result, with no
// version check, so of two at once the second's write covers the first's
// for every field.
package api

import (
	"errors"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// templatePatchDTO is the body PATCH /templates/{id} accepts. Every field is
// a pointer so an absent key can be told from a zero value.
//
// A JSON null is refused for every one of these fields, and survey.enabled
// and survey.questions, rather than read either way, and
// the edge cases are why. Read as "clear", it deletes data the caller never
// meant to touch: a client serializing an unset value as null (a Go client's
// nil slice, a survey it edits through PUT /templates/{id}/survey instead)
// would wipe that field on every edit, which is the defect this type exists
// to remove. Read as "keep", it silently ignores a JSON Merge Patch client
// that asked for a removal, and for prompts that means a field the author
// thinks they locked stays open to every launch. Refusing is never silent
// and never destructive, and it costs nothing: leaving the key out keeps a
// field, and an empty value clears every one that can be empty. It is also
// what AWX does, whose API
// answers a null on a non-nullable field with a 400.
type templatePatchDTO struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`

	// Kind, Definition and Inventory are not updatable (templateWriteDTO
	// says why), and the store carries all three forward from storage. Each
	// is accepted when it equals the stored value, so a client that sends
	// back the fields a create takes, one of them edited, still works, and
	// refused when it differs: answering 200 for a re-point that changed
	// nothing told the caller it had. See fixedFieldRefusal.
	Kind       *string `json:"kind"`
	Definition *string `json:"definition"`
	Inventory  *int    `json:"inventory"`

	Defaults *map[string]any `json:"defaults"`
	Prompts  *[]string       `json:"prompts"`

	Survey *surveyPatchDTO `json:"survey"`

	AllowSimultaneous    *bool     `json:"allow_simultaneous"`
	RequiredCapabilities *[]string `json:"required_capabilities"`

	// nulls names every field the body set to JSON null, survey fields as
	// "survey.<name>", sorted. Unexported, so decoding never fills it.
	nulls []string
}

// surveyPatchDTO is the survey half of a PATCH, partial in its own right.
//
// Its two fields are separate edits. Turning a survey off while keeping its
// questions is a real state ("a survey written and turned off says
// something different from no survey at all"), so naming only enabled must
// not delete the questions. Questions, when named, replace the whole list in
// the order given, as PUT /templates/{id}/survey does, since the order is
// the order the launch form asks them in.
type surveyPatchDTO struct {
	Enabled   *bool          `json:"enabled"`
	Questions *[]questionDTO `json:"questions"`

	// nulls is templatePatchDTO.nulls for this object's own keys.
	nulls []string
}

// templateNoun names a template in the shared PATCH refusals (patch.go).
const templateNoun = "a template"

// UnmarshalJSON decodes a PATCH body strictly and records which fields it set
// to null.
//
// decodeJSON's DisallowUnknownFields does not reach inside a custom
// unmarshaler, so the strict decode is repeated in decodePatch; an unknown
// key is still refused as a malformed body, as it was before this type
// existed.
func (d *templatePatchDTO) UnmarshalJSON(data []byte) error {
	// fields is the same struct without this method, so decoding into it
	// does not recurse back here.
	type fields templatePatchDTO
	decoded, nulls, err := decodePatch[fields](data, templateNoun)
	if err != nil {
		return err
	}
	*d = templatePatchDTO(decoded)

	// A null survey is caught above, as a key of this object. A survey that
	// is an object carries its own nulls, which are reported under its name.
	if d.Survey != nil {
		for _, name := range d.Survey.nulls {
			nulls = append(nulls, "survey."+name)
		}
		sort.Strings(nulls)
	}
	d.nulls = nulls
	return nil
}

// UnmarshalJSON decodes the survey half of a PATCH strictly and records which
// of its fields were null.
func (s *surveyPatchDTO) UnmarshalJSON(data []byte) error {
	type fields surveyPatchDTO
	decoded, nulls, err := decodePatch[fields](data, templateNoun)
	if err != nil {
		// A field named twice inside the survey is reported under the
		// survey's name, as its nulls are.
		var repeated *patchRepeatedFieldError
		if errors.As(err, &repeated) {
			repeated.Field = "survey." + repeated.Field
		}
		return err
	}
	*s = surveyPatchDTO(decoded)
	s.nulls = nulls
	return nil
}

// templateRemedy is what a refused change to a fixed template field says to
// do instead.
const templateRemedy = "copy the template instead"

// fixedFieldRefusal is the 400 message for a body that asks to change the
// kind, the definition or the inventory, or "" when it asks none of that.
//
// The definition is compared trimmed, since the store saves it trimmed, so
// an echo of the stored one with spaces around it is still an echo.
func (d templatePatchDTO) fixedFieldRefusal(stored launch.Template) string {
	switch {
	case d.Kind != nil && *d.Kind != stored.KindName:
		return fixedFieldMessage("kind", templateNoun, templateRemedy)
	case d.Definition != nil && strings.TrimSpace(*d.Definition) != stored.Definition:
		return fixedFieldMessage("definition", templateNoun, templateRemedy)
	case d.Inventory != nil && *d.Inventory != stored.InventoryID:
		return fixedFieldMessage("inventory", templateNoun, templateRemedy)
	}
	return ""
}

// nullRefusal is the 400 message for a body that set fields to null.
func (d templatePatchDTO) nullRefusal() string {
	return patchNullRefusal(templateNoun, d.nulls)
}

// apply folds the fields the body named over stored, leaving every other
// field as it was. The result goes to the store's Update, which validates
// the whole of it, so a partial PATCH is held to every check a full one is.
func (d templatePatchDTO) apply(stored launch.Template) launch.Template {
	if d.Name != nil {
		stored.Name = *d.Name
	}
	if d.Description != nil {
		stored.Description = *d.Description
	}
	if d.Defaults != nil {
		stored.Defaults = launch.Fields(*d.Defaults)
	}
	if d.Prompts != nil {
		stored.Prompts = *d.Prompts
	}
	if d.AllowSimultaneous != nil {
		stored.AllowSimultaneous = *d.AllowSimultaneous
	}
	if d.RequiredCapabilities != nil {
		stored.RequiredCaps = *d.RequiredCapabilities
	}
	if d.Survey != nil {
		if d.Survey.Enabled != nil {
			stored.Survey.Enabled = *d.Survey.Enabled
		}
		if d.Survey.Questions != nil {
			stored.Survey.Questions = fromQuestionDTOs(*d.Survey.Questions)
		}
	}
	return stored
}
