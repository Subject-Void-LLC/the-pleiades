// This file holds the binding rule's refusals to never quoting a secret: the
// rule tells two vault credentials apart by their vault identifier and two
// database connections by their name, and its refusal names the one they
// share, so neither may be read out of an input the type marks secret.
package credstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// sharedIdentifier is the secret two credentials of each type below hold
// for the input their kind reads as an identifier.
const sharedIdentifier = "sk-live-IDENTIFIER-CANARY-77aa66bb"

// identifierType is a custom type of kind whose input named identifier is
// secret.
func identifierType(namespace string, kind credtype.Kind, identifier string) credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Identified " + namespace,
		Kind:      kind,
		Namespace: namespace,
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "api_token", Label: "Token", Secret: true},
			{ID: identifier, Label: "Identifier", Secret: true},
		}},
	}
}

// TestNoBindingRefusalQuotesASecretInput tries each way a type could make
// the binding rule read its identifier out of a secret input: created with
// the vault or database kind, or changed to the vault kind later. Each is
// refused at the type's write; were one accepted, the test binds two of its
// credentials and fails on the refusal quoting the secret.
func TestNoBindingRefusalQuotesASecretInput(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		kind, created credtype.Kind
		identifier    string
	}{
		"a vault type":           {credtype.KindVault, credtype.KindVault, credtype.VaultIdentifierInput},
		"a database type":        {credtype.KindDatabase, credtype.KindDatabase, credtype.DatabaseInputName},
		"a type made a vault":    {credtype.KindVault, credtype.KindCloud, credtype.VaultIdentifierInput},
		"a type made a database": {credtype.KindDatabase, credtype.KindCloud, credtype.DatabaseInputName},
	} {
		t.Run(name, func(t *testing.T) {
			store, client, orgID, _ := fixture(t)
			ct := identifierType("identified", tc.created, tc.identifier)
			stored, err := store.CreateType(ctx, orgID, ct)
			if tc.created == tc.kind {
				if errors.Is(err, credtype.ErrInvalidType) {
					return
				}
			} else {
				if err != nil {
					t.Fatalf("CreateType() = %v", err)
				}
				ct.Kind = tc.kind
				if _, err = store.UpdateType(ctx, stored.ID, ct); errors.Is(err, credtype.ErrInvalidType) {
					return
				}
			}
			if err != nil {
				t.Fatalf("the type write = %v, want ErrInvalidType or nothing", err)
			}

			var ids []int
			for _, credName := range []string{"first", "second"} {
				cred, err := store.CreateCredential(ctx, orgID, stored.ID, credName, "",
					map[string]string{"api_token": theSecret, tc.identifier: sharedIdentifier}, nil)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, cred.ID)
			}
			inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
			tmpl := client.Template.Create().SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
				SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)
			err = store.SetTemplateCredentials(ctx, tmpl.ID, ids)
			if err != nil && strings.Contains(err.Error(), sharedIdentifier) {
				t.Fatal("the type write was accepted, and refusing to bind two of its credentials quoted a secret input's value")
			}
			t.Fatalf("the type write was accepted with a secret %s input; SetTemplateCredentials() = %v", tc.identifier, err)
		})
	}
}

// TestABindingRefusalNeverQuotesAPreRuleIdentifier seeds, through the ent
// client, a vault type stored before Validate refused a secret vault_id, and
// binds two of its credentials sharing one. The refusal names the credentials
// and the rule, never the identifier, so this holds however the type came to
// be stored.
func TestABindingRefusalNeverQuotesAPreRuleIdentifier(t *testing.T) {
	ctx := context.Background()
	store, client, orgID, _ := fixture(t)
	row := client.CredentialType.Create().
		SetName("Pre-rule Vault").SetKind(string(credtype.KindVault)).SetNamespace("prerule_vault").SetManaged(false).
		SetInputs(credtype.InputSchema{Fields: []credtype.InputField{
			{ID: credtype.VaultIdentifierInput, Label: "Vault ID", Secret: true},
			{ID: credtype.VaultPasswordInput, Label: "Password", Secret: true},
		}}).
		SetInjectors(credtype.Injectors{}).SetOrganizationID(orgID).SaveX(ctx)
	var ids []int
	for _, name := range []string{"first", "second"} {
		cred, err := store.CreateCredential(ctx, orgID, row.ID, name, "",
			map[string]string{credtype.VaultIdentifierInput: sharedIdentifier, credtype.VaultPasswordInput: "pw-" + name}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cred.ID)
	}
	inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
	tmpl := client.Template.Create().SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)
	err := store.SetTemplateCredentials(ctx, tmpl.ID, ids)
	if !errors.Is(err, credtype.ErrBindingConflict) {
		t.Fatalf("SetTemplateCredentials() = %v, want ErrBindingConflict", err)
	}
	if strings.Contains(err.Error(), sharedIdentifier) {
		t.Errorf("the refusal quotes the secret identifier: %v", err)
	}
}
