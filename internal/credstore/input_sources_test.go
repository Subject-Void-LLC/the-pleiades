package credstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// The write-time half of the input-source rules.
//
// Every test here asserts a REFUSAL, and that is what the file is for.
// Nothing reads a binding until a job dispatches, so a binding this store
// accepts and should not have is discovered days later by somebody who did
// not write it. Architecture Principle 5's "catch it at write time" is the
// whole design, and these are the cases it has to catch.

// sourceFixture adds an external-kind credential type and one credential of
// it to the shared fixture, which is what every binding needs on the other
// end.
func sourceFixture(t *testing.T) (credstore.Store, *ent.Client, int, int, int) {
	t.Helper()

	ctx := context.Background()
	store, client, orgID, targetTypeID := fixture(t)

	sourceType, err := store.CreateType(ctx, orgID, credtype.CredentialType{
		Name:      "Test Vault",
		Kind:      credtype.KindExternal,
		Namespace: "test_vault",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "token", Label: "Token", Secret: true}},
			Required: []string{"token"},
		},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}
	source, err := store.CreateCredential(ctx, orgID, sourceType.ID, "vault", "",
		map[string]string{"token": "s.t"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() for the source error = %v", err)
	}
	return store, client, orgID, targetTypeID, source.ID
}

// bind is the binding every test below varies one field of.
func bind(inputID string, sourceID int) []credstore.InputSourceBinding {
	return []credstore.InputSourceBinding{{
		InputID:            inputID,
		SourceCredentialID: sourceID,
		Metadata:           map[string]string{"path": "secret/data/p"},
	}}
}

// TestABindingRoundTripsThroughTheStore is the positive control. Every
// refusal below is only meaningful if the legal case is accepted.
func TestABindingRoundTripsThroughTheStore(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources(bind("api_token", sourceID)))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := store.ListCredentialInputSources(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListCredentialInputSources() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListCredentialInputSources() returned %d bindings, want one", len(got))
	}
	if got[0].InputID != "api_token" || got[0].SourceCredentialID != sourceID {
		t.Errorf("binding = %+v, want api_token bound to the source", got[0])
	}
	// The source's name and namespace are carried so a reader does not
	// need a second request, matching Credential's own convention.
	if got[0].SourceCredentialName != "vault" || got[0].SourceCredentialNamespace != "test_vault" {
		t.Errorf("binding = %+v, want the source's name and namespace carried", got[0])
	}
	if got[0].Metadata["path"] != "secret/data/p" {
		t.Errorf("metadata = %v, want the path preserved", got[0].Metadata)
	}
}

// TestAnEmptySetUnbindsEverything covers the replace semantics, which is
// the half a merge would get wrong.
func TestAnEmptySetUnbindsEverything(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "",
		map[string]string{"api_token": "stored"}, nil,
		credstore.WithInputSources(bind("api_token", sourceID)))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	got, err := store.SetCredentialInputSources(ctx, target.ID, nil)
	if err != nil {
		t.Fatalf("SetCredentialInputSources() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("SetCredentialInputSources(nil) left %d bindings, want none", len(got))
	}
}

// TestTheStoreRefusesEveryBindingItCanTell is the table of refusals.
func TestTheStoreRefusesEveryBindingItCanTell(t *testing.T) {
	tests := []struct {
		name string
		// build returns the bindings to attempt, given the fixture's ids.
		build func(t *testing.T, store credstore.Store, orgID, targetTypeID, sourceID int) []credstore.InputSourceBinding
		want  error
	}{
		{
			name: "an input the type does not declare",
			build: func(*testing.T, credstore.Store, int, int, int) []credstore.InputSourceBinding {
				return bind("not_an_input", 1)
			},
			want: credstore.ErrNotFound,
		},
		{
			name: "the same input bound twice in one request",
			build: func(_ *testing.T, _ credstore.Store, _, _, sourceID int) []credstore.InputSourceBinding {
				return append(bind("api_token", sourceID), bind("api_token", sourceID)...)
			},
			want: credstore.ErrExists,
		},
		{
			name: "a source that does not exist",
			build: func(*testing.T, credstore.Store, int, int, int) []credstore.InputSourceBinding {
				return bind("api_token", 999999)
			},
			want: credstore.ErrNotFound,
		},
		{
			name: "a source that is not an external-kind credential",
			build: func(t *testing.T, store credstore.Store, orgID, targetTypeID, _ int) []credstore.InputSourceBinding {
				// A credential of the fixture's own cloud-kind type:
				// perfectly valid, and simply not a thing that can supply
				// another credential's input.
				other, err := store.CreateCredential(context.Background(), orgID, targetTypeID, "not a vault", "",
					map[string]string{"api_token": "x"}, nil)
				if err != nil {
					t.Fatalf("CreateCredential() error = %v", err)
				}
				return bind("api_token", other.ID)
			},
			want: credstore.ErrInUse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

			target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "",
				map[string]string{"api_token": "stored"}, nil)
			if err != nil {
				t.Fatalf("CreateCredential() error = %v", err)
			}

			_, err = store.SetCredentialInputSources(ctx, target.ID,
				tt.build(t, store, orgID, targetTypeID, sourceID))
			if !errors.Is(err, tt.want) {
				t.Fatalf("SetCredentialInputSources() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestASourceInAnotherOrganizationIsRefused is the tenancy boundary, kept
// out of the table above because it needs a second organization.
//
// The failure it prevents is the one that matters most in this file: a
// credential reading its values through another tenant's secret manager
// dispatches jobs authenticating as somebody else.
func TestASourceInAnotherOrganizationIsRefused(t *testing.T) {
	ctx := context.Background()
	store, client, orgID, targetTypeID, _ := sourceFixture(t)

	otherOrg := client.Organization.Create().SetName("other").SaveX(ctx)
	otherType, err := store.CreateType(ctx, otherOrg.ID, credtype.CredentialType{
		Name:      "Their Vault",
		Kind:      credtype.KindExternal,
		Namespace: "their_vault",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: "token", Label: "Token", Secret: true}},
			Required: []string{"token"},
		},
	})
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}
	theirs, err := store.CreateCredential(ctx, otherOrg.ID, otherType.ID, "their vault", "",
		map[string]string{"token": "s.them"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "",
		map[string]string{"api_token": "stored"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	_, err = store.SetCredentialInputSources(ctx, target.ID, bind("api_token", theirs.ID))
	if !errors.Is(err, credstore.ErrCrossOrganization) {
		t.Fatalf("SetCredentialInputSources() error = %v, want a cross-organization refusal", err)
	}
}

// TestARefusedSetLeavesTheStoredBindingsAlone is what the transaction is
// for. A partial application would leave a credential reading some inputs
// from a source and some from nowhere, and the second kind fails at
// injection rather than here.
func TestARefusedSetLeavesTheStoredBindingsAlone(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources(bind("api_token", sourceID)))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// A set whose first binding is legal and whose second is not.
	bad := append(bind("api_token", sourceID), credstore.InputSourceBinding{
		InputID:            "not_an_input",
		SourceCredentialID: sourceID,
	})
	if _, err := store.SetCredentialInputSources(ctx, target.ID, bad); err == nil {
		t.Fatal("SetCredentialInputSources() accepted a set naming an undeclared input")
	}

	got, err := store.ListCredentialInputSources(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListCredentialInputSources() error = %v", err)
	}
	if len(got) != 1 || got[0].InputID != "api_token" {
		t.Fatalf("after a refused set the bindings are %+v, want the original one intact", got)
	}
}

// TestUpdatingAnUnrelatedFieldKeepsTheBindings covers the reason
// UpdateCredential reads the stored bindings back when the option is not
// passed: without it, renaming a credential would fail the required-input
// check on an input it is not touching.
func TestUpdatingAnUnrelatedFieldKeepsTheBindings(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources(bind("api_token", sourceID)))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	if _, err := store.UpdateCredential(ctx, target.ID, "renamed", "", nil, nil); err != nil {
		t.Fatalf("UpdateCredential() error = %v, want a rename to succeed with the binding standing", err)
	}

	got, err := store.ListCredentialInputSources(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListCredentialInputSources() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after an unrelated update the bindings are %+v, want the original one intact", got)
	}
}

// TestUpdateCanReplaceTheBindings is the other half: passing the option
// does replace them, in the same write as the field change.
func TestUpdateCanReplaceTheBindings(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "",
		map[string]string{"api_token": "stored"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	if _, err := store.UpdateCredential(ctx, target.ID, "api", "", nil, nil,
		credstore.WithInputSources(bind("api_token", sourceID))); err != nil {
		t.Fatalf("UpdateCredential() error = %v", err)
	}

	got, err := store.ListCredentialInputSources(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListCredentialInputSources() error = %v", err)
	}
	if len(got) != 1 || got[0].SourceCredentialID != sourceID {
		t.Fatalf("bindings = %+v, want the one the update supplied", got)
	}
}

// TestDeletingASourceUnbindsRatherThanRefusing pins the cascade decision,
// which is deliberate and is the opposite of what DeleteType does.
//
// Credential.sourced_by carries the reasoning: a credential holds a secret,
// and a compromised secret must stay deletable during an incident rather
// than waiting on every credential that reads through it being edited
// first. What makes that safe is that the target then fails LOUDLY at
// injection, which credtype's own required-input check provides.
func TestDeletingASourceUnbindsRatherThanRefusing(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sourceID := sourceFixture(t)

	target, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources(bind("api_token", sourceID)))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	if err := store.DeleteCredential(ctx, sourceID); err != nil {
		t.Fatalf("DeleteCredential() error = %v, want a compromised source to be deletable", err)
	}

	got, err := store.ListCredentialInputSources(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListCredentialInputSources() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after deleting the source the bindings are %+v, want none", got)
	}
}

// TestBindingOnACredentialThatDoesNotExistIsNotFound covers the ordinary
// mistake of a stale id, which must be a 404 rather than a server error.
func TestBindingOnACredentialThatDoesNotExistIsNotFound(t *testing.T) {
	ctx := context.Background()
	store, _, _, _, sourceID := sourceFixture(t)

	if _, err := store.SetCredentialInputSources(ctx, 999999, bind("api_token", sourceID)); !errors.Is(err, credstore.ErrNotFound) {
		t.Fatalf("SetCredentialInputSources() error = %v, want a not-found refusal", err)
	}
}

// TestAnIndirectCycleIsRefused covers the WALK rather than the one-hop
// check: a to b to c and back to a is only found by following edges the
// proposed binding does not itself name.
func TestAnIndirectCycleIsRefused(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, _, _ := sourceFixture(t)

	third, err := store.CreateCredential(ctx, orgID, vaultTypeID(t, store, orgID), "vault c", "",
		map[string]string{"token": "s.c"}, nil)
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	second, err := store.CreateCredential(ctx, orgID, vaultTypeID(t, store, orgID), "vault b", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: third.ID, Metadata: map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	first, err := store.CreateCredential(ctx, orgID, vaultTypeID(t, store, orgID), "vault a", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: second.ID, Metadata: map[string]string{"path": "p"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	// Pointing c at a closes a three-link loop. The proposed binding names
	// only a, so the cycle is found by walking a to b to c.
	_, err = store.SetCredentialInputSources(ctx, third.ID, []credstore.InputSourceBinding{{
		InputID: "token", SourceCredentialID: first.ID, Metadata: map[string]string{"path": "p"},
	}})
	if !errors.Is(err, credtype.ErrLookupCycle) {
		t.Fatalf("SetCredentialInputSources() error = %v, want a cycle refusal", err)
	}
}

// TestADiamondIsAccepted is the negative control for the walk above. A walk
// that did not remember where it had been would either refuse this or
// revisit the shared node forever.
func TestADiamondIsAccepted(t *testing.T) {
	ctx := context.Background()
	store, _, orgID, targetTypeID, sharedID := sourceFixture(t)

	left, err := store.CreateCredential(ctx, orgID, vaultTypeID(t, store, orgID), "left", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: sharedID, Metadata: map[string]string{"path": "l"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	right, err := store.CreateCredential(ctx, orgID, vaultTypeID(t, store, orgID), "right", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{{
			InputID: "token", SourceCredentialID: sharedID, Metadata: map[string]string{"path": "r"},
		}}))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}

	if _, err := store.CreateCredential(ctx, orgID, targetTypeID, "api", "", nil, nil,
		credstore.WithInputSources([]credstore.InputSourceBinding{
			{InputID: "api_token", SourceCredentialID: left.ID, Metadata: map[string]string{"path": "p"}},
			{InputID: "api_url", SourceCredentialID: right.ID, Metadata: map[string]string{"path": "p"}},
		})); err != nil {
		t.Fatalf("a diamond must be accepted, got error = %v", err)
	}
}

// vaultTypeID returns the external-kind type sourceFixture installed.
func vaultTypeID(t *testing.T, store credstore.Store, orgID int) int {
	t.Helper()
	ct, err := store.GetTypeByNamespace(context.Background(), "test_vault")
	if err != nil {
		t.Fatalf("GetTypeByNamespace() error = %v", err)
	}
	return ct.ID
}
