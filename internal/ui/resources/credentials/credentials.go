// Package credentials is the Credentials view: which secrets exist, what
// type each one is, and what is bound to it. Never a value.
//
// # This view was declared with a written promise not to enumerate, and
// that promise has been revised
//
// The earlier version of this file committed, in writing, to never listing
// credential names even once implemented. The argument was that the set of
// names in a deployment tells a reader which vendors are present, which
// devices are managed by what, and which accounts exist to be attacked;
// that this is reconnaissance; and that it does not become safe by omitting
// the values. The view was to be "built around a specific, authorized
// lookup rather than a browsable catalog."
//
// It ships as a list. Silently contradicting a written commitment would be
// worse than either choice, so here is why it changed.
//
// The argument proved too much. Every fact it protects is already readable
// by the same person from the views next to this one: Devices names every
// managed device and its vendor, Templates names what runs against them,
// and Inventories names how they are grouped. A reader assembling a target
// list does not need this page. Withholding it costs the operator the one
// question they most need answered about secrets -- which exist, who bound
// them, and which are stale -- and rotation is not possible without
// enumeration. The promise was protecting a reader who already had the
// information and taxing the operator who needed it.
//
// The second half of the original sentence turned out to be the right half.
// It asked for an AUTHORIZED lookup, and authorization is what this has:
// credential:read is its own scope, granted separately from template:read
// and device:read, so seeing this page is a decision somebody made rather
// than a side effect of having an account. The control is the scope, not
// the absence of a list.
//
// What did NOT change is the part that was never negotiable. There is no
// field here for a secret value and there never will be. The port this
// view holds is credstore.TypeReader's sibling on the credential side,
// whose projection HAS NO FIELD for a plaintext input: a secret reads back
// as a redaction marker and there is nowhere for the real thing to go. A
// mistake in this file cannot leak a secret, because the object it is
// handed does not contain one.
//
// # Writable, once the form seam could express a per-type field set
//
// This said writes stay on the API, because a credential's inputs are
// per-type and a create form would have to build its controls from the
// selected type's schema before the record exists, "which is the one thing
// view.Descriptor.FieldsFor deliberately does not do". That was an accurate
// description of the seam and not of the requirement. FieldsFor now takes a
// view.Resolve carrying the submission as well as the record id, so the
// controls can be resolved from the type being chosen. form.go is that
// resolution; nothing about what this view refuses to render changed.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package credentials

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "credentials"

// typeField names the control that drives the rest of the form.
//
// Deliberately not "type": that name is already this view's read-only
// display column, which renders the type's NAME, while this carries its id.
// One name for two different values in the same form is how a projector and
// a binder end up disagreeing about what a field holds.
const typeField = "credential_type"

// declaredFields declare the shape.
//
// There is deliberately no field for a secret value, and there never will
// be. A Field renders into a table, a form and a detail list, so declaring
// one for a secret would be three separate places it could reach a page.
// The per-type inputs are not here either: they are resolved per submission
// in form.go, because they are not knowable until a type is chosen.
func declaredFields(store typeReader, orgs inventory.OrganizationLister) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, InList: true, InForm: true, MobilePrimary: true,
			Autocomplete: "off",
			Help:         "What this credential is called, within its organization.",
		},
		{
			Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
			MaxLen: 1024, InForm: true,
			Help: "What it is for, for somebody who did not create it.",
		},
		{
			Name: typeField, Label: "CREDENTIAL TYPE", Kind: view.KindSelect,
			Required: true, Immutable: true, InForm: true,
			Options: typeOptions(store),
			// Immutable because the type decides how every stored input is
			// interpreted and where it is injected. Re-pointing a saved
			// credential at a different schema is a replacement, and one
			// that would leave values behind under names the new type never
			// declared. Make a new credential instead.
			Help: "What this credential holds and where its values go at run time. Fixed once saved.",
		},
		{
			Name: "organization", Label: "ORGANIZATION", Kind: view.KindSelect,
			Required: true, Immutable: true, InList: true, InForm: true,
			References: "organizations",
			// Immutable for the reason the Inventories view gives about its
			// own: moving a credential between tenants silently re-scopes
			// who can reach it, and the store's update has no parameter for
			// it regardless, so offering the control would be offering a
			// decision that could be submitted and reported as successful
			// without happening.
			Help: "The tenant that owns this credential. Fixed once saved.",
			Options: func(ctx context.Context) ([]view.Option, error) {
				found, err := orgs.ListOrganizations(ctx)
				if err != nil {
					return nil, err
				}
				out := make([]view.Option, 0, len(found))
				for _, org := range found {
					out = append(out, view.Option{Label: org.Name, Value: strconv.Itoa(org.ID)})
				}
				return out, nil
			},
		},
		{
			// The read-only display column for the same fact typeField
			// carries as an id, kept separate for the reason that const's
			// comment gives.
			Name: "type", Label: "TYPE", Kind: view.KindReadOnly, InList: true,
			References: "credential-types",
			Help:       "What this credential holds and where its values go at run time.",
		},
		{
			Name: "kind", Label: "KIND", Kind: view.KindBadge, InList: true,
			Help:       "The grouping the binding rule keys on: a template takes at most one credential per kind, with vault exempted.",
			BadgeClass: func(string) string { return "badge-neutral" },
		},
		{
			Name: "inputs", Label: "INPUTS", Kind: view.KindReadOnly, InList: true,
			Help: "Which inputs this credential supplies, and where each one comes from. A secret's value is not readable here or through any other port.",
		},
		{
			Name: "bound", Label: "BOUND TO", Kind: view.KindReadOnly, InList: true,
			Help: "How many templates run as this credential. This is what a rotation has to plan around.",
		},
	}
}

// describeInputs says which inputs are supplied and where each comes from,
// without saying what any of them is.
//
// The external ones are named separately and that is the useful part: a
// value stored here and a value fetched from a secret manager at dispatch
// are operationally different things, and during a migration "which
// credentials point at this mount" is a question somebody has to answer.
// The reference is not redacted, because a path is a pointer to a secret
// rather than a secret.
func describeInputs(c credstore.Credential) string {
	if len(c.Inputs) == 0 && len(c.External) == 0 {
		return "none"
	}

	stored := make([]string, 0, len(c.Inputs))
	for id := range c.Inputs {
		if _, external := c.External[id]; external {
			continue
		}
		stored = append(stored, id)
	}
	sort.Strings(stored)

	external := make([]string, 0, len(c.External))
	for id, ref := range c.External {
		external = append(external, id+" from "+ref)
	}
	sort.Strings(external)

	var parts []string
	if len(stored) > 0 {
		parts = append(parts, "stored: "+strings.Join(stored, ", "))
	}
	if len(external) > 0 {
		parts = append(parts, "external: "+strings.Join(external, "; "))
	}
	return strings.Join(parts, " | ")
}

// describeBound reports how many templates run as this credential.
//
// A count rather than the ids, for the reason the Teams view gives about
// its own membership column: a column of primary keys is unreadable where
// the count is the thing being scanned for. Zero is called out rather than
// rendered as "0", because an unbound credential is the interesting row in
// an access review.
func describeBound(c credstore.Credential) string {
	switch len(c.TemplateIDs) {
	case 0:
		return "nothing"
	case 1:
		return "1 template"
	default:
		return fmt.Sprintf("%d templates", len(c.TemplateIDs))
	}
}

// reader adapts the credential store to the view's paging contract.
type reader struct{ store credstore.Store }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[credstore.Credential], error) {
	creds, err := r.store.ListAllCredentials(ctx)
	if err != nil {
		return view.Page[credstore.Credential]{}, err
	}
	if search := strings.ToLower(strings.TrimSpace(q.Search)); search != "" {
		kept := creds[:0]
		for _, c := range creds {
			if strings.Contains(strings.ToLower(c.Name), search) ||
				strings.Contains(strings.ToLower(c.TypeName), search) {
				kept = append(kept, c)
			}
		}
		creds = kept
	}
	return view.Page[credstore.Credential]{Items: creds}, nil
}

func (r reader) Get(ctx context.Context, id string) (credstore.Credential, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return credstore.Credential{}, credstore.ErrNotFound
	}
	return r.store.GetCredential(ctx, numeric)
}

// Register wires the Credentials view over the credential store.
func Register(store credstore.Store, orgs inventory.OrganizationLister) error {
	declared := declaredFields(store, orgs)
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Credentials",
		NavLabel: "CREDENTIALS",
		// Second within Resources, AWX's own position for it: a template
		// names a credential, so it reads directly after the thing that
		// names it and before the catalog that template points into.
		NavOrder: 50,
		NavGroup: view.NavGroupResources,
		Summary:  "Which secrets exist and what runs as them. Never a value.",
		// AWX's credential tabs. Neither is backed: bindings have no
		// credential-scoped level, and the credential store resolves
		// template to credentials rather than the reverse.
		Sections: []view.Section{
			view.Planned("Templates",
				"The templates this credential is bound to, and how it reaches each run.",
				"The store answers which credentials a template binds, not which templates bind a credential, so this needs the inverse index.",
				[]view.Field{
					{Name: "template", Label: "TEMPLATE", Kind: view.KindText, InList: true, MobilePrimary: true, References: "templates"},
					{Name: "injector", Label: "INJECTED AS", Kind: view.KindText, InList: true},
					{Name: "organization", Label: "ORGANIZATION", Kind: view.KindText, InList: true, References: "organizations"},
				}),
			view.Planned("Access",
				"The role bindings that reach this credential.",
				"auth.ScopeType has system, organization, inventory, group and device, and no credential: access to one is inherited from the organization that owns it.",
				[]view.Field{
					{Name: "team", Label: "TEAM", Kind: view.KindText, InList: true, MobilePrimary: true, References: "teams"},
					{Name: "role", Label: "ROLE", Kind: view.KindText, InList: true},
					{Name: "effect", Label: "EFFECT", Kind: view.KindBadge, InList: true},
				}),
		},
		Status:    view.StatusImplemented,
		IDField:   "name",
		Fields:    declared,
		FieldsFor: inputFields(store),
		Ops: view.Ops{
			List:   &apispec.ListCredentials,
			Get:    &apispec.GetCredential,
			Create: &apispec.CreateCredential,
			Update: &apispec.UpdateCredential,
			Delete: &apispec.DeleteCredentialEndpoint,
		},
		Handlers: view.MustBind[credstore.Credential](reader{store}, writer{store}, view.Projector[credstore.Credential]{
			Form: formValues,
			Bind: func(v view.Values) (credstore.Credential, view.FieldErrors) {
				errs := view.FieldErrors{}
				c := credstore.Credential{
					Name:        strings.TrimSpace(v.Get("name")),
					Description: strings.TrimSpace(v.Get("description")),
				}

				// Both are immutable, so view.Values answers empty for them
				// on an edit and the writer's Update ignores them: it reads
				// the stored row for anything it must not change.
				if !v.Editing() {
					typeID, err := strconv.Atoi(strings.TrimSpace(v.Get(typeField)))
					if err != nil || typeID <= 0 {
						errs.Add(typeField, "Choose a credential type.")
					}
					orgID, err := strconv.Atoi(strings.TrimSpace(v.Get("organization")))
					if err != nil || orgID <= 0 {
						errs.Add("organization", "Choose an organization.")
					}
					c.TypeID, c.OrganizationID = typeID, orgID
				}

				c.Inputs = bindInputs(v)
				return c, errs
			},
			Row: func(c credstore.Credential) view.Row {
				return view.Row{
					ID: strconv.Itoa(c.ID),
					Cells: view.Cells{
						"name": c.Name,
						// The type's NAME, never its id, for the reason
						// view.Field.References gives: a cell reading "3"
						// moves a join into the reader's head.
						"type":         c.TypeName,
						"kind":         string(c.Kind),
						"organization": c.OrganizationName,
						"inputs":       describeInputs(c),
						"bound":        describeBound(c),
					},
					Refs: map[string]string{
						"type":         strconv.Itoa(c.TypeID),
						"organization": strconv.Itoa(c.OrganizationID),
					},
				}
			},
		}),
	})
}
