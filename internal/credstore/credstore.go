// Package credstore persists credential types and credentials.
//
// # The one thing to understand about this package
//
// It cannot return a plaintext secret, and that is enforced by the type
// system rather than by discipline.
//
// PLAN.md Section 29.3 requires that secret fields read back as a redaction
// marker and that there be no plaintext read API at all (Section 17.6). The
// obvious way to satisfy that is a rule: "handlers must remember to redact."
// Rules like that hold until the day somebody adds a handler, and the
// failure is silent and permanent, because a secret that reached a
// response is a secret that reached a log, a proxy, and a browser history.
//
// So the rule is a package boundary instead. Credential, the type every
// method here returns, HAS NO FIELD for a secret input's value: a secret
// reads back as redact.Marker and there is nowhere for the real value to
// go. The interface that can produce plaintext lives in
// internal/credstore/resolve, a separate package, consumed by the injector
// at dispatch and by nothing else, and internal/archtest fails the build if
// internal/api ever imports it.
//
// A handler holding a Store cannot leak a secret by forgetting something.
// It can only leak one by importing a package the build refuses to let it
// import.
package credstore

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// Errors this package returns.
var (
	// ErrNotFound reports an id naming no credential or credential type.
	ErrNotFound = errors.New("credstore: record not found")

	// ErrExists reports a name or namespace already taken.
	ErrExists = errors.New("credstore: record already exists")

	// ErrManaged reports an attempt to modify a type this platform ships.
	// AWX's own rule, and load bearing for imports: a managed type cannot
	// be edited, so an import must recognize and reuse the built-ins
	// rather than recreating them as custom types.
	ErrManaged = errors.New("credstore: a managed credential type cannot be modified")

	// ErrInUse reports an attempt to delete something still referenced.
	ErrInUse = errors.New("credstore: record is still in use")

	// ErrCrossOrganization reports a reference that would cross a tenancy
	// boundary. ent cannot express this, the same way it cannot express
	// Template's own inventory check, so it is refused here.
	ErrCrossOrganization = errors.New("credstore: record belongs to a different organization")
)

// CredentialType is a stored credential type: the domain declaration plus
// the identity storage gives it.
//
// The domain type is embedded rather than copied field by field, so a
// caller reads ct.Name and ct.Injectors exactly as it would on the domain
// type, and adding an input field never needs a change here.
//
// The identity lives HERE rather than on credtype.CredentialType, because
// that type is also the shape of an ent field.JSON column and of an AWX
// export. A database id inside a JSON column would be a second, disagreeing
// answer to "which row is this", and an id in an export is meaningless to
// whoever imports it.
type CredentialType struct {
	// ID identifies the stored row.
	ID int

	// OrganizationID is the owning tenant, and zero for a managed type,
	// which belongs to nobody and is usable by everybody.
	OrganizationID int

	// OrganizationName is the tenant's name, carried so a cross-tenant
	// list renders the name rather than the primary key. A column reading
	// "ORGANIZATION: 1" has not saved the reader a join, it has moved the
	// join into their head; Credential already carries the name for the
	// same reason.
	OrganizationName string

	credtype.CredentialType
}

// Credential is the read projection of one credential.
//
// Inputs carries a value for every input the credential holds, with every
// SECRET one replaced by redact.Marker. That is not a courtesy applied on
// the way out; it is applied here, at the only place credentials are read
// through this interface, so no caller can be handed the real thing by
// accident.
//
// A marker rather than an omitted key, because absent and withheld are
// different facts and a form rendering the empty string would silently
// clear the stored value on the next save. internal/launch's own saved
// configuration redaction made the identical choice, and they share the one
// marker definition.
type Credential struct {
	ID          int
	Name        string
	Description string

	// TypeID and the three fields after it describe the credential's type
	// without requiring a second fetch, because every caller that reads a
	// credential also needs to know what it is.
	TypeID        int
	TypeName      string
	TypeNamespace string
	Kind          credtype.Kind

	OrganizationID   int
	OrganizationName string

	// Inputs holds redacted values. See the type comment.
	Inputs map[string]string

	// External maps an input id to its external secret-manager reference.
	// NOT redacted, deliberately: a Vault path is a pointer to a secret
	// rather than a secret, and hiding it would make "which credentials
	// point at this mount" unanswerable during a migration.
	External map[string]string

	// TemplateIDs are the templates this credential is bound to.
	TemplateIDs []int
}

// InputSource is one binding between a credential's input and the
// credential that supplies its value from an external secret manager.
//
// It is the row form of what Credential.External says as a string, and the
// two coexist: internal/credstore/resolve's graph.go carries the reasoning
// for why one did not replace the other.
type InputSource struct {
	ID int

	// InputID is the id of the TARGET credential's input this binding
	// supplies.
	InputID string

	// SourceCredentialID and the two fields after it describe the source
	// without requiring a second fetch, matching Credential's own
	// convention for its type.
	SourceCredentialID        int
	SourceCredentialName      string
	SourceCredentialNamespace string

	// Metadata is the source's own per-field addressing: for a HashiCorp
	// Vault source, the secret path, the key within it, and optionally a
	// version.
	//
	// NOT redacted, deliberately, and for the identical reason External is
	// not: a path is a pointer to a secret rather than a secret, and hiding
	// it would make "which credentials point at this mount" unanswerable
	// during a migration or an incident.
	Metadata map[string]string
}

// InputSourceBinding is one binding as a caller writes it.
//
// Separate from InputSource because a write names the source by id and a
// read carries its name and namespace too. Reusing one struct would mean a
// caller had to populate fields the store ignores, which is how a caller
// comes to believe it can rename a source by writing to it.
type InputSourceBinding struct {
	InputID            string
	SourceCredentialID int
	Metadata           map[string]string
}

// CredentialOption configures a credential write.
//
// Variadic options rather than more parameters, because the two writers
// already take six arguments each and because every existing caller means
// "no source bindings" and should keep saying so by saying nothing.
type CredentialOption func(*credentialWrite)

// credentialWrite carries what the options set.
type credentialWrite struct {
	sources    []InputSourceBinding
	hasSources bool
}

// WithInputSources binds inputs of the credential being written to source
// credentials, in the same write.
//
// Passing an empty slice is meaningful and different from not passing the
// option at all: it clears every binding, the way sending an empty list to
// SetCredentialInputSources does.
func WithInputSources(sources []InputSourceBinding) CredentialOption {
	return func(w *credentialWrite) {
		w.sources = sources
		w.hasSources = true
	}
}

// applyCredentialOptions collects the options into one value.
func applyCredentialOptions(opts []CredentialOption) credentialWrite {
	var w credentialWrite
	for _, opt := range opts {
		opt(&w)
	}
	return w
}

// sourcedIDs returns the input ids these bindings supply, for the required
// input check.
func sourcedIDs(bindings []InputSourceBinding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.InputID)
	}
	return out
}

// Bound projects this credential into the shape the binding rule reads.
//
// The vault identifier is carried on the projection rather than read out of
// Inputs, because Inputs is redacted here and vault_id may be a secret
// field. The store extracts it once, from the real values, at read time.
type Bound struct {
	credtype.Bound
}

// TypeReader is the read-only half of the type catalog.
//
// It is a narrower interface than Store on purpose: the UI's credential
// type view and the credential form both need to list and read types and
// neither should be able to write one, so they take this.
type TypeReader interface {
	// GetType returns one credential type.
	GetType(ctx context.Context, id int) (CredentialType, error)

	// GetTypeByNamespace returns the type with this stable identifier,
	// which is what an import keys on.
	GetTypeByNamespace(ctx context.Context, namespace string) (CredentialType, error)

	// ListTypes returns the types visible to an organization: its own
	// custom ones plus every managed type, which belong to nobody.
	ListTypes(ctx context.Context, organizationID int) ([]CredentialType, error)

	// ListAllTypes returns every credential type across every tenant.
	//
	// It is a separate method rather than ListTypes with a zero
	// organization, because zero already means something there: the
	// managed types and nothing else. Overloading it would have widened
	// an existing API response, since the credential-type endpoint passes
	// zero when its organization query parameter is absent.
	//
	// It exists for the administrative UI, which is cross-tenant by
	// construction: its Inventories, Teams and Organizations views all
	// list every record with an ORGANIZATION column, and a Credential
	// Types view that showed only the managed ones would be a list of six
	// rows that never changes. A type is a SCHEMA rather than a secret --
	// it declares that a token exists, never what it is -- so this
	// discloses which vendors a deployment integrates with and nothing
	// more. That is a real disclosure and it is why this is a distinct
	// method behind the credential:read scope rather than the default
	// reading of the one above.
	ListAllTypes(ctx context.Context) ([]CredentialType, error)
}

// Store is the whole persistence surface.
//
// Every method that returns a credential returns the redacted projection.
// There is no method here that returns a plaintext input value, and adding
// one would defeat the boundary this package exists to draw.
type Store interface {
	TypeReader

	// CreateType stores a new custom credential type. It refuses a type
	// that does not validate, a duplicate namespace, and a name already
	// taken within the organization.
	CreateType(ctx context.Context, organizationID int, ct credtype.CredentialType) (CredentialType, error)

	// UpdateType replaces a custom type's editable fields. It refuses a
	// managed type outright.
	UpdateType(ctx context.Context, id int, ct credtype.CredentialType) (CredentialType, error)

	// DeleteType removes a custom type. It refuses a managed type, and a
	// type that credentials still reference, because a credential whose
	// type vanished cannot be injected and finding that out at launch is
	// worse than being refused here.
	DeleteType(ctx context.Context, id int) error

	// EnsureManagedType creates or updates a type this platform ships,
	// keyed on namespace.
	//
	// This is the one method permitted to write a managed row, and it
	// exists because managed types are reconciled at startup rather than
	// installed by a migration: a migration cannot be re-run when a later
	// release adds a managed type, and a reconcile can.
	EnsureManagedType(ctx context.Context, ct credtype.CredentialType) (CredentialType, error)

	// GetCredential returns one credential, redacted.
	GetCredential(ctx context.Context, id int) (Credential, error)

	// ListCredentials returns an organization's credentials, redacted.
	ListCredentials(ctx context.Context, organizationID int) ([]Credential, error)

	// ListAllCredentials returns every credential across every tenant,
	// redacted.
	//
	// The symmetric method to ListAllTypes and it exists for the same
	// consumer, but it needs its own justification because the disclosure
	// is larger: a credential NAME is operational information in a way a
	// type name is not.
	//
	// Two things make it the right thing to offer. The projection cannot
	// carry a secret value at all, so what is disclosed is the existence
	// of a credential, its type, its tenant and what it is bound to.
	// And the alternative is worse: rotation is impossible without
	// enumeration, and "which credentials exist and which are stale" is
	// the single question an operator most needs answered about secrets.
	// See internal/ui/resources/credentials for the revision of the
	// earlier commitment not to enumerate, and why the scope rather than
	// the absence of a list is the control.
	ListAllCredentials(ctx context.Context) ([]Credential, error)

	// CreateCredential stores a new credential. inputs carries real
	// values on the way IN, which is the asymmetry this whole package
	// rests on: writing a secret is ordinary, reading one back is not
	// possible.
	//
	// WithInputSources makes the credential and its source bindings one
	// write. That is not a convenience: a required input supplied by a
	// source has no stored value, so creating the credential first and
	// binding it second would have to either refuse the create or leave a
	// window in which the credential exists and cannot authenticate.
	CreateCredential(ctx context.Context, organizationID, typeID int, name, description string, inputs, external map[string]string, opts ...CredentialOption) (Credential, error)

	// UpdateCredential replaces a credential's values.
	//
	// An input whose supplied value is redact.Marker is left at whatever
	// is already stored, which is what lets a form round-trip: it renders
	// the marker for a secret, the operator edits an unrelated field, and
	// submitting does not overwrite the secret with the marker text.
	//
	// Bindings are left alone unless WithInputSources is passed, so an
	// ordinary edit of a name or an unrelated field cannot silently unbind
	// an input. When it is passed it replaces the whole set, matching
	// SetCredentialInputSources.
	UpdateCredential(ctx context.Context, id int, name, description string, inputs, external map[string]string, opts ...CredentialOption) (Credential, error)

	// DeleteCredential removes a credential and every binding to it.
	DeleteCredential(ctx context.Context, id int) error

	// TemplateCredentials returns the credentials bound to a template,
	// redacted.
	TemplateCredentials(ctx context.Context, templateID int) ([]Credential, error)

	// SetTemplateCredentials replaces a template's bindings.
	//
	// It runs credtype.CheckBinding before writing, so the one-per-kind
	// rule with vault exempted is enforced at the store as well as at the
	// handler. Two callers, one implementation: the handler checks so the
	// caller gets a conflict naming both credentials, and the store checks
	// so a second writer cannot skip it.
	SetTemplateCredentials(ctx context.Context, templateID int, credentialIDs []int) error

	// ListCredentialInputSources returns one credential's input bindings,
	// ordered by input id.
	ListCredentialInputSources(ctx context.Context, credentialID int) ([]InputSource, error)

	// SetCredentialInputSources replaces a credential's input bindings.
	//
	// It refuses, before writing any of them, an input the credential's
	// type does not declare, a source in another organization, a source
	// that does not name a readable field, a secret read into an input that
	// is not secret, and a set that would make
	// resolution return to the credential it started from. Each of those
	// would otherwise surface at dispatch, which is days later and
	// somebody else's job.
	SetCredentialInputSources(ctx context.Context, credentialID int, bindings []InputSourceBinding) ([]InputSource, error)
}
