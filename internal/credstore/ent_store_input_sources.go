package credstore

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credentialinputsource"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage"
)

// The input-source half of the ent store: which credential supplies a given
// credential's input when this platform stores no value for it.
//
// Split from ent_store_credentials.go for the ~300 line file convention,
// not because it is a different concern.
//
// # Why so much of this file is refusal
//
// Architecture Principle 5 says type safety moves left: catch it at write
// time, never at task 47 of 200. A bad binding here is exactly that failure
// shape, because nothing reads these rows until a job dispatches, which may
// be days later and will be somebody else's problem. So an input id the
// type never declared, a source in another tenant, a source that is not an
// source that cannot supply the value it promises, and a reference cycle are all refused by the
// write that creates them rather than by the dispatch that trips over them.
//
// The cycle check is the one worth reading twice, because it is duplicated
// on purpose. internal/credstore/resolve bounds the walk again at
// resolution, and that is not redundancy: this check sees one writer's
// proposed graph at one instant, and two concurrent writers can each write
// an acyclic change that is cyclic together. The resolve-time bound is the
// control that holds regardless, and this one exists so the ordinary case
// is a 409 to the person who caused it instead of a failed job for somebody
// else.

// ListCredentialInputSources returns one credential's input bindings,
// ordered by input id so a caller rendering them gets a stable list.
func (s *entStore) ListCredentialInputSources(ctx context.Context, credentialID int) ([]InputSource, error) {
	rows, err := s.db(ctx).CredentialInputSource.Query().
		Where(credentialinputsource.HasTargetCredentialWith(credential.IDEQ(credentialID))).
		WithSourceCredential(func(q *ent.CredentialQuery) { q.WithCredentialType() }).
		Order(ent.Asc(credentialinputsource.FieldInputID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("credstore: listing input sources for credential %d: %w", credentialID, err)
	}

	out := make([]InputSource, 0, len(rows))
	for _, row := range rows {
		source := row.Edges.SourceCredential
		if source == nil {
			return nil, fmt.Errorf("credstore: input source %d was read without its source credential", row.ID)
		}
		projected := InputSource{
			ID:                   row.ID,
			InputID:              row.InputID,
			SourceCredentialID:   source.ID,
			SourceCredentialName: source.Name,
			Metadata:             row.Metadata,
		}
		if ct := source.Edges.CredentialType; ct != nil {
			projected.SourceCredentialNamespace = ct.Namespace
		}
		out = append(out, projected)
	}
	return out, nil
}

// SetCredentialInputSources replaces a credential's input bindings with the
// given set, refusing the whole request if any one binding is bad.
//
// Replace rather than merge, matching SetTemplateCredentials: a caller
// sending the list it wants cannot be surprised by a binding it forgot was
// there, which is the failure a merge semantics produces.
//
// All or nothing, inside a transaction. A partial application would leave a
// credential reading some inputs from a source and some from nowhere, and
// the second kind fails at injection rather than here.
func (s *entStore) SetCredentialInputSources(
	ctx context.Context,
	credentialID int,
	bindings []InputSourceBinding,
) ([]InputSource, error) {
	target, err := s.loadCredential(ctx, credential.IDEQ(credentialID))
	if err != nil {
		return nil, err
	}
	ct := target.Edges.CredentialType
	org := target.Edges.Organization
	if ct == nil || org == nil {
		return nil, fmt.Errorf("credstore: credential %d was read without its type or organization", credentialID)
	}

	if err := storage.NewEntUnitOfWork(s.client).WithTx(ctx, func(txCtx context.Context) error {
		return s.writeInputSources(txCtx, credentialID, ct.Inputs, org.ID, bindings)
	}); err != nil {
		return nil, err
	}

	return s.ListCredentialInputSources(ctx, credentialID)
}

// writeInputSources checks and then replaces one credential's bindings,
// using whatever client is in scope.
//
// It is the shared half of SetCredentialInputSources and of the two
// credential writers' WithInputSources option, so the refusals cannot
// differ by which door a binding came through. It does NOT open a
// transaction of its own: every caller is already inside one, which is what
// makes "the credential and its bindings are one write" true.
func (s *entStore) writeInputSources(
	ctx context.Context,
	credentialID int,
	schema credtype.InputSchema,
	organizationID int,
	bindings []InputSourceBinding,
) error {
	if err := s.checkBindings(ctx, credentialID, schema, organizationID, bindings); err != nil {
		return err
	}

	db := s.db(ctx)
	if _, err := db.CredentialInputSource.Delete().
		Where(credentialinputsource.HasTargetCredentialWith(credential.IDEQ(credentialID))).
		Exec(ctx); err != nil {
		return fmt.Errorf("credstore: clearing input sources for credential %d: %w", credentialID, err)
	}

	for _, b := range bindings {
		if err := db.CredentialInputSource.Create().
			SetInputID(b.InputID).
			SetMetadata(b.Metadata).
			SetTargetCredentialID(credentialID).
			SetSourceCredentialID(b.SourceCredentialID).
			Exec(ctx); err != nil {
			// Names the input rather than the source's values: this error
			// reaches an API response.
			return fmt.Errorf("credstore: binding input %q of credential %d: %w",
				b.InputID, credentialID, err)
		}
	}
	return nil
}

// db returns the client this call must use: the transaction's own client
// when one is in scope, and the top-level client otherwise.
//
// Without it a check running inside a transaction would query the
// top-level client and miss the rows that transaction had just written,
// which for the cycle walk means missing exactly the edge being added.
func (s *entStore) db(ctx context.Context) *ent.Client {
	if tx := ent.FromContext(ctx); tx != nil {
		return tx
	}
	return s.client
}

// checkBindings refuses every binding this store can tell is wrong before
// any of them is written.
func (s *entStore) checkBindings(
	ctx context.Context,
	targetID int,
	schema credtype.InputSchema,
	organizationID int,
	bindings []InputSourceBinding,
) error {
	seen := make(map[string]struct{}, len(bindings))
	for _, b := range bindings {
		if _, dup := seen[b.InputID]; dup {
			return fmt.Errorf("%w: input %q is bound to a source twice in one request", ErrExists, b.InputID)
		}
		seen[b.InputID] = struct{}{}

		// An input the type does not declare would be written, stored, and
		// then silently ignored at injection, because the injector reads
		// the type's own field list rather than whatever keys happen to be
		// present.
		if _, ok := schema.Field(b.InputID); !ok {
			return fmt.Errorf("%w: this credential's type declares no input %q, so binding a source to it would do nothing",
				ErrNotFound, b.InputID)
		}

		if b.SourceCredentialID == targetID {
			return fmt.Errorf("%w: credential %d cannot be its own source", credtype.ErrLookupCycle, targetID)
		}

		source, err := s.db(ctx).Credential.Query().
			Where(credential.IDEQ(b.SourceCredentialID)).
			WithCredentialType().
			WithOrganization().
			Only(ctx)
		if err != nil {
			return wrapNotFound(err, "credential")
		}
		sourceType := source.Edges.CredentialType
		sourceOrg := source.Edges.Organization
		if sourceType == nil || sourceOrg == nil {
			return fmt.Errorf("credstore: credential %d was read without its type or organization", b.SourceCredentialID)
		}
		if sourceOrg.ID != organizationID {
			// The same refusal boundFor makes, for the same reason: a
			// credential reading its values through another tenant's Vault
			// authenticates as somebody else.
			return fmt.Errorf("%w: the source credential %q belongs to another organization",
				ErrCrossOrganization, source.Name)
		}
		if err := checkSourceField(b, source.Name, sourceType, schema); err != nil {
			return err
		}
	}

	return s.checkNoCycle(ctx, targetID, bindings)
}

// checkSourceField refuses a binding whose source cannot supply the value
// it promises, at the moment somebody writes it.
//
// Until Phase 78d this was a flat refusal of any source that was not an
// external-kind credential, which made PLAN.md Section 17.4's "link a
// standard Password credential to it" impossible: naming a Password
// credential as a source failed here, before the resolver was ever
// reached. The two source shapes are now separated instead.
//
// An EXTERNAL source addresses a secret with its own vocabulary, decoded
// by that source's own LookupFactory, so there is nothing general to check
// here and the metadata is left to the factory at dispatch.
//
// Any OTHER kind is an ordinary credential whose field is read directly.
// That has exactly two ways to be wrong and both are knowable now: the
// binding names no field, or it names one the source's type does not
// declare. Checking them at write time is the argument the cycle check
// already makes for itself. Nothing reads these rows until a job
// dispatches, so a binding found broken then is found during a run,
// against a device, by whoever launched it rather than by whoever wrote it.
func checkSourceField(
	b InputSourceBinding,
	sourceName string,
	sourceType *ent.CredentialType,
	targetSchema credtype.InputSchema,
) error {
	if credtype.Kind(sourceType.Kind) == credtype.KindExternal {
		return nil
	}

	field := b.Metadata[credtype.SourceFieldMetadataKey]
	if field == "" {
		return fmt.Errorf(
			"%w: credential %q is an ordinary credential rather than a secret source, so binding input %q to it has "+
				"to name which of its fields to read, in the %q metadata key",
			ErrNotFound, sourceName, b.InputID, credtype.SourceFieldMetadataKey)
	}
	sourceField, declared := sourceType.Inputs.Field(field)
	if !declared {
		return fmt.Errorf(
			"%w: input %q would read field %q of credential %q, whose type declares no such input",
			ErrNotFound, b.InputID, field, sourceName)
	}

	// A secret may only be read into an input that is itself secret. The
	// resolver refuses this too, and the duplication is deliberate for the
	// reason the cycle check gives for its own: this one catches the writer,
	// and that one catches a row written behind the store.
	//
	// The rule exists because the masking ruleset is built from the TARGET
	// type's secret fields, so a secret landing in an input nothing marks
	// secret is a secret nothing masks.
	if targetField, ok := targetSchema.Field(b.InputID); sourceField.Secret && (!ok || !targetField.Secret) {
		return fmt.Errorf(
			"%w: field %q of credential %q is secret and input %q is not, so reading it would move a secret into "+
				"a value nothing masks: declare the input secret, or bind a field that is not",
			ErrInUse, field, sourceName, b.InputID)
	}
	return nil
}

// checkNoCycle refuses a proposed set of bindings that would let resolution
// return to the credential it started from.
//
// It walks the STORED graph from each proposed source, treating the target
// as if the proposal were already applied. Reaching the target means the
// proposal closes a loop.
func (s *entStore) checkNoCycle(ctx context.Context, targetID int, bindings []InputSourceBinding) error {
	// Visited is keyed on credential id rather than on binding, because
	// two inputs bound to the same source are one edge for this purpose and
	// walking it twice would only be slower.
	visited := make(map[int]struct{})
	queue := make([]int, 0, len(bindings))
	for _, b := range bindings {
		queue = append(queue, b.SourceCredentialID)
	}

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]

		if id == targetID {
			return fmt.Errorf("%w: this binding would make credential %d resolve through itself",
				credtype.ErrLookupCycle, targetID)
		}
		if _, done := visited[id]; done {
			continue
		}
		visited[id] = struct{}{}

		rows, err := s.db(ctx).CredentialInputSource.Query().
			Where(credentialinputsource.HasTargetCredentialWith(credential.IDEQ(id))).
			WithSourceCredential().
			All(ctx)
		if err != nil {
			return fmt.Errorf("credstore: walking input sources of credential %d: %w", id, err)
		}
		for _, row := range rows {
			if row.Edges.SourceCredential != nil {
				queue = append(queue, row.Edges.SourceCredential.ID)
			}
		}
	}
	return nil
}
