// This file holds the credential form's writer to answering, beside a
// control, the refusal a real store makes for a credential whose stored
// values this Controller cannot decrypt.
package credentials

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestUpdatingAnUnreadableCredentialIsAFieldFault saves the edit form over a
// credential whose stored values read back sealed, through the real store,
// and expects the refusal on the name control rather than a 500.
func TestUpdatingAnUnreadableCredentialIsAFieldFault(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "form.db")
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
	ct, err := store.CreateType(ctx, org.ID, credtype.CredentialType{
		Name: "Form API", Kind: credtype.KindCloud, Namespace: "form_api",
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "api_token", Label: "Token", Secret: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateCredential(ctx, org.ID, ct.ID, "sealed", "", map[string]string{"api_token": "a-token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.Credential.UpdateOneID(cred.ID).SetInputs(map[string]string{crypto.EncryptedKeyMarker: "v1:not-a-real-envelope"}).ExecX(ctx)

	err = writer{store}.Update(ctx, strconv.Itoa(cred.ID), credstore.Credential{Name: "renamed", Inputs: map[string]string{"api_token": "another"}})
	var fault view.FieldFault
	if !errors.As(err, &fault) || fault.Field != "name" {
		t.Fatalf("Update() = %v, want a fault on the name control", err)
	}
	if !strings.Contains(fault.Message, "cannot be read with this Controller's key") {
		t.Errorf("the fault says %q, which does not say why", fault.Message)
	}
}
