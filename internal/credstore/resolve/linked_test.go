// Package resolve_test's linked-credential half: an input filled from
// another credential's own field rather than out of a secret manager.
//
// These tests are separate from graph_test.go because they exercise the
// branch Phase 78d added rather than the external-source walk Phase 78a
// built, and because the fixture they need is a non-external credential
// type, which that file has no other use for.
package resolve_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// passwordType is an ordinary, non-external credential type: a plain
// password an operator stores once and points other credentials at.
//
// This is the shape PLAN.md Section 17.4 means by "link a standard Password
// credential to it", and until Phase 78d naming one as a source was refused
// outright.
func passwordType(t testing.TB, store credstore.Store, orgID int) int {
	t.Helper()

	created, err := store.CreateType(context.Background(), orgID, credtype.CredentialType{
		Name:      "Password",
		Kind:      credtype.KindSSH,
		Namespace: "linked_password",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "password", Label: "Password", Secret: true},
			{ID: "username", Label: "Username"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateType() for the password type error = %v", err)
	}
	return created.ID
}

// TestAnInputResolvesFromALinkedCredentialsField is the base case for the
// form 78a did not build: a value read from another credential's own row,
// with no network anywhere in the path.
func TestAnInputResolvesFromALinkedCredentialsField(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, _ := graphFixture(t)

	sourceID := passwordType(t, store, orgID)
	source, err := store.CreateCredential(ctx, orgID, sourceID, "shared password", "",
		map[string]string{"username": "operator", "password": "a-real-passphrase"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "password"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := "a-real-passphrase"; got[0].Inputs["api_token"] != want {
		t.Errorf("the bound input = %q, want %q", got[0].Inputs["api_token"], want)
	}

	// The field named is the field read. Binding the same source's other
	// field must produce the other value, or this test would pass against
	// an implementation that returned whatever it found first.
	other, err := store.CreateCredential(ctx, orgID, targetTypeID, "other api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "username"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the second target error = %v", err)
	}
	gotOther, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{other.ID})
	if err != nil {
		t.Fatalf("Resolve() for the second target error = %v", err)
	}
	if want := "operator"; gotOther[0].Inputs["api_token"] != want {
		t.Errorf("the second bound input = %q, want %q", gotOther[0].Inputs["api_token"], want)
	}
}

// TestALinkedCredentialResolvesWithNoLookupsConfigured is the claim that
// this form needs no secret source at all, made falsifiable.
//
// Before Phase 78d the resolver refused every binding up front when no
// lookups were configured, because every binding meant an external source.
// A linked credential has no factory, no namespace to register and nothing
// to dial, so a controller with no secret manager must still resolve one.
func TestALinkedCredentialResolvesWithNoLookupsConfigured(t *testing.T) {
	ctx := context.Background()
	store, client, _, orgID, targetTypeID, _ := graphFixture(t)

	sourceID := passwordType(t, store, orgID)
	source, err := store.CreateCredential(ctx, orgID, sourceID, "shared password", "",
		map[string]string{"password": "a-real-passphrase"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "password"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	// No WithLookups at all.
	got, err := resolve.NewEntResolver(client).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if want := "a-real-passphrase"; got[0].Inputs["api_token"] != want {
		t.Errorf("the bound input = %q, want %q", got[0].Inputs["api_token"], want)
	}
}

// TestALinkedCredentialMayItselfBeVaultBacked is the test of whether this
// is the right seam.
//
// The branch that reads a field off a linked credential knows nothing about
// Vault, and the branch that reads out of Vault knows nothing about linked
// credentials. They compose anyway, because the source is fully resolved
// before either runs. If this needed a special case, the seam would be in
// the wrong place.
func TestALinkedCredentialMayItselfBeVaultBacked(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, vaultTypeID := graphFixture(t)

	vault, err := store.CreateCredential(ctx, orgID, vaultTypeID, "prod vault", "",
		map[string]string{"token": "s.roottoken"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the vault error = %v", err)
	}

	// The shared password stores no password of its own: it reads one out
	// of Vault.
	sourceID := passwordType(t, store, orgID)
	source, err := store.CreateCredential(ctx, orgID, sourceID, "shared password", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "password",
			SourceCredentialID: vault.ID,
			Metadata:           map[string]string{"path": "secret/data/shared"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "password"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// The stub source concatenates its own token with the reference, so one
	// assertion carries the whole chain: the vault read happened, and the
	// field read off the linked credential returned its result.
	if want := "s.roottoken/secret/data/shared"; got[0].Inputs["api_token"] != want {
		t.Errorf("the bound input = %q, want %q", got[0].Inputs["api_token"], want)
	}
}

// TestALinkedFieldThatCannotSupplyAValueIsRefusedByName covers what the
// store cannot catch at write time: a field that is declared, passes every
// write check, and is empty when the job actually runs.
func TestALinkedFieldThatCannotSupplyAValueIsRefusedByName(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, _ := graphFixture(t)

	sourceID := passwordType(t, store, orgID)
	source, err := store.CreateCredential(ctx, orgID, sourceID, "shared password", "",
		map[string]string{"username": "operator"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "password"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}

	_, err = resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err == nil {
		t.Fatal("an empty linked field resolved to something")
	}
	if !errors.Is(err, credtype.ErrLookupReference) {
		t.Errorf("error = %v, want it to wrap ErrLookupReference", err)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %v, want it to say the field is empty", err)
	}
}

// TestALinkedBindingTheStoreWouldHaveRefusedIsRefusedAtResolveToo is the
// resolve-time half of the write-time check, and it is not redundant with
// it for the reason the cycle pair is not redundant either.
//
// The store refuses a binding that names no field, or names one the
// source's type does not declare. Both refusals see one writer's proposal
// at one instant, and neither covers a row written by direct SQL, by an
// older release, or by a type that was edited after the binding was
// written. That last one is not hypothetical: a credential type's input
// list is data an administrator can change, so a binding that was legal
// when written can stop naming a declared field without anything touching
// the binding.
//
// So these rows are written through the ent client rather than the store,
// which is exactly the situation the resolve-time refusal exists for.
func TestALinkedBindingTheStoreWouldHaveRefusedIsRefusedAtResolveToo(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metadata map[string]string
		want     string
	}{
		{
			name:     "no field named at all",
			metadata: map[string]string{},
			want:     "has to name which of its fields to read",
		},
		{
			name:     "a field the source's type does not declare",
			metadata: map[string]string{credtype.SourceFieldMetadataKey: "no_such_field"},
			want:     "declares no such input",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, client, lookups, orgID, targetTypeID, _ := graphFixture(t)

			sourceID := passwordType(t, store, orgID)
			source, err := store.CreateCredential(ctx, orgID, sourceID, "shared password", "",
				map[string]string{"password": "a-real-passphrase"}, nil)
			if err != nil {
				t.Fatalf("CreateCredential() for the source error = %v", err)
			}
			// api_token has a stored value, so the target is legal without
			// the binding and the binding can be added behind the store.
			target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "",
				map[string]string{"api_token": "stored"}, nil)
			if err != nil {
				t.Fatalf("CreateCredential() for the target error = %v", err)
			}

			if err := client.CredentialInputSource.Create().
				SetInputID("api_token").
				SetMetadata(tt.metadata).
				SetTargetCredentialID(target.ID).
				SetSourceCredentialID(source.ID).
				Exec(ctx); err != nil {
				t.Fatalf("writing a binding directly: %v", err)
			}

			_, err = resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
			if err == nil {
				t.Fatal("an unresolvable binding resolved")
			}
			if !errors.Is(err, credtype.ErrLookupReference) {
				t.Errorf("error = %v, want it to wrap ErrLookupReference", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestASecretCannotBeLaunderedIntoANonSecretInput is the containment rule,
// and it closes a real escalation rather than tidying an edge case.
//
// The masking ruleset is built from the TARGET credential type's own secret
// fields, so a value read out of a secret field and written into an input
// its type does not mark secret is never registered with the redactor. It
// renders into an extra var, an environment variable or a generated file in
// cleartext and appears unmasked in captured output, while the credential it
// came from stays masked everywhere else. Anyone able to write a credential
// type could otherwise read any same-organization secret out through a type
// they control.
//
// The old blanket "external-kind sources only" refusal was providing this
// containment by accident. Removing it had to put something deliberate in
// its place.
func TestASecretCannotBeLaunderedIntoANonSecretInput(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, _, _ := graphFixture(t)

	// A type whose one input is NOT secret, which is the whole trick.
	lootType, err := store.CreateType(ctx, orgID, credtype.CredentialType{
		Name: "Loot", Kind: credtype.KindCloud, Namespace: "loot_type",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "loot", Label: "Loot", Secret: false},
		}},
		Injectors: credtype.Injectors{ExtraVars: map[string]any{"loot": "{{ loot }}"}},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}

	sourceTypeID := passwordType(t, store, orgID)
	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "production machine", "",
		map[string]string{"password": "the-production-password"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}

	binding := []credstore.InputSourceBinding{{
		InputID:            "loot",
		SourceCredentialID: source.ID,
		Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "password"},
	}}

	// Refused at the write, which is where whoever wrote it can act on it.
	_, err = store.CreateCredential(ctx, orgID, lootType.ID, "loot", "", nil, nil,
		credstore.WithInputSources(binding))
	if err == nil {
		t.Fatal("a secret was bound into a non-secret input")
	}
	if !strings.Contains(err.Error(), "nothing masks") {
		t.Errorf("error = %v, want it to say the value would be unmasked", err)
	}

	// And refused again at resolve, for a row written behind the store. The
	// duplication is the same argument the cycle check makes for itself.
	target, err := store.CreateCredential(ctx, orgID, lootType.ID, "loot", "",
		map[string]string{"loot": "placeholder"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the target error = %v", err)
	}
	if err := client.CredentialInputSource.Create().
		SetInputID("loot").
		SetMetadata(map[string]string{credtype.SourceFieldMetadataKey: "password"}).
		SetTargetCredentialID(target.ID).
		SetSourceCredentialID(source.ID).
		Exec(ctx); err != nil {
		t.Fatalf("writing a binding directly: %v", err)
	}

	resolved, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err == nil {
		t.Fatalf("the resolver laundered a secret into a non-secret input: %v", resolved[0].Inputs["loot"])
	}
	if !errors.Is(err, credtype.ErrLookupReference) {
		t.Errorf("error = %v, want ErrLookupReference", err)
	}
	if strings.Contains(err.Error(), "the-production-password") {
		t.Error("the refusal leaked the secret it was refusing to leak")
	}
}

// TestASecretIntoASecretInputIsStillAllowed is the negative control. Without
// it the rule above would pass against an implementation that refused every
// linked binding, which would break the feature this stage exists to add.
func TestASecretIntoASecretInputIsStillAllowed(t *testing.T) {
	ctx := context.Background()
	store, client, lookups, orgID, targetTypeID, _ := graphFixture(t)

	sourceTypeID := passwordType(t, store, orgID)
	source, err := store.CreateCredential(ctx, orgID, sourceTypeID, "shared password", "",
		map[string]string{"password": "a-real-passphrase"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}
	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "prod api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID:            "api_token",
			SourceCredentialID: source.ID,
			Metadata:           map[string]string{credtype.SourceFieldMetadataKey: "password"},
		}}))
	if err != nil {
		t.Fatalf("a secret bound into a secret input was refused: %v", err)
	}

	got, err := resolve.NewEntResolver(client, resolve.WithLookups(lookups)).Resolve(ctx, []int{target.ID})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got[0].Inputs["api_token"] != "a-real-passphrase" {
		t.Errorf("the bound input = %q, want the source's password", got[0].Inputs["api_token"])
	}
}
