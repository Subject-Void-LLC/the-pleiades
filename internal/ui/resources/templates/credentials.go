package templates

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file is the credential half of the Templates view: which credentials
// a template runs as, and the launch-time prompts the bound ones ask for.
//
// # Why binding is an action rather than a control on the edit form
//
// The plan for this phase put a credentials multi-select on the template
// edit form. That is the right affordance in the wrong place, and the
// reason is a decision this same phase made deliberately elsewhere:
// binding lives under the credential:write scope rather than
// template:write, because a template author decides WHAT runs while
// whoever binds a credential decides what it runs AS, which is the higher
// privilege.
//
// A control on the edit form would be gated by that form's own scope, so
// anybody who could rename a template could change what it authenticates
// as. A RecordAction carries its own Endpoint, and the same chain that
// decides whether to render a button decides whether to accept its
// submission, so putting it here keeps the affordance where an operator
// looks for it and the authorization where this phase decided it belongs.

// credentialPrefix namespaces a prompted credential input's form control.
//
// It mirrors surveyPrefix for the same reason: an input called "password"
// and a survey question called "password" legitimately coexist on one
// form, and a shared name would silently deliver one where the other was
// meant. The credential id is part of the name because two bound
// credentials of different types can both declare an input called
// "password", and they are different secrets.
const credentialPrefix = "credential_"

// credentials is the slice of the credential surface this view adapts.
//
// Declared here rather than taken as credstore.Store, so this view holds
// the narrowest thing that does the job and cannot, for instance, delete a
// credential. It is the same two-method read shape internal/api's own
// dispatcher declares, plus the one write that the bind action performs.
type credentials interface {
	// TemplateCredentials returns what a template runs as, redacted.
	TemplateCredentials(ctx context.Context, templateID int) ([]credstore.Credential, error)

	// GetType returns a credential's type, which is where the input schema
	// lives: a credstore.Credential carries its type's name and kind but
	// not its fields, and the prompts are a property of the fields.
	GetType(ctx context.Context, id int) (credstore.CredentialType, error)

	// ListAllCredentials offers the choices the bind action presents.
	ListAllCredentials(ctx context.Context) ([]credstore.Credential, error)

	// SetTemplateCredentials replaces a template's bindings. It runs the
	// one-per-kind rule before writing, so this view does not restate it.
	SetTemplateCredentials(ctx context.Context, templateID int, credentialIDs []int) error
}

// promptedCredentialFields returns one password control per input the
// template's bound credentials prompt for at launch.
//
// Nothing is prefilled and nothing can be: an ask-at-runtime input is never
// stored, which is the whole point of the type split in
// api.Dispatcher.LaunchTemplate. So the control starts empty every time,
// and the help text says why rather than leaving an operator wondering
// whether the platform forgot their value.
func promptedCredentialFields(creds credentials, templateID int) func(context.Context) ([]view.Field, error) {
	return func(ctx context.Context) ([]view.Field, error) {
		bound, err := creds.TemplateCredentials(ctx, templateID)
		if err != nil {
			// A credential surface that cannot be read prompts for
			// nothing. The launch that follows fails at fan-out with a
			// reason naming the input, which is loud rather than silent.
			return nil, nil
		}

		var out []view.Field
		for _, c := range bound {
			ct, typeErr := creds.GetType(ctx, c.TypeID)
			if typeErr != nil {
				continue
			}
			for _, id := range ct.Inputs.AskAtRuntimeFields() {
				field, ok := ct.Inputs.Field(id)
				if !ok {
					continue
				}
				out = append(out, view.Field{
					Name:  credentialControl(c.ID, id),
					Label: strings.ToUpper(c.Name + " " + field.Label),
					// A password control, never a text one. The value is
					// typed in a room that may have other people in it,
					// and a browser offering to remember it would be
					// offering to remember a secret that belongs to one
					// run.
					Kind:         view.KindPassword,
					InForm:       true,
					Autocomplete: "off",
					Help: fmt.Sprintf(
						"Asked at launch by the credential %q and never stored, so it is blank every time and a relaunch cannot reuse it.",
						c.Name),
				})
			}
		}
		return out, nil
	}
}

// credentialControl is the form control name for one prompted input.
func credentialControl(credentialID int, inputID string) string {
	return credentialPrefix + strconv.Itoa(credentialID) + "_" + inputID
}

// bindPromptedCredentials reads the prompted inputs back off a submission.
//
// It returns credtype.PromptedInputs rather than folding the values into
// launch.Config, and that is a structural property rather than a style
// choice: Config is what recordConfig persists, and a prompted credential
// input must never be persisted. Keeping them in a separate value is what
// makes the never-persist rule a fact about the types rather than a rule
// somebody has to remember.
//
// An empty control is dropped rather than sent as an empty string. Absent
// means "not answered", which the injector reports by name; an empty string
// would mean "answered with nothing", which would inject a blank secret and
// fail against the remote service instead.
func bindPromptedCredentials(fields []view.Field, v view.Values) credtype.PromptedInputs {
	var out credtype.PromptedInputs
	for _, f := range fields {
		credentialID, inputID, ok := parseCredentialControl(f.Name)
		if !ok {
			continue
		}
		value := v.Get(f.Name)
		if value == "" {
			continue
		}
		if out == nil {
			out = credtype.PromptedInputs{}
		}
		if out[credentialID] == nil {
			out[credentialID] = map[string]string{}
		}
		out[credentialID][inputID] = value
	}
	return out
}

// parseCredentialControl reads a control name back into its credential id
// and input id.
//
// The split is on the FIRST underscore after the id, because an input id
// legitimately contains underscores (ssh_key_unlock) and a credential id
// never does. Splitting the other way would turn ssh_key_unlock into
// ssh.
func parseCredentialControl(name string) (int, string, bool) {
	rest, ok := strings.CutPrefix(name, credentialPrefix)
	if !ok {
		return 0, "", false
	}
	idText, inputID, ok := strings.Cut(rest, "_")
	if !ok || inputID == "" {
		return 0, "", false
	}
	id, err := strconv.Atoi(idText)
	if err != nil || id < 1 {
		return 0, "", false
	}
	return id, inputID, true
}

// bindCredentialsAction sets what a template runs as.
//
// A multi-select over the existing credentials rather than a list of ids to
// type, for the reason view.KindLookup's own comment gives: a control
// asking somebody to type primary keys is a control that will receive the
// wrong primary key, and here the wrong key means the run authenticates as
// something nobody intended.
func bindCredentialsAction(creds credentials) view.RecordAction {
	return view.RecordAction{
		Name:     "credentials",
		Label:    "Credentials",
		Heading:  "What this template runs as",
		Endpoint: &apispec.SetTemplateCredentials,
		// What the template is bound to NOW, which is what makes this a
		// replace rather than a blanking.
		//
		// This control resolved exactly these ids before the prefill seam
		// existed, sorted them, and dropped them on the floor, because
		// there was nowhere for a record action to put a form value. So
		// the multi-select rendered with nothing selected on a template
		// that was bound to three credentials, and pressing the button as
		// drawn replaced those three with none: the template silently
		// stopped authenticating as anything. The help text below says
		// replacing this list replaces what the template runs as, and it
		// was telling the truth.
		//
		// Comma joined because that is how a multi-select prefill travels
		// in this UI, and sorted so the rendering does not reshuffle
		// between reads.
		Form: func(ctx context.Context, id string) (map[string]string, error) {
			templateID, err := strconv.Atoi(id)
			if err != nil {
				return nil, nil
			}
			bound, err := creds.TemplateCredentials(ctx, templateID)
			if err != nil {
				return nil, err
			}
			selected := make([]string, 0, len(bound))
			for _, c := range bound {
				selected = append(selected, strconv.Itoa(c.ID))
			}
			sort.Strings(selected)
			return map[string]string{"credentials": strings.Join(selected, ",")}, nil
		},
		FieldsFor: func(ctx context.Context, id string) ([]view.Field, error) {
			return []view.Field{{
				Name: "credentials", Label: "CREDENTIALS", Kind: view.KindLookup, InForm: true,
				Help: "At most one credential per kind, except vault credentials, which may repeat when each names a distinct identifier. Replacing this list replaces what the template authenticates as.",
				Options: func(ctx context.Context) ([]view.Option, error) {
					all, err := creds.ListAllCredentials(ctx)
					if err != nil {
						return nil, err
					}
					out := make([]view.Option, 0, len(all))
					for _, c := range all {
						// The type in the label, because the binding rule
						// keys on kind and a reader choosing two
						// credentials needs to see the collision before
						// the store refuses it.
						out = append(out, view.Option{
							Label: c.Name + " (" + c.TypeName + ")",
							Value: strconv.Itoa(c.ID),
						})
					}
					return out, nil
				},
			}}, nil
		},
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			templateID, err := strconv.Atoi(id)
			if err != nil || templateID < 1 {
				return "", nil, fmt.Errorf("credentials: %q is not a template id", id)
			}

			// Selected, not Get: a multi-select submits one value per
			// chosen option, and reading it through the singular accessor
			// would bind the first credential and silently drop the rest.
			ids := make([]int, 0, 4)
			for _, raw := range v.Selected("credentials") {
				trimmed := strings.TrimSpace(raw)
				if trimmed == "" {
					continue
				}
				parsed, convErr := strconv.Atoi(trimmed)
				if convErr != nil || parsed < 1 {
					errs := view.FieldErrors{}
					errs.Add("credentials", "That is not a known credential: "+strconv.Quote(trimmed)+".")
					return "", errs, nil
				}
				ids = append(ids, parsed)
			}

			if err := creds.SetTemplateCredentials(ctx, templateID, ids); err != nil {
				// The store's message names both colliding credentials,
				// which is the detail an operator needs to choose which one
				// to drop, so it is shown as written.
				errs := view.FieldErrors{}
				errs.Add("credentials", err.Error())
				return "", errs, nil
			}
			return "/" + Name + "/" + id, nil, nil
		},
	}
}
