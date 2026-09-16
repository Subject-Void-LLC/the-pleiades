// This file is the last piece of the credential type's write path: editing
// one input in place.
//
// Until it existed the only way to correct a typo in an input's label was
// to remove the input and add it back, which is not a correction: removing
// an input leaves every credential of the type holding a value the type no
// longer declares, so the round trip can strand records that a one field
// edit would not have touched at all. It was also the only route, because a
// row control could not prompt and an action form could not prefill.
//
// The form is the add form's own field slice. The id is Immutable, so the
// add half renders it and this half withholds it: an injector references an
// input BY id, and renaming one in place would break every template using
// it while looking like a spelling fix. The URL says which input.
package credentialtypes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// editInputName is the edit control's URL segment.
const editInputName = "edit-input"

// editInputAction edits one input of a type's schema in place.
//
// It names the same endpoint the add and remove controls do, because all
// three are one API operation: set-inputs replaces the whole document
// however it was changed, and "may this caller set this type's inputs" has
// one answer for all of them.
func editInputAction(store credstore.Store) view.RowAction {
	return view.RowAction{
		Name:     editInputName,
		Label:    "Edit",
		Heading:  "Edit this input",
		Endpoint: &apispec.SetCredentialTypeInputs,
		Fields:   inputFormFields(),
		Form:     inputValues(store),
		Submit: func(ctx context.Context, parentID, rowID string, v view.Values) (string, view.FieldErrors, error) {
			redirect, err := withType(ctx, store, parentID, inputsTitle, func(ct *credstore.CredentialType) error {
				return editInput(&ct.Inputs, rowID, v)
			})
			if fault := asFieldErrors(err); fault.Any() {
				return "", fault, nil
			}
			return redirect, nil, err
		},
	}
}

// inputValues prefills the edit form from the stored input.
//
// Read back from the schema rather than from the row the table drew,
// because a Row's cells are display strings and a form value is a
// submission token. REQUIRED renders "yes" in its column where the checkbox
// reads only the literal "true", TYPE renders "string" where the select
// posts its own value, and MULTILINE, HELP and DEFAULT appear in no column
// at all. A prefill built from cells would be wrong in two controls and
// blank in three.
func inputValues(store credstore.TypeReader) func(context.Context, string, string) (map[string]string, error) {
	return func(ctx context.Context, parentID, rowID string) (map[string]string, error) {
		numeric, err := strconv.Atoi(parentID)
		if err != nil {
			return nil, credstore.ErrNotFound
		}
		ct, err := store.GetType(ctx, numeric)
		if err != nil {
			return nil, err
		}

		field, ok := ct.Inputs.Field(rowID)
		if !ok {
			// A stale page reaching an input somebody else removed. A
			// refusal rather than an error page, because the operator can
			// act on it: the list they are looking at is out of date.
			return nil, view.Refuse(fmt.Errorf("this credential type has no input %q any more", rowID))
		}

		required := false
		for _, name := range ct.Inputs.Required {
			if name == rowID {
				required = true
				break
			}
		}

		// An empty stored type means string, which is how the schema
		// itself reads it, so the select opens on the value the input
		// actually has rather than on a blank option nobody chose.
		inputType := field.Type
		if inputType == "" {
			inputType = credtype.InputString
		}

		return map[string]string{
			"label":     field.Label,
			"type":      string(inputType),
			"secret":    checkbox(field.Secret),
			"required":  checkbox(required),
			"multiline": checkbox(field.Multiline),
			"help":      field.Help,
			"default":   field.Default,
			// No "id": it is Immutable, so the edit form does not render
			// it, and NarrowPrefill refuses a value for a control the form
			// does not draw.
		}, nil
	}
}

// checkbox encodes a boolean the way a submitted checkbox encodes it. The
// form reads the literal "true" and nothing else, so "false" would render
// as unchecked exactly as "" does and would be a second spelling of one
// state.
func checkbox(on bool) string {
	if on {
		return "true"
	}
	return ""
}

// editInput replaces one input in place, keeping its position.
//
// In place rather than remove-and-append, because the schema's order is the
// order a credential's own form renders its controls in, and an edit that
// moved a field to the bottom would silently rearrange every form of that
// type.
func editInput(schema *credtype.InputSchema, id string, v view.Values) error {
	at := -1
	for i, f := range schema.Fields {
		if f.ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return view.Refuse(fmt.Errorf("this credential type has no input %q, so nothing was changed", id))
	}

	schema.Fields[at] = credtype.InputField{
		// The stored id, never a submitted one. The form does not offer
		// the control, so there is nothing to read, and taking it from the
		// URL is what makes a rename impossible rather than merely
		// undocumented.
		ID:        id,
		Label:     strings.TrimSpace(v.Get("label")),
		Type:      credtype.InputType(strings.TrimSpace(v.Get("type"))),
		Secret:    v.Bool("secret"),
		Multiline: v.Bool("multiline"),
		Help:      strings.TrimSpace(v.Get("help")),
		Default:   strings.TrimSpace(v.Get("default")),
	}

	// Required lives beside the fields rather than on one, so it is
	// rewritten here rather than carried along.
	required := make([]string, 0, len(schema.Required)+1)
	for _, name := range schema.Required {
		if name != id {
			required = append(required, name)
		}
	}
	if v.Bool("required") {
		required = append(required, id)
	}
	schema.Required = required
	return nil
}

// asFieldErrors turns a refusal that names one control into a message on
// that control, so a rule the form itself could have expressed is answered
// on the box that broke it rather than on a separate page.
//
// Only the store's schema rules reach here; anything else stays an error
// and is answered as one. inputFault already owns the mapping, so this is
// the seam between a row action's Refused and the add form's own answer
// rather than a second copy of it.
func asFieldErrors(err error) view.FieldErrors {
	var refused view.Refused
	if !errors.As(err, &refused) || refused.Err == nil {
		return view.FieldErrors{}
	}
	return inputFault(refused.Err)
}
