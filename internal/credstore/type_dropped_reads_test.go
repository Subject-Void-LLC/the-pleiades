// This file holds what reads and binding writes do with what a credential
// stores for an input its type no longer declares (carried.go): no reader
// shows it, a write replacing the bindings keeps it, and the binding rule
// reads the credential as the run does, without it.
package credstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credentialinputsource"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// storedBindings counts the input source rows a credential has, whatever any
// reader shows of them.
func storedBindings(t *testing.T, client *ent.Client, id int) int {
	t.Helper()
	return client.CredentialInputSource.Query().
		Where(credentialinputsource.HasTargetCredentialWith(credential.IDEQ(id))).
		CountX(context.Background())
}

// TestNoReaderShowsWhatACredentialHoldsForAnInputItsTypeDropped reads the
// credential and its bindings back. The reference and the binding are not
// secret, but their input is unknown to the current schema, so a caller
// shown them could not send them back.
func TestNoReaderShowsWhatACredentialHoldsForAnInputItsTypeDropped(t *testing.T) {
	ctx := context.Background()
	store, _, id := droppedNote(t)
	read, err := store.GetCredential(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, shown := read.External["note"]; shown {
		t.Error("a read shows the reference of an input the type does not declare")
	}
	sources, err := store.ListCredentialInputSources(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.InputID == "note" {
			t.Error("the bindings list shows a binding for an input the type does not declare")
		}
	}
}

// TestReplacingBindingsKeepsACarriedOne writes the bindings three ways: back
// as they were listed, empty, and empty through a credential update. Each
// replaces the bindings of declared inputs and keeps the carried one, as an
// update keeps a carried value and reference.
func TestReplacingBindingsKeepsACarriedOne(t *testing.T) {
	ctx := context.Background()
	for name, write := range map[string]func(credstore.Store, int) error{
		"what was listed": func(store credstore.Store, id int) error {
			listed, err := store.ListCredentialInputSources(ctx, id)
			if err != nil {
				return err
			}
			bindings := make([]credstore.InputSourceBinding, 0, len(listed))
			for _, s := range listed {
				bindings = append(bindings, credstore.InputSourceBinding{
					InputID: s.InputID, SourceCredentialID: s.SourceCredentialID, Metadata: s.Metadata,
				})
			}
			_, err = store.SetCredentialInputSources(ctx, id, bindings)
			return err
		},
		"none": func(store credstore.Store, id int) error {
			_, err := store.SetCredentialInputSources(ctx, id, []credstore.InputSourceBinding{})
			return err
		},
		"none through an update": func(store credstore.Store, id int) error {
			_, err := store.UpdateCredentialWith(ctx, id, func(stored credstore.Credential) (credstore.Credential, error) {
				return stored, nil
			}, credstore.WithInputSources([]credstore.InputSourceBinding{}))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, client, id := droppedNote(t)
			if err := write(store, id); err != nil {
				t.Fatalf("replacing the bindings = %v", err)
			}
			if got := storedBindings(t, client, id); got != 1 {
				t.Errorf("after replacing the bindings the credential has %d, want the carried one kept", got)
			}
		})
	}
}

// TestTheBindingRuleReadsACredentialAsTheRunDoes binds two credentials of a
// vault type whose older release left out vault_id, one still carrying a
// value for it. The run reads only declared inputs (the resolver), so to it
// both are the default identity, and the injector refuses the pair; the
// binding rule must refuse it too, rather than accept a template no run of
// can succeed.
func TestTheBindingRuleReadsACredentialAsTheRunDoes(t *testing.T) {
	ctx := context.Background()
	store, client, orgID, _ := fixture(t)
	older := credtype.CredentialType{
		Name: "Dropped Vault", Kind: credtype.KindVault, Namespace: "dropped_vault",
		Inputs: credtype.InputSchema{
			Fields:   []credtype.InputField{{ID: credtype.VaultPasswordInput, Label: "Password", Secret: true}},
			Required: []string{credtype.VaultPasswordInput},
		},
	}
	newer := older
	newer.Inputs.Fields = append([]credtype.InputField{{ID: credtype.VaultIdentifierInput, Label: "Vault ID"}}, older.Inputs.Fields...)
	shipped, err := store.EnsureManagedType(ctx, newer)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for name, inputs := range map[string]map[string]string{
		"named":   {credtype.VaultPasswordInput: "first-password", credtype.VaultIdentifierInput: "prod"},
		"unnamed": {credtype.VaultPasswordInput: "second-password"},
	} {
		cred, err := store.CreateCredential(ctx, orgID, shipped.ID, name, "", inputs, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cred.ID)
	}
	if _, err := store.EnsureManagedType(ctx, older); err != nil {
		t.Fatal(err)
	}

	inv := client.Inventory.Create().SetName("edge").SetOrganizationID(orgID).SaveX(ctx)
	tmpl := client.Template.Create().SetName("deploy").SetKind("runbook").SetDefinition("deploy.yml").
		SetOrganizationID(orgID).SetInventoryID(inv.ID).SaveX(ctx)
	bindErr := store.SetTemplateCredentials(ctx, tmpl.ID, ids)

	resolved, err := resolve.NewEntResolver(client).Resolve(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	injector, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := injector.Inject(resolved, nil)
	if (bindErr == nil) != (runErr == nil) {
		t.Fatalf("binding them = %v and running them = %v: the binding rule and the run disagree", bindErr, runErr)
	}
	if !errors.Is(bindErr, credtype.ErrBindingConflict) {
		t.Errorf("binding them = %v, want ErrBindingConflict", bindErr)
	}
}
