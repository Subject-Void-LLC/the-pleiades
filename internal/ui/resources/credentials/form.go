// This file is the credential create and edit form: the controls a
// credential's own type declares, resolved while the form is being filled
// in rather than after the record exists.
//
// A credential is the one resource here whose field set is not knowable
// from its own package. The inputs belong to the credential TYPE, which is
// data somebody wrote over the API, so the form has to be built from a
// schema that arrives at run time. That is view.Descriptor.FieldsFor, and
// until Credentials needed it that seam refused a create outright, on the
// reasoning that a record which does not exist has nothing to resolve a
// field set from. True for Templates, whose dynamic fields come from a kind
// its stored row names. Not true here: what drives the resolution is the
// type being chosen in the form, which is present on a create and absent
// from the database. The seam now carries view.Resolve, which holds both.
//
// # No secret is ever rendered
//
// credstore hands back a redacted projection: every secret input reads as
// redact.Marker and the real value has nowhere to go. This file prefills a
// secret control with that marker and submits it back unchanged, which
// credstore.mergeInputs already reads as "leave the stored value alone".
// So an ordinary rename cannot blank a password, and a form that is never
// given a secret cannot leak one.
package credentials

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// inputPrefix namespaces a type-declared input away from this view's own
// static fields.
//
// Required rather than tidy. An input id is whatever the credential type's
// author chose, the pattern allows "name" and "description", and both are
// fields this view already declares. Without the prefix a type could
// silently take over the control that names the credential.
const inputPrefix = "input_"

// typeReader is the slice of credstore.Store this form needs: which types
// exist, and what one of them declares.
type typeReader interface {
	ListAllTypes(ctx context.Context) ([]credstore.CredentialType, error)
	GetType(ctx context.Context, id int) (credstore.CredentialType, error)
	GetCredential(ctx context.Context, id int) (credstore.Credential, error)
}

// typeOptions lists the credential types a new credential may be given.
func typeOptions(store typeReader) func(context.Context) ([]view.Option, error) {
	return func(ctx context.Context) ([]view.Option, error) {
		types, err := store.ListAllTypes(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]view.Option, 0, len(types))
		for _, t := range types {
			label := t.Name
			if t.OrganizationName != "" {
				label += " (" + t.OrganizationName + ")"
			}
			out = append(out, view.Option{Label: label, Value: strconv.Itoa(t.ID)})
		}
		return out, nil
	}
}

// inputFields resolves the controls the chosen credential type declares.
//
// The type comes from the submission on a create and from the stored record
// on an edit, because a credential's type is immutable: changing it would
// reinterpret every stored input against a different schema, which is a
// replacement rather than an edit.
func inputFields(store typeReader) func(context.Context, view.Resolve) ([]view.Field, error) {
	return func(ctx context.Context, r view.Resolve) ([]view.Field, error) {
		id, ok := resolveTypeID(ctx, store, r)
		if !ok {
			// No type chosen yet. The form renders its static half and the
			// submit reads "Continue", which is the first step of the two
			// this form takes without JavaScript.
			return nil, nil
		}
		ct, err := store.GetType(ctx, id)
		if err != nil {
			// A type that cannot be read declares no controls. The write
			// still refuses, with the store's own error against the field.
			return nil, nil
		}
		return controlsFor(ct.Inputs), nil
	}
}

// resolveTypeID answers which credential type this form is being filled in
// against, and whether one has been chosen at all.
func resolveTypeID(ctx context.Context, store typeReader, r view.Resolve) (int, bool) {
	if r.Editing() {
		id, err := strconv.Atoi(r.ID)
		if err != nil {
			return 0, false
		}
		cred, err := store.GetCredential(ctx, id)
		if err != nil {
			return 0, false
		}
		return cred.TypeID, true
	}
	id, err := strconv.Atoi(strings.TrimSpace(r.Values.Get(typeField)))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// controlsFor maps a type's input schema onto form controls.
//
// An ask-at-runtime input is deliberately absent: PLAN.md Section 29.3 says
// such a value is never persisted, so offering a control that stores it
// would promise something the platform refuses to do.
func controlsFor(schema credtype.InputSchema) []view.Field {
	required := make(map[string]bool, len(schema.Required))
	for _, id := range schema.Required {
		required[id] = true
	}

	out := make([]view.Field, 0, len(schema.Fields))
	for _, in := range schema.Fields {
		if in.AskAtRuntime {
			continue
		}
		out = append(out, controlFor(in, required[in.ID]))
	}
	return out
}

// controlFor maps one declared input onto one control.
func controlFor(in credtype.InputField, required bool) view.Field {
	f := view.Field{
		Name:     inputPrefix + in.ID,
		Label:    strings.ToUpper(labelFor(in)),
		Help:     in.Help,
		Required: required,
		InForm:   true,
		// Off for every input without exception. A browser offering to
		// remember one of these is offering to store a credential in a
		// place this platform does not control.
		Autocomplete: "off",
	}

	switch {
	case in.Secret:
		f.Kind = view.KindPassword
		f.Autocomplete = "new-password"
		f.Help = strings.TrimSpace(in.Help + " Leave as it is to keep the stored value.")
	case len(in.Choices) > 0:
		f.Kind = view.KindSelect
		choices := append([]string(nil), in.Choices...)
		f.Options = func(context.Context) ([]view.Option, error) {
			out := make([]view.Option, 0, len(choices))
			for _, c := range choices {
				out = append(out, view.Option{Label: c, Value: c})
			}
			return out, nil
		}
	case in.Type == credtype.InputBoolean:
		f.Kind = view.KindBool
	case in.Multiline:
		f.Kind = view.KindLongText
	default:
		f.Kind = view.KindText
	}
	return f
}

// labelFor is the input's own label, falling back to its id so a type that
// omitted one still renders a named control rather than an unlabelled box.
func labelFor(in credtype.InputField) string {
	if strings.TrimSpace(in.Label) != "" {
		return in.Label
	}
	return strings.ReplaceAll(in.ID, "_", " ")
}

// formValues prefills one credential's edit form.
//
// Secrets come back as redact.Marker, which is what the store handed over:
// this function never sees a plaintext secret and could not render one if
// it tried.
func formValues(c credstore.Credential) map[string]string {
	out := map[string]string{
		"name":         c.Name,
		"description":  c.Description,
		typeField:      strconv.Itoa(c.TypeID),
		"organization": strconv.Itoa(c.OrganizationID),
	}
	for id, value := range c.Inputs {
		out[inputPrefix+id] = value
	}
	return out
}

// bindInputs reads the submitted type-declared controls back into the input
// map a write carries.
//
// A submitted redact.Marker is passed through rather than filtered here.
// credstore.mergeInputs is where "unchanged" is interpreted, and doing it
// in one place is what keeps this form from having its own opinion about
// which stored secret survives a save.
func bindInputs(v view.Values) map[string]string {
	out := map[string]string{}
	for _, f := range v.Fields() {
		if !strings.HasPrefix(f.Name, inputPrefix) {
			continue
		}
		id := strings.TrimPrefix(f.Name, inputPrefix)
		if f.Kind == view.KindBool {
			out[id] = strconv.FormatBool(v.Bool(f.Name))
			continue
		}
		value := v.Get(f.Name)
		if value == "" {
			// An omitted input is omitted rather than stored empty, so a
			// type's own default still applies and CheckValues can tell
			// "not supplied" from "supplied as nothing".
			continue
		}
		out[id] = value
	}
	return out
}

// writer is the credential write port.
type writer struct{ store credstore.Store }

// Create stores a new credential against the chosen type and tenant.
func (w writer) Create(ctx context.Context, c credstore.Credential) (string, error) {
	created, err := w.store.CreateCredential(ctx, c.OrganizationID, c.TypeID, c.Name, c.Description, c.Inputs, nil)
	if err != nil {
		return "", writeFault(err)
	}
	return strconv.Itoa(created.ID), nil
}

// Update renames a credential and applies whatever inputs were supplied.
//
// It reads the stored record first to carry External forward. The store's
// update writes External wholesale, so passing the nil this form has no
// control for would silently drop every external secret reference the
// credential holds, turning a rename into an outage.
func (w writer) Update(ctx context.Context, id string, c credstore.Credential) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("credentials: unreadable credential id %q: %w", id, err)
	}
	stored, err := w.store.GetCredential(ctx, numeric)
	if err != nil {
		return err
	}
	if _, err := w.store.UpdateCredential(ctx, numeric, c.Name, c.Description, c.Inputs, stored.External); err != nil {
		return writeFault(err)
	}
	return nil
}

// Delete removes a credential.
func (w writer) Delete(ctx context.Context, id string) error {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("credentials: unreadable credential id %q: %w", id, err)
	}
	return w.store.DeleteCredential(ctx, numeric)
}

// writeFault turns a store refusal into a message against a control where
// it can be read, rather than a 500 that says a save failed without saying
// which box was wrong.
//
// The store's own validation is the authority on whether a set of inputs
// satisfies a type: it is what a create through the API is held to, and
// re-implementing it here would be a second opinion that can disagree.
func writeFault(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		return view.FieldFault{Field: "name", Message: "A credential with this name already exists in this organization."}
	case strings.Contains(msg, "input"):
		return view.FieldFault{Field: typeField, Message: msg}
	default:
		return err
	}
}
