// This file is the Inputs tab of a credential type: the read-only table of
// the fields a credential of the type holds.
//
// It shows what the list column can only summarise -- each input's id,
// label, type, and whether it is secret and required -- on the type's own
// detail page. Authoring an input is a metadata write and lands separately;
// see the package doc comment for why the injector document in particular
// stays off a free-form control.
package credentialtypes

import (
	"context"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// inputsTitle is the section heading and the tab slug an add redirects to.
const inputsTitle = "Inputs"

// addInputName is the Add-input header action's URL segment.
const addInputName = "add-input"

// inputColumns are the read-only columns the Inputs table renders. The
// secret flag is a badge because it is the one column that changes what a
// reader is responsible for; the rest are plain text.
var inputColumns = []view.Field{
	{Name: "id", Label: "ID", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "label", Label: "LABEL", Kind: view.KindText, InList: true},
	{Name: "type", Label: "TYPE", Kind: view.KindText, InList: true},
	{
		Name: "secret", Label: "SECRET", Kind: view.KindBadge, InList: true,
		BadgeClass: secretClass,
	},
	{Name: "required", Label: "REQUIRED", Kind: view.KindText, InList: true},
}

// secretClass marks the secret inputs, the ones whose value is encrypted at
// rest and never shown again.
func secretClass(value string) string {
	if value == "yes" {
		return "badge-changed"
	}
	return "badge-neutral"
}

// inputsSection is the Inputs tab: the type's input schema as a table, with
// an Add control in the header.
func inputsSection(store credstore.Store) view.Section {
	return view.Section{
		Title:   inputsTitle,
		Summary: "The fields a credential of this type holds. A secret input is encrypted at rest and never shown again.",
		Status:  view.StatusImplemented,
		Fields:  inputColumns,
		Empty:   "This credential type has no inputs yet.",
		Rows:    inputRows(store),
		Actions: []string{addInputName},
	}
}

// inputRows projects a type's input schema onto section rows. A row's id is
// the input's own id.
func inputRows(store credstore.TypeReader) func(context.Context, string) ([]view.Row, error) {
	return func(ctx context.Context, parentID string) ([]view.Row, error) {
		// Empty parent: an input belongs to a type, not to the collection,
		// so the section on the list page shows its empty state rather than
		// every input in the system.
		if parentID == "" {
			return nil, nil
		}
		numeric, err := strconv.Atoi(parentID)
		if err != nil {
			return nil, nil
		}
		ct, err := store.GetType(ctx, numeric)
		if err != nil {
			return nil, err
		}

		required := make(map[string]bool, len(ct.Inputs.Required))
		for _, id := range ct.Inputs.Required {
			required[id] = true
		}

		out := make([]view.Row, 0, len(ct.Inputs.Fields))
		for _, f := range ct.Inputs.Fields {
			out = append(out, view.Row{
				ID: f.ID,
				Cells: view.Cells{
					"id":       f.ID,
					"label":    f.Label,
					"type":     inputTypeLabel(f.Type),
					"secret":   yesNo(f.Secret),
					"required": yesNo(required[f.ID]),
				},
			})
		}
		return out, nil
	}
}

// inputTypeLabel names an input's type, defaulting an empty one to string,
// which is how the schema itself reads it.
func inputTypeLabel(t credtype.InputType) string {
	if t == credtype.InputBoolean {
		return "boolean"
	}
	return "string"
}

// yesNo renders a boolean cell.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// inputTypeOptions offers the two input types a form may set.
func inputTypeOptions(context.Context) ([]view.Option, error) {
	return []view.Option{
		{Label: "string", Value: string(credtype.InputString)},
		{Label: "boolean", Value: string(credtype.InputBoolean)},
	}, nil
}

// addInputAction appends one input to a type's schema.
//
// It carries the type's own set-inputs endpoint, whose relation is distinct
// from the metadata edit's "update": the affordance layer keys one relation
// to one endpoint per resource, so a section write action cannot reuse the
// relation the record's own edit already owns. The Submit does the work
// through the same store the API endpoint does, reading the stored type so
// the injector document beside the schema is carried forward unchanged.
//
// The form is blank, which is why this is an ADD rather than an edit: a
// record action's form prefills nothing, so editing an existing input in
// place needs a seam this does not have yet. Adding needs none.
func addInputAction(store credstore.Store) view.RecordAction {
	return view.RecordAction{
		Name:     addInputName,
		Label:    "Add input",
		Heading:  "Add an input to this credential type",
		Endpoint: &apispec.SetCredentialTypeInputs,
		Fields: []view.Field{
			{
				Name: "id", Label: "ID", Kind: view.KindText, Required: true, InForm: true,
				Autocomplete: "off",
				Help:         "The identifier an injector references. Lowercase letters, digits and underscores, starting with a letter.",
			},
			{
				Name: "label", Label: "LABEL", Kind: view.KindText, Required: true, InForm: true,
				Help: "What a credential's own form shows for this field.",
			},
			{
				Name: "type", Label: "TYPE", Kind: view.KindSelect, InForm: true,
				Options: inputTypeOptions,
				Help:    "A string value, or a true/false flag.",
			},
			{
				Name: "secret", Label: "SECRET", Kind: view.KindBool, InForm: true,
				Help: "Whether the value is encrypted at rest and never shown again. The most consequential choice here.",
			},
			{
				Name: "required", Label: "REQUIRED", Kind: view.KindBool, InForm: true,
				Help: "Whether a credential of this type must supply it.",
			},
			{
				Name: "multiline", Label: "MULTILINE", Kind: view.KindBool, InForm: true,
				Help: "Whether a credential's form renders it as a text area rather than one line.",
			},
			{
				Name: "help", Label: "HELP", Kind: view.KindLongText, InForm: true,
				Help: "Explanatory text shown beneath the control on a credential's form.",
			},
			{
				Name: "default", Label: "DEFAULT", Kind: view.KindText, InForm: true,
				Help: "The value used when a credential omits this input. A secret input may not carry one.",
			},
		},
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", nil, credstore.ErrNotFound
			}
			ct, err := store.GetType(ctx, numeric)
			if err != nil {
				return "", nil, err
			}

			field := credtype.InputField{
				ID:        strings.TrimSpace(v.Get("id")),
				Label:     strings.TrimSpace(v.Get("label")),
				Type:      credtype.InputType(strings.TrimSpace(v.Get("type"))),
				Secret:    v.Bool("secret"),
				Multiline: v.Bool("multiline"),
				Help:      strings.TrimSpace(v.Get("help")),
				Default:   strings.TrimSpace(v.Get("default")),
			}
			ct.Inputs.Fields = append(ct.Inputs.Fields, field)
			if v.Bool("required") {
				ct.Inputs.Required = append(ct.Inputs.Required, field.ID)
			}

			if _, err := store.UpdateType(ctx, numeric, ct.CredentialType); err != nil {
				return "", inputFault(err), nil
			}
			return "/ui/" + Name + "/" + id + "?tab=" + view.TabSlug(inputsTitle), nil, nil
		},
	}
}

// inputFault turns the store's schema refusals into messages on the control
// that caused them. The store is the authority, so its own words are shown
// rather than a vaguer restatement.
func inputFault(err error) view.FieldErrors {
	errs := view.FieldErrors{}
	if err == nil {
		return errs
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "must be lowercase") || strings.Contains(msg, "both named"):
		errs.Add("id", msg)
	case strings.Contains(msg, "no label"):
		errs.Add("label", msg)
	case strings.Contains(msg, "secret and carries a default"):
		errs.Add("default", msg)
	default:
		// A rule with no single control to blame lands on the id, the field
		// that names the input the rest describe.
		errs.Add("id", msg)
	}
	return errs
}
