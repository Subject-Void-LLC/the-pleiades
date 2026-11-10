// This file holds a credential whose type no longer declares an input it
// stores for: the rollback window, where an older build's reconcile leaves
// out an input a newer build shipped and keeps what credentials hold for it
// (TestEnsureManagedTypeNeverDeletesValuesOfAnInputItDoesNotShip).
package credstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// rotatedToken is the value a rotation writes over theSecret.
const rotatedToken = "sk-live-ROTATED-CANARY-0a1b2c3d4e5f"

// droppedNote makes a managed type with a secret note, a credential storing
// a value, a reference and a source binding for it, and then reconciles the
// type as an older build would, without the note. It returns the store, the
// client and the credential's id.
func droppedNote(t *testing.T) (credstore.Store, *ent.Client, int) {
	t.Helper()
	ctx := context.Background()
	store, client, orgID, _, sourceID := sourceFixture(t)
	newer := noteType(secretNote)
	newer.Namespace = "dropped_note"
	stored, err := store.EnsureManagedType(ctx, newer)
	if err != nil {
		t.Fatalf("EnsureManagedType(newer) = %v", err)
	}
	cred, err := store.CreateCredential(ctx, orgID, stored.ID, "noted", "",
		map[string]string{"api_token": theSecret, "note": noteSecret}, map[string]string{"note": "file:dropped_note"},
		credstore.WithInputSources(bind("note", sourceID)))
	if err != nil {
		t.Fatal(err)
	}
	older := noteType(nil)
	older.Namespace = newer.Namespace
	if _, err := store.EnsureManagedType(ctx, older); err != nil {
		t.Fatalf("EnsureManagedType(older) = %v", err)
	}
	return store, client, cred.ID
}

// TestACredentialWhoseTypeDroppedAnInputCanStillBeUpdated rotates the token
// of a credential inside the rollback window, through both write forms. The
// value and the reference its type no longer declares were stored under a
// schema that did, so the update carries them as they are, neither checked
// against nor dropped by the current one, and no read shows them.
func TestACredentialWhoseTypeDroppedAnInputCanStillBeUpdated(t *testing.T) {
	ctx := context.Background()
	for name, update := range map[string]func(credstore.Store, int) error{
		"UpdateCredential": func(store credstore.Store, id int) error {
			read, err := store.GetCredential(ctx, id)
			if err != nil {
				return err
			}
			_, err = store.UpdateCredential(ctx, id, read.Name, read.Description,
				map[string]string{"api_token": rotatedToken}, read.External)
			return err
		},
		"UpdateCredentialWith": func(store credstore.Store, id int) error {
			_, err := store.UpdateCredentialWith(ctx, id, func(stored credstore.Credential) (credstore.Credential, error) {
				stored.Inputs = map[string]string{"api_token": rotatedToken}
				return stored, nil
			})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, client, id := droppedNote(t)
			if err := update(store, id); err != nil {
				t.Fatalf("rotating the token of a credential whose type dropped an input failed: %v", err)
			}
			row := client.Credential.GetX(ctx, id)
			if row.Inputs["api_token"] != rotatedToken {
				t.Error("the rotation did not store the new token")
			}
			if row.Inputs["note"] != noteSecret || row.External["note"] != "file:dropped_note" {
				t.Errorf("the update did not carry what the type no longer declares: inputs %d keys, external %v",
					len(row.Inputs), row.External)
			}
			if got := storedBindings(t, client, id); got != 1 {
				t.Errorf("the update did not carry the note's source binding: %d bindings", got)
			}
			read, err := store.GetCredential(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, shown := read.Inputs["note"]; shown {
				t.Error("a read shows a value under an input the type does not declare")
			}
		})
	}
}

// TestAnUpdateNamingAnInputTheTypeDoesNotDeclareIsRefused is the other half:
// carrying a stored value is not writing one, so a caller sending a value or
// a new reference for an input the type does not declare is still refused.
func TestAnUpdateNamingAnInputTheTypeDoesNotDeclareIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		inputs, external map[string]string
	}{
		"a value":         {map[string]string{"note": "a new note"}, map[string]string{"note": "file:dropped_note"}},
		"a new reference": {nil, map[string]string{"note": "file:another_note"}},
	} {
		t.Run(name, func(t *testing.T) {
			store, client, id := droppedNote(t)
			_, err := store.UpdateCredential(ctx, id, "noted", "", tc.inputs, tc.external)
			if !errors.Is(err, credtype.ErrInvalidCredential) {
				t.Fatalf("UpdateCredential() = %v, want ErrInvalidCredential", err)
			}
			if row := client.Credential.GetX(ctx, id); row.Inputs["note"] != noteSecret || row.External["note"] != "file:dropped_note" {
				t.Error("the refused update changed the credential")
			}
		})
	}
}

// TestUpdatingACredentialThisControllerCannotReadIsRefused holds what the
// carrying rule must not reach: a credential whose stored values this
// Controller cannot decrypt reads back as one sealed key, which no type
// declares, and carrying it would seal the old ciphertext inside a new
// envelope as though it were a value.
func TestUpdatingACredentialThisControllerCannotReadIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sealed.db")
	client, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))
	store := credstore.NewEntStore(client, render.New())
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct, err := store.CreateType(ctx, org.ID, noteType(secretNote))
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateCredential(ctx, org.ID, ct.ID, "sealed", "", map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	sealed := map[string]string{crypto.EncryptedKeyMarker: "v1:not-a-real-envelope"}
	raw.Credential.UpdateOneID(cred.ID).SetInputs(sealed).ExecX(ctx)

	_, err = store.UpdateCredential(ctx, cred.ID, "sealed", "", map[string]string{"api_token": rotatedToken}, nil)
	if !errors.Is(err, credstore.ErrInUse) {
		t.Fatalf("UpdateCredential() = %v, want ErrInUse", err)
	}
	if got := raw.Credential.GetX(ctx, cred.ID).Inputs; len(got) != 1 || got[crypto.EncryptedKeyMarker] != sealed[crypto.EncryptedKeyMarker] {
		t.Error("the refused update rewrote the sealed values")
	}
}
