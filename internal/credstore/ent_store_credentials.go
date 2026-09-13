package credstore

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/predicate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/template"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage"
)

// The credential half of the ent store. Split from ent_store.go for the
// ~300 line file convention, not because it is a different concern.

// GetCredential returns one credential, redacted.
func (s *entStore) GetCredential(ctx context.Context, id int) (Credential, error) {
	row, err := s.loadCredential(ctx, credential.IDEQ(id))
	if err != nil {
		return Credential{}, err
	}
	return s.project(ctx, row)
}

// ListCredentials returns an organization's credentials, redacted.
func (s *entStore) ListCredentials(ctx context.Context, organizationID int) ([]Credential, error) {
	rows, err := s.client.Credential.Query().
		Where(credential.HasOrganizationWith(organization.IDEQ(organizationID))).
		WithCredentialType().
		WithOrganization().
		WithTemplates().
		Order(ent.Asc(credential.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("credstore: listing credentials: %w", err)
	}

	out := make([]Credential, 0, len(rows))
	for _, row := range rows {
		projected, projectErr := s.project(ctx, row)
		if projectErr != nil {
			return nil, projectErr
		}
		out = append(out, projected)
	}
	return out, nil
}

// ListAllCredentials returns every credential across every tenant,
// redacted. See the interface for what that discloses and why it is
// offered.
//
// It shares project() with every other read path, which is the part that
// matters: the redaction is applied in one function rather than per query,
// so a new listing cannot be the one that forgets it.
func (s *entStore) ListAllCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := s.client.Credential.Query().
		WithCredentialType().
		WithOrganization().
		WithTemplates().
		Order(ent.Asc(credential.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("credstore: listing every credential: %w", err)
	}

	out := make([]Credential, 0, len(rows))
	for _, row := range rows {
		projected, projectErr := s.project(ctx, row)
		if projectErr != nil {
			return nil, projectErr
		}
		out = append(out, projected)
	}
	return out, nil
}

// CreateCredential stores a new credential.
//
// inputs carries REAL values on the way in, which is the asymmetry this
// package rests on: writing a secret is ordinary, reading one back is not
// possible. The hook on the client encrypts them before they reach the
// database; nothing here sees ciphertext or plaintext twice.
func (s *entStore) CreateCredential(ctx context.Context, organizationID, typeID int, name, description string, inputs, external map[string]string, opts ...CredentialOption) (Credential, error) {
	write := applyCredentialOptions(opts)
	ct, err := s.client.CredentialType.Get(ctx, typeID)
	if err != nil {
		return Credential{}, wrapNotFound(err, "credential type")
	}

	// A custom type belongs to one tenant, and a credential of it must
	// belong to the same one. A managed type belongs to nobody and is
	// usable by everybody, so it is exempt. ent cannot express this, the
	// same way it cannot express Template's own inventory check, and
	// getting it wrong would let one tenant build a credential on another
	// tenant's type definition.
	if !ct.Managed {
		owner, ownerErr := ct.QueryOrganization().Only(ctx)
		if ownerErr != nil {
			return Credential{}, fmt.Errorf("credstore: reading the credential type's organization: %w", ownerErr)
		}
		if owner.ID != organizationID {
			return Credential{}, fmt.Errorf(
				"%w: the credential type %q belongs to another organization", ErrCrossOrganization, ct.Name)
		}
	}

	// One check, both maps. CheckValues validates the stored values and the
	// external references against the same schema, including the rule that
	// a required input may be satisfied by an external reference rather
	// than by a stored value. This package used to run a second, separate
	// check over external alone; it was the same rule written twice, which
	// is the duplication PLAN.md Section 25 forbids, and it was deleted
	// rather than kept in sync.
	//
	// A required input supplied by a source binding is the fourth
	// exemption, alongside a default, an external reference and a launch
	// prompt. It has to be passed in here rather than read back, because
	// on a create the bindings do not exist yet: that ordering is the
	// whole reason WithInputSources exists.
	if err := ct.Inputs.CheckValues(inputs, external, sourcedIDs(write.sources)...); err != nil {
		return Credential{}, err
	}

	// The credential and its bindings are one transaction. A partial
	// application would leave a credential whose required input has no
	// value and no source, which fails at injection rather than here.
	var createdID int
	if err := storage.NewEntUnitOfWork(s.client).WithTx(ctx, func(txCtx context.Context) error {
		tx := ent.FromContext(txCtx)
		row, createErr := tx.Credential.Create().
			SetName(name).
			SetDescription(description).
			SetOrganizationID(organizationID).
			SetCredentialTypeID(typeID).
			SetInputs(inputs).
			SetExternal(external).
			Save(txCtx)
		if createErr != nil {
			return wrapConstraint(createErr, "credential")
		}
		createdID = row.ID

		if !write.hasSources {
			return nil
		}
		return s.writeInputSources(txCtx, row.ID, ct.Inputs, organizationID, write.sources)
	}); err != nil {
		return Credential{}, err
	}

	loaded, err := s.loadCredential(ctx, credential.IDEQ(createdID))
	if err != nil {
		return Credential{}, err
	}
	return s.project(ctx, loaded)
}

// UpdateCredential replaces a credential's values.
//
// An input whose supplied value is the redaction marker is left at whatever
// is already stored. That is what lets a form round-trip: it renders the
// marker for a secret, the operator edits an unrelated field, and
// submitting does not overwrite the real secret with the literal text
// "$encrypted$". Without this the first edit of any credential would
// destroy every secret on it, which is a data-loss bug that looks like a
// successful save.
func (s *entStore) UpdateCredential(ctx context.Context, id int, name, description string, inputs, external map[string]string, opts ...CredentialOption) (Credential, error) {
	write := applyCredentialOptions(opts)

	row, err := s.loadCredential(ctx, credential.IDEQ(id))
	if err != nil {
		return Credential{}, err
	}
	ct := row.Edges.CredentialType
	org := row.Edges.Organization
	if ct == nil || org == nil {
		return Credential{}, fmt.Errorf("credstore: credential %d was read without its type or organization", id)
	}

	// Which inputs count as sourced depends on whether this write replaces
	// the bindings. When it does not, the stored ones still stand, and
	// reading them back is what stops an ordinary rename from failing the
	// required check on an input that has a source it is not touching.
	sourced := sourcedIDs(write.sources)
	if !write.hasSources {
		existing, listErr := s.ListCredentialInputSources(ctx, id)
		if listErr != nil {
			return Credential{}, listErr
		}
		sourced = sourced[:0]
		for _, e := range existing {
			sourced = append(sourced, e.InputID)
		}
	}

	merged := mergeInputs(row.Inputs, inputs)
	if err := ct.Inputs.CheckValues(merged, external, sourced...); err != nil {
		return Credential{}, err
	}

	if err := storage.NewEntUnitOfWork(s.client).WithTx(ctx, func(txCtx context.Context) error {
		tx := ent.FromContext(txCtx)
		if _, updateErr := tx.Credential.UpdateOneID(id).
			SetName(name).
			SetDescription(description).
			SetInputs(merged).
			SetExternal(external).
			Save(txCtx); updateErr != nil {
			return wrapConstraint(updateErr, "credential")
		}
		if !write.hasSources {
			return nil
		}
		return s.writeInputSources(txCtx, id, ct.Inputs, org.ID, write.sources)
	}); err != nil {
		return Credential{}, err
	}

	updated, err := s.loadCredential(ctx, credential.IDEQ(id))
	if err != nil {
		return Credential{}, err
	}
	return s.project(ctx, updated)
}

// DeleteCredential removes a credential and every binding to it.
//
// The join rows cascade, so a template that bound this credential simply
// stops binding it. That is correct: the alternative, refusing to delete a
// bound credential, would mean a compromised credential could not be
// removed until every template that used it was edited first, which is
// exactly backwards during an incident.
func (s *entStore) DeleteCredential(ctx context.Context, id int) error {
	if err := s.client.Credential.DeleteOneID(id).Exec(ctx); err != nil {
		return wrapNotFound(err, "credential")
	}
	return nil
}

// TemplateCredentials returns the credentials bound to a template.
func (s *entStore) TemplateCredentials(ctx context.Context, templateID int) ([]Credential, error) {
	rows, err := s.client.Credential.Query().
		Where(credential.HasTemplatesWith(template.IDEQ(templateID))).
		WithCredentialType().
		WithOrganization().
		WithTemplates().
		Order(ent.Asc(credential.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("credstore: listing a template's credentials: %w", err)
	}

	out := make([]Credential, 0, len(rows))
	for _, row := range rows {
		projected, projectErr := s.project(ctx, row)
		if projectErr != nil {
			return nil, projectErr
		}
		out = append(out, projected)
	}
	return out, nil
}

// SetTemplateCredentials replaces a template's bindings.
//
// It runs credtype.CheckBinding before writing. The handler runs it too, so
// a caller gets a conflict naming both credentials rather than an opaque
// store error, and this one exists so a second writer cannot skip it. Two
// callers, one implementation.
func (s *entStore) SetTemplateCredentials(ctx context.Context, templateID int, credentialIDs []int) error {
	tmpl, err := s.client.Template.Get(ctx, templateID)
	if err != nil {
		return wrapNotFound(err, "template")
	}
	owner, err := tmpl.QueryOrganization().Only(ctx)
	if err != nil {
		return fmt.Errorf("credstore: reading the template's organization: %w", err)
	}

	bound, err := s.boundFor(ctx, credentialIDs, owner.ID)
	if err != nil {
		return err
	}
	if err := credtype.CheckBinding(bound); err != nil {
		return err
	}

	if err := s.client.Template.UpdateOneID(templateID).
		ClearCredentials().
		AddCredentialIDs(credentialIDs...).
		Exec(ctx); err != nil {
		return fmt.Errorf("credstore: binding credentials to template %d: %w", templateID, err)
	}
	return nil
}

// boundFor loads the binding projection for a set of credential ids,
// refusing any that belong to another organization.
//
// The tenancy check lives here rather than only at the handler because this
// is where the write happens. A template in one tenant binding another
// tenant's credential would dispatch jobs authenticating as somebody else,
// which is the same shape as Template's own cross-tenant inventory refusal.
func (s *entStore) boundFor(ctx context.Context, ids []int, organizationID int) ([]credtype.Bound, error) {
	out := make([]credtype.Bound, 0, len(ids))

	for _, id := range ids {
		row, err := s.loadCredential(ctx, credential.IDEQ(id))
		if err != nil {
			return nil, err
		}
		ct := row.Edges.CredentialType
		org := row.Edges.Organization
		if ct == nil || org == nil {
			return nil, fmt.Errorf("credstore: credential %d was read without its type or organization", id)
		}
		if org.ID != organizationID {
			return nil, fmt.Errorf(
				"%w: the credential %q belongs to another organization", ErrCrossOrganization, row.Name)
		}

		kind := credtype.Kind(ct.Kind)
		out = append(out, credtype.Bound{
			CredentialID:   id,
			CredentialName: row.Name,
			Kind:           kind,
			// Read from the REAL values, which is why this happens in the
			// store rather than at the handler: the handler only ever
			// holds the redacted projection, and vault_id may itself be a
			// secret field.
			VaultIdentifier: credtype.VaultIdentifierOf(kind, row.Inputs),
		})
	}
	return out, nil
}

// loadCredential fetches one credential with the edges every projection
// needs.
func (s *entStore) loadCredential(ctx context.Context, where predicate.Credential) (*ent.Credential, error) {
	row, err := s.client.Credential.Query().
		Where(where).
		WithCredentialType().
		WithOrganization().
		WithTemplates().
		Only(ctx)
	if err != nil {
		return nil, wrapNotFound(err, "credential")
	}
	return row, nil
}

// project converts a stored row into the redacted read projection.
//
// Every read path in this package ends here, which is what makes the
// redaction unmissable rather than a step each method has to remember.
func (s *entStore) project(ctx context.Context, row *ent.Credential) (Credential, error) {
	ct := row.Edges.CredentialType
	org := row.Edges.Organization
	if ct == nil || org == nil {
		return Credential{}, fmt.Errorf("credstore: credential %d was read without its type or organization", row.ID)
	}

	templateIDs := make([]int, 0, len(row.Edges.Templates))
	for _, t := range row.Edges.Templates {
		templateIDs = append(templateIDs, t.ID)
	}

	return Credential{
		ID:               row.ID,
		Name:             row.Name,
		Description:      row.Description,
		TypeID:           ct.ID,
		TypeName:         ct.Name,
		TypeNamespace:    ct.Namespace,
		Kind:             credtype.Kind(ct.Kind),
		OrganizationID:   org.ID,
		OrganizationName: org.Name,
		Inputs:           redactInputs(ct.Inputs, row.Inputs),
		External:         cloneStrings(row.External),
		TemplateIDs:      templateIDs,
	}, nil
}

// mergeInputs applies an update's values over the stored ones, treating the
// redaction marker as "leave this alone".
//
// See UpdateCredential for why. An input absent from the update is also
// left alone, so a partial update is a partial update rather than a silent
// deletion of everything it did not mention.
func mergeInputs(stored, supplied map[string]string) map[string]string {
	merged := cloneStrings(stored)
	if merged == nil {
		merged = make(map[string]string)
	}
	for id, value := range supplied {
		if value == redact.Marker {
			continue
		}
		merged[id] = value
	}
	return merged
}

// cloneStrings copies a map so a caller cannot mutate what the store holds.
func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
