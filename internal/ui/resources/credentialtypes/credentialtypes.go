// Package credentialtypes is the Credential Types view.
//
// The administrative surface behind every bound credential: an inputs
// schema saying what secrets a type holds, and injectors saying how they
// reach a run (environment, extra variables, files). A customer's playbooks
// read the variables these inject, which is why this is the gap that
// decides whether a migration is possible at all.
//
// # Writable, through structured controls only
//
// A credential type's metadata -- its name, description, kind, namespace
// and owning tenant -- is created, edited and deleted here. Its inputs
// schema and injector document are shown as summaries and, for now,
// authored through the API and the import command.
//
// That split is a decision rather than an omission. An injector document is
// executable in every sense that matters: it decides which environment
// variables a customer's playbook runs with, and internal/credtype refuses
// a set of them (LD_PRELOAD and its relatives) precisely because an
// injector that could set one is code execution inside the run. So when
// this view learns to author inputs and injectors it will do so through
// structured, validated repeating rows -- every template compiled through
// the real render engine at save, every name held to credtype's own
// refusals -- never a free-form field a browser could paste an arbitrary
// document into. The metadata form carries none of that risk, which is why
// it lands first.
//
// A managed type belongs to nobody and cannot be edited or deleted, which
// an import relies on to reuse the built-ins rather than recreating them;
// the store refuses either write, and this view withdraws the controls so
// the refusal is not something a reader has to discover by trying.
//
// The Test action is the other half of authoring and carries none of the
// injector risk: it renders a type's injectors against values the caller
// supplies, touches no stored credential, and reports the SHAPE it would
// produce with no value in it.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package credentialtypes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "credential-types"

// declaredFields declare the shape.
//
// The metadata half is writable; the inputs and injectors columns are
// read-only summaries. There is deliberately no field carrying a template's
// rendered output: the columns describe the type, a value belongs to a
// credential, and a credential's secret values are not readable through any
// port this view could hold.
//
// Namespace and organization are immutable, so the edit form drops them:
// the namespace is what an import matches on and the store will not rewrite
// it, and the owner decides who can reach the type. Both are required on a
// create and settled there.
func declaredFields(orgs inventory.OrganizationLister) []view.Field {
	return []view.Field{
		{
			Name: "name", Label: "NAME", Kind: view.KindText,
			Required: true, MaxLen: 253, InList: true, InForm: true, MobilePrimary: true,
			Autocomplete: "off",
			Help:         "What this type is called, within its organization.",
		},
		{
			Name: "description", Label: "DESCRIPTION", Kind: view.KindLongText,
			MaxLen: 1024, InForm: true,
			Help: "What a credential of this type is for, for somebody who did not create it.",
		},
		{
			Name: "kind", Label: "KIND", Kind: view.KindSelect,
			Required: true, InList: true, InForm: true,
			Options: kindOptions,
			Help:    "AWX's coarse grouping. It is what the one-credential-per-kind binding rule keys on.",
		},
		{
			Name: "namespace", Label: "NAMESPACE", Kind: view.KindText,
			Required: true, Immutable: true, InList: true, InForm: true,
			Help: "The stable identifier an import matches on, rather than the display name. Fixed once saved.",
		},
		{
			Name: "inputs", Label: "INPUTS", Kind: view.KindReadOnly, InList: true,
			Help: "What a credential of this type holds. A secret input is marked, and its value is never readable here or anywhere else.",
		},
		{
			Name: "injectors", Label: "INJECTORS", Kind: view.KindReadOnly, InList: true,
			Help: "Where those inputs go at run time: environment variables, extra variables, generated files.",
		},
		{
			Name: "organization", Label: "ORGANIZATION", Kind: view.KindSelect,
			Required: true, Immutable: true, InList: true, InForm: true,
			Options: orgOptions(orgs),
			Help:    "The tenant that owns this type. A managed type belongs to nobody and is usable by everybody. Fixed once saved.",
		},
		{
			Name: "managed", Label: "MANAGED", Kind: view.KindBadge, InList: true,
			Help:       "Whether this platform ships the type. A managed type cannot be edited, which is what lets an import reuse it rather than recreating it.",
			BadgeClass: managedClass,
		},
	}
}

// managedClass distinguishes what this platform ships from what an
// operator wrote, which is the one column here that changes what a reader
// may do with the row.
func managedClass(value string) string {
	if value == "platform" {
		return "badge-neutral"
	}
	return "badge-changed"
}

// testAction renders a type's injectors against caller-supplied values and
// reports the shape it would produce.
//
// The values are the caller's own, never a stored credential's, which is
// what makes this safe to offer: there is nothing to disclose because
// nothing stored is read. The result names keys and never values, for the
// reason credtype.Preview's own comment gives: returning the rendered
// values would turn an authoring aid into an oracle.
func testAction(types credstore.TypeReader, eng render.Engine) view.RecordAction {
	return view.RecordAction{
		Name:     "test",
		Label:    "Test",
		Heading:  "Test this credential type's injectors",
		Endpoint: &apispec.TestCredentialType,
		Fields: []view.Field{{
			Name: "values", Label: "SAMPLE VALUES", Kind: view.KindLongText, InForm: true,
			Help: "One input per line, as id=value. These are yours, not a stored credential's: nothing here is saved and no stored value is read.",
		}},
		Submit: func(ctx context.Context, id string, v view.Values) (string, view.FieldErrors, error) {
			numeric, err := strconv.Atoi(id)
			if err != nil {
				return "", nil, credstore.ErrNotFound
			}
			ct, err := types.GetType(ctx, numeric)
			if err != nil {
				return "", nil, err
			}

			values, bad := parseSampleValues(v.Get("values"))
			if bad != "" {
				errs := view.FieldErrors{}
				errs.Add("values", "Write one input per line as id=value. This line is neither: "+bad+".")
				return "", errs, nil
			}

			if _, err := ct.Injectors.Preview(ct.Inputs, eng, values); err != nil {
				// The preview's own message names the target and the
				// template and quotes no value, so it is shown as written
				// rather than replaced with something vaguer.
				errs := view.FieldErrors{}
				errs.Add("values", err.Error())
				return "", errs, nil
			}
			// An empty redirect, which sends the caller back to this record
			// through the handler's own resourcePath. These four used to
			// build the path by hand and every one of them left off the
			// UI's mount prefix, so a successful write redirected to
			// /templates/1 rather than /ui/templates/1 and answered the
			// operator with a 404 after the save had already happened.
			// The handler knows the prefix; a call site does not.
			return "", nil, nil
		},
	}
}

// parseSampleValues reads the id=value lines the Test action accepts,
// naming the first line that is not one rather than skipping it. Silently
// dropping a malformed line would report a successful preview for a set of
// values the author did not supply.
func parseSampleValues(raw string) (map[string]string, string) {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		id, value, ok := strings.Cut(trimmed, "=")
		id = strings.TrimSpace(id)
		if !ok || id == "" {
			return nil, strconv.Quote(trimmed)
		}
		out[id] = strings.TrimSpace(value)
	}
	return out, ""
}

// reader adapts the type catalog to the view's paging contract.
type reader struct{ types credstore.TypeReader }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[credstore.CredentialType], error) {
	// Every type, across every tenant, which is what the rest of this
	// administrative UI does and why the ORGANIZATION column exists. See
	// credstore.TypeReader.ListAllTypes for what that discloses and what
	// it does not.
	types, err := r.types.ListAllTypes(ctx)
	if err != nil {
		return view.Page[credstore.CredentialType]{}, err
	}
	if search := strings.ToLower(strings.TrimSpace(q.Search)); search != "" {
		kept := types[:0]
		for _, ct := range types {
			if strings.Contains(strings.ToLower(ct.Name), search) ||
				strings.Contains(strings.ToLower(ct.Namespace), search) {
				kept = append(kept, ct)
			}
		}
		types = kept
	}
	return view.Page[credstore.CredentialType]{Items: types}, nil
}

func (r reader) Get(ctx context.Context, id string) (credstore.CredentialType, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return credstore.CredentialType{}, credstore.ErrNotFound
	}
	return r.types.GetType(ctx, numeric)
}

// describeOwner names the tenant a type belongs to.
//
// A managed type has no owner, and saying so is more useful than an empty
// cell, which reads as missing data rather than as a deliberate absence.
func describeOwner(ct credstore.CredentialType) string {
	if ct.Managed || ct.OrganizationID == 0 {
		return "shipped with the platform"
	}
	if ct.OrganizationName != "" {
		return ct.OrganizationName
	}
	return "organization " + strconv.Itoa(ct.OrganizationID)
}

// describeInputs summarises an input schema for a table cell.
//
// A count plus the secret ones named, because those two facts are what a
// reader scans this column for: how much a credential of this type has to
// carry, and which parts of it are secrets they are taking responsibility
// for.
func describeInputs(schema credtype.InputSchema) string {
	if len(schema.Fields) == 0 {
		return "none"
	}
	secrets := schema.SecretFields()
	if len(secrets) == 0 {
		return fmt.Sprintf("%d, none secret", len(schema.Fields))
	}
	return fmt.Sprintf("%d, %d secret (%s)", len(schema.Fields), len(secrets), strings.Join(secrets, ", "))
}

// describeInjectors summarises where a type's inputs go.
//
// It names the targets and counts, never a template, because a template is
// long enough to break the table and a reader comparing types wants the
// shape rather than the text.
func describeInjectors(inj credtype.Injectors) string {
	var parts []string
	if len(inj.Env) > 0 {
		names := make([]string, 0, len(inj.Env))
		for name := range inj.Env {
			names = append(names, name)
		}
		sort.Strings(names)
		parts = append(parts, "env: "+strings.Join(names, ", "))
	}
	if len(inj.ExtraVars) > 0 {
		parts = append(parts, fmt.Sprintf("extra vars: %d", len(inj.ExtraVars)))
	}
	if labels := inj.FileLabels(); len(labels) > 0 {
		parts = append(parts, fmt.Sprintf("files: %d", len(labels)))
	}
	if len(parts) == 0 {
		// Not a gap. A machine or vault credential reaches a run as a Go
		// value rather than through any of these targets, and a reader
		// should not be left wondering whether the row is broken.
		return "none: this type reaches a run directly rather than through injection"
	}
	return strings.Join(parts, "; ")
}

// managedRow reports whether a projected row is a type this platform
// ships, which is the one fact that changes what a reader may do with it.
func managedRow(r view.Row) bool { return r.Cells["managed"] == "platform" }

// Register wires the Credential Types view over the type catalog.
func Register(store credstore.Store, orgs inventory.OrganizationLister, eng render.Engine) error {
	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Credential Types",
		NavLabel: "CREDENTIAL TYPES",
		NavOrder: 140,
		NavGroup: view.NavGroupAdministration,
		Summary:  "What a credential holds, and how its secrets reach the automation.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   declaredFields(orgs),
		Ops: view.Ops{
			List:   &apispec.ListCredentialTypes,
			Get:    &apispec.GetCredentialType,
			Create: &apispec.CreateCredentialType,
			Update: &apispec.UpdateCredentialType,
			Delete: &apispec.DeleteCredentialType,
		},
		// A managed type is refused by the store on every write, so the
		// affordance is withdrawn rather than offered and then refused. It
		// is keyed on the relation because a predicate that ignored it
		// would withdraw every affordance, leaving the page looking as
		// though the caller could do nothing at all.
		//
		// The two set-* relations belong here for the same reason the edit
		// and delete ones do, and were missing: the schema and injector
		// controls reach UpdateType, which refuses a managed type in its
		// second statement, so every one of them was being offered on a
		// platform type and answered with a refusal.
		Applies: func(r view.Row, rel auth.LinkRel) bool {
			if !managedRow(r) {
				return true
			}
			switch rel {
			case apispec.UpdateCredentialType.Rel,
				apispec.DeleteCredentialType.Rel,
				apispec.SetCredentialTypeInputs.Rel,
				apispec.SetCredentialTypeInjectors.Rel:
				return false
			}
			return true
		},
		Sections: []view.Section{inputsSection(store), injectorsSection(store)},
		Actions:  []view.RecordAction{testAction(store, eng), addInputAction(store), addInjectorAction(store)},
		Handlers: view.MustBind[credstore.CredentialType](reader{store}, writer{store}, view.Projector[credstore.CredentialType]{
			Form: formValues,
			Bind: func(v view.Values) (credstore.CredentialType, view.FieldErrors) {
				return bindType(v)
			},
			Row: func(ct credstore.CredentialType) view.Row {
				managedLabel := "custom"
				if ct.Managed {
					managedLabel = "platform"
				}
				return view.Row{
					ID: strconv.Itoa(ct.ID),
					Cells: view.Cells{
						"name":         ct.Name,
						"kind":         string(ct.Kind),
						"namespace":    ct.Namespace,
						"inputs":       describeInputs(ct.Inputs),
						"injectors":    describeInjectors(ct.Injectors),
						"organization": describeOwner(ct),
						"managed":      managedLabel,
					},
				}
			},
		}),
	})
}

// DeclaredNotImplemented re-exports the catalog's own list so a future
// panel can render it without importing the catalog directly.
//
// It exists because the honest answer to "why can I not create a gce
// credential" is a sentence this platform already has, and the worst place
// for that sentence is only in a Go doc comment an operator never reads.
func DeclaredNotImplemented() []managed.NotImplemented { return managed.DeclaredNotImplemented() }
