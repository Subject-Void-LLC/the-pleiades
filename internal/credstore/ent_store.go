package credstore

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credentialtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// entStore is the ent-backed Store.
//
// Reads arrive here already decrypted, because the composition root
// registers crypto.CredentialInputsInterceptor on the client. That is what
// makes the redaction in this file the ONLY thing standing between stored
// plaintext and a caller, and why it happens in one function that every
// read path goes through rather than at each call site.
type entStore struct {
	client *ent.Client

	// engine validates a credential type's injector templates at the
	// write, so an author learns about a template that references an input
	// the type does not declare when they save it rather than when an
	// operator launches a job.
	engine render.Engine
}

// NewEntStore returns a Store over an ent client.
//
// It panics on a nil client or engine, following internal/launch's
// NewEntStore precedent: both are supplied by a composition root, so a nil
// one is a wiring error that must fail at process start rather than at the
// first request. A store that silently skipped injector validation would
// accept credential types that cannot render.
func NewEntStore(client *ent.Client, engine render.Engine) Store {
	if client == nil {
		panic("credstore: NewEntStore requires an ent client")
	}
	if engine == nil {
		panic("credstore: NewEntStore requires a render engine, or injector templates are never validated")
	}
	return &entStore{client: client, engine: engine}
}

// GetType returns one credential type.
func (s *entStore) GetType(ctx context.Context, id int) (CredentialType, error) {
	return s.loadType(ctx, credentialtype.IDEQ(id))
}

// GetTypeByNamespace returns the type with this stable identifier.
func (s *entStore) GetTypeByNamespace(ctx context.Context, namespace string) (CredentialType, error) {
	return s.loadType(ctx, credentialtype.NamespaceEQ(namespace))
}

// loadType fetches one type with the edges every projection needs.
//
// Every type read goes through here rather than through Client.Get, because
// Get cannot eager-load an edge and the projection carries the owning
// organization. An earlier version used Get and silently reported every
// custom type as belonging to organization zero, which a caller would have
// read as "managed, usable by everyone".
func (s *entStore) loadType(ctx context.Context, where predicate.CredentialType) (CredentialType, error) {
	row, err := s.client.CredentialType.Query().
		Where(where).
		WithOrganization().
		Only(ctx)
	if err != nil {
		return CredentialType{}, wrapNotFound(err, "credential type")
	}
	return typeFromRow(row), nil
}

// ListTypes returns an organization's own types plus every managed type.
//
// Managed types belong to nobody and are usable by everybody, so a query
// filtered only by organization would hide every built-in from every
// tenant, which is the whole catalog.
func (s *entStore) ListTypes(ctx context.Context, organizationID int) ([]CredentialType, error) {
	rows, err := s.client.CredentialType.Query().
		Where(credentialtype.Or(
			credentialtype.Managed(true),
			credentialtype.HasOrganizationWith(organization.IDEQ(organizationID)),
		)).
		WithOrganization().
		Order(ent.Asc(credentialtype.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("credstore: listing credential types: %w", err)
	}

	out := make([]CredentialType, 0, len(rows))
	for _, row := range rows {
		out = append(out, typeFromRow(row))
	}
	return out, nil
}

// ListAllTypes returns every credential type across every tenant. See the
// interface for why this is a separate method rather than a zero
// organization.
func (s *entStore) ListAllTypes(ctx context.Context) ([]CredentialType, error) {
	rows, err := s.client.CredentialType.Query().
		WithOrganization().
		Order(ent.Asc(credentialtype.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("credstore: listing every credential type: %w", err)
	}

	out := make([]CredentialType, 0, len(rows))
	for _, row := range rows {
		out = append(out, typeFromRow(row))
	}
	return out, nil
}

// CreateType stores a new custom credential type.
func (s *entStore) CreateType(ctx context.Context, organizationID int, ct credtype.CredentialType) (CredentialType, error) {
	if err := ct.Validate(s.engine); err != nil {
		return CredentialType{}, err
	}
	if ct.Managed {
		// EnsureManagedType is the one method permitted to write a managed
		// row. Letting this one do it would mean an API caller could mint
		// a type nobody can subsequently edit or delete.
		return CredentialType{}, fmt.Errorf("%w: use the managed-type reconcile instead", ErrManaged)
	}

	row, err := s.client.CredentialType.Create().
		SetName(ct.Name).
		SetDescription(ct.Description).
		SetKind(string(ct.Kind)).
		SetNamespace(ct.Namespace).
		SetManaged(false).
		SetInputs(ct.Inputs).
		SetInjectors(ct.Injectors).
		SetOrganizationID(organizationID).
		Save(ctx)
	if err != nil {
		return CredentialType{}, wrapConstraint(err, "credential type")
	}
	return s.loadType(ctx, credentialtype.IDEQ(row.ID))
}

// UpdateType replaces a custom type's editable fields.
func (s *entStore) UpdateType(ctx context.Context, id int, ct credtype.CredentialType) (CredentialType, error) {
	existing, err := s.client.CredentialType.Get(ctx, id)
	if err != nil {
		return CredentialType{}, wrapNotFound(err, "credential type")
	}
	if existing.Managed {
		return CredentialType{}, fmt.Errorf("%w: %q is shipped by this platform", ErrManaged, existing.Name)
	}

	// The namespace is immutable in the schema, so the caller's value is
	// ignored rather than refused: an update that omits it should not fail
	// on a field it was never allowed to change. Validation runs against
	// the stored one so a caller cannot validate against a namespace that
	// will not be written.
	ct.Namespace = existing.Namespace
	if err := ct.Validate(s.engine); err != nil {
		return CredentialType{}, err
	}

	row, err := s.client.CredentialType.UpdateOneID(id).
		SetName(ct.Name).
		SetDescription(ct.Description).
		SetKind(string(ct.Kind)).
		SetInputs(ct.Inputs).
		SetInjectors(ct.Injectors).
		Save(ctx)
	if err != nil {
		return CredentialType{}, wrapConstraint(err, "credential type")
	}
	return s.loadType(ctx, credentialtype.IDEQ(row.ID))
}

// DeleteType removes a custom type.
func (s *entStore) DeleteType(ctx context.Context, id int) error {
	existing, err := s.client.CredentialType.Get(ctx, id)
	if err != nil {
		return wrapNotFound(err, "credential type")
	}
	if existing.Managed {
		return fmt.Errorf("%w: %q is shipped by this platform", ErrManaged, existing.Name)
	}

	// Refused rather than cascaded. A credential whose type vanished
	// cannot be injected, and discovering that at launch is worse than
	// being refused here with a count.
	used, err := s.client.Credential.Query().
		Where(credential.HasCredentialTypeWith(credentialtype.IDEQ(id))).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("credstore: counting credentials of type %d: %w", id, err)
	}
	if used > 0 {
		return fmt.Errorf("%w: %d credential(s) still use the type %q", ErrInUse, used, existing.Name)
	}

	if err := s.client.CredentialType.DeleteOneID(id).Exec(ctx); err != nil {
		return wrapNotFound(err, "credential type")
	}
	return nil
}

// EnsureManagedType creates or updates a type this platform ships.
//
// Keyed on namespace and idempotent, because managed types are reconciled
// at every startup rather than installed once: a migration cannot be re-run
// when a later release adds a type, and a reconcile can. The managed=true
// refusal on the other methods is what keeps this the only writer.
func (s *entStore) EnsureManagedType(ctx context.Context, ct credtype.CredentialType) (CredentialType, error) {
	ct.Managed = true
	if err := ct.Validate(s.engine); err != nil {
		return CredentialType{}, err
	}

	existing, err := s.client.CredentialType.Query().
		Where(credentialtype.NamespaceEQ(ct.Namespace)).
		Only(ctx)
	switch {
	case err == nil:
		return s.adoptExistingType(ctx, existing, ct)

	case ent.IsNotFound(err):
		row, createErr := s.client.CredentialType.Create().
			SetName(ct.Name).
			SetDescription(ct.Description).
			SetKind(string(ct.Kind)).
			SetNamespace(ct.Namespace).
			SetManaged(true).
			SetInputs(ct.Inputs).
			SetInjectors(ct.Injectors).
			Save(ctx)
		if createErr == nil {
			return s.loadType(ctx, credentialtype.IDEQ(row.ID))
		}
		if !ent.IsConstraintError(createErr) {
			return CredentialType{}, wrapConstraint(createErr, "credential type")
		}

		// The namespace was free at the Query above and taken by the time
		// this Create ran, so somebody else installed it in between. That
		// is the ordinary outcome of two controllers cold-starting against
		// one shared database, not an error: this method is documented as
		// idempotent precisely because it is reconciled at every startup.
		//
		// Reporting the constraint as ErrExists is what this used to do and
		// is wrong twice over. It is the same sentinel a genuine collision
		// with a CUSTOM type returns, so ReconcileManaged cannot tell them
		// apart and tells the operator to "rename the custom credential
		// type to free the namespace" -- naming a type that does not exist,
		// over a row that is already correct. And it fails a reconcile that
		// in fact succeeded, just not by our hand.
		//
		// Re-reading and running the row through the same handler the
		// err == nil branch uses is what makes the two paths agree by
		// construction: a managed winner is adopted, and a custom winner
		// still returns ErrExists, which is the one case an operator does
		// have to act on.
		winner, reReadErr := s.client.CredentialType.Query().
			Where(credentialtype.NamespaceEQ(ct.Namespace)).
			Only(ctx)
		if reReadErr != nil {
			return CredentialType{}, fmt.Errorf(
				"credstore: the namespace %q was claimed while installing managed type %q and could not be re-read: %w",
				ct.Namespace, ct.Name, reReadErr)
		}
		return s.adoptExistingType(ctx, winner, ct)

	default:
		return CredentialType{}, fmt.Errorf("credstore: looking up managed type %q: %w", ct.Namespace, err)
	}
}

// adoptExistingType reconciles ct onto a row that already holds its
// namespace, whether this call found that row itself or lost a race to
// whoever created it.
//
// Both callers must behave identically, which is the entire reason this is
// one function rather than two similar blocks: the difference between "the
// row was there when I looked" and "the row appeared while I was writing"
// is timing, and an operator reading the result should not be able to tell
// which happened.
func (s *entStore) adoptExistingType(ctx context.Context, existing *ent.CredentialType, ct credtype.CredentialType) (CredentialType, error) {
	if !existing.Managed {
		// A custom type already holds this namespace. Overwriting it
		// would silently replace something an operator wrote with
		// something this platform ships.
		return CredentialType{}, fmt.Errorf(
			"%w: the namespace %q is held by a custom credential type", ErrExists, ct.Namespace)
	}

	row, updateErr := s.client.CredentialType.UpdateOneID(existing.ID).
		SetName(ct.Name).
		SetDescription(ct.Description).
		SetKind(string(ct.Kind)).
		SetInputs(ct.Inputs).
		SetInjectors(ct.Injectors).
		Save(ctx)
	if updateErr != nil {
		return CredentialType{}, fmt.Errorf("credstore: reconciling managed type %q: %w", ct.Namespace, updateErr)
	}
	return s.loadType(ctx, credentialtype.IDEQ(row.ID))
}

// wrapNotFound turns ent's own not-found into this package's sentinel.
func wrapNotFound(err error, what string) error {
	if ent.IsNotFound(err) {
		return fmt.Errorf("%w: no such %s", ErrNotFound, what)
	}
	return fmt.Errorf("credstore: reading %s: %w", what, err)
}

// wrapConstraint turns a uniqueness violation into this package's sentinel.
//
// The message deliberately does not echo the value that collided: a
// credential name is not a secret, but this helper is also used on paths
// where an error is returned to an unauthenticated caller, and "that name
// exists" is a smaller disclosure than repeating what was tried.
func wrapConstraint(err error, what string) error {
	if ent.IsConstraintError(err) {
		return fmt.Errorf("%w: a %s with that name or namespace already exists", ErrExists, what)
	}
	return fmt.Errorf("credstore: writing %s: %w", what, err)
}

// typeFromRow converts a stored row into the read projection.
//
// The organization edge is read from Edges rather than queried, so a caller
// that did not eager-load it gets zero rather than a second round trip. A
// managed type legitimately has none, which is why zero is a meaningful
// value here rather than a missing one.
func typeFromRow(row *ent.CredentialType) CredentialType {
	out := CredentialType{
		ID: row.ID,
		CredentialType: credtype.CredentialType{
			Name:        row.Name,
			Description: row.Description,
			Kind:        credtype.Kind(row.Kind),
			Namespace:   row.Namespace,
			Managed:     row.Managed,
			Inputs:      row.Inputs,
			Injectors:   row.Injectors,
		},
	}
	if org := row.Edges.Organization; org != nil {
		out.OrganizationID, out.OrganizationName = org.ID, org.Name
	}
	return out
}

// redactInputs is the single place a stored credential's values become a
// readable projection, and the reason this package can claim there is no
// plaintext read API.
//
// Every read path in this file goes through it. A secret input's value is
// replaced by the marker; a non-secret one is returned as stored, because
// a region or a username is not a secret and hiding it would make the
// credential unmanageable.
//
// An input the type no longer declares is dropped rather than returned. A
// type edited to remove a field leaves values behind in every credential of
// that type, and those values were secret under a schema that no longer
// says so: returning them would be a disclosure caused by an edit nobody
// connected to it.
func redactInputs(schema credtype.InputSchema, stored map[string]string) map[string]string {
	secret := make(map[string]struct{})
	for _, id := range schema.SecretFields() {
		secret[id] = struct{}{}
	}

	out := make(map[string]string, len(stored))
	for _, field := range schema.Fields {
		value, present := stored[field.ID]
		if !present {
			continue
		}
		if _, isSecret := secret[field.ID]; isSecret {
			if value != "" {
				out[field.ID] = redact.Marker
			}
			continue
		}
		out[field.ID] = value
	}
	return out
}
