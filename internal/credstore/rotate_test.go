// This file holds key rotation (internal/crypto/rotate.go), which writes
// credentials without this store and so without the type lock, to the rule
// type_inputs.go states for it: a purge that commits between rotation's read
// of a credential and its write makes the write skip that credential.
package credstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// rotationKey marks the context of the rotation pass under test.
type rotationKey struct{}

// TestRotate_StaleWriteAfterPurgeIsSkipped lands a type edit, removing the
// secret note and adding it back plain, after rotation has read the
// credential and before it writes it back. Rotation's write is one UPDATE
// conditional on the updated_at it read, and the purge's write moved
// updated_at, so the write matches nothing: the credential is skipped, and
// the value the purge deleted is not written back under a now-plain note.
func TestRotate_StaleWriteAfterPurgeIsSkipped(t *testing.T) {
	ctx := context.Background()
	client, err := ent.OpenEmbedded(ctx, filepath.Join(t.TempDir(), "rotate.db"))
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
	cred, err := store.CreateCredential(ctx, org.ID, ct.ID, "noted", "",
		map[string]string{"api_token": theSecret, "note": noteSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Registered after the decrypting interceptor, so it runs inside it: the
	// rows rotation holds are read before the edit.
	var once sync.Once
	editErr := errors.New("the type edit never ran after rotation's read")
	client.Credential.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(qctx context.Context, q ent.Query) (ent.Value, error) {
			rows, err := next.Query(qctx, q)
			if qctx.Value(rotationKey{}) != nil {
				once.Do(func() {
					_, removeErr := store.UpdateType(ctx, ct.ID, noteType(nil))
					_, addErr := store.UpdateType(ctx, ct.ID, noteType(plainNote))
					editErr = errors.Join(removeErr, addErr)
				})
			}
			return rows, err
		})
	}))

	count, err := crypto.RotateCredentialInputs(context.WithValue(ctx, rotationKey{}, true), client, svc)
	if err != nil {
		t.Fatalf("RotateCredentialInputs() = %v", err)
	}
	if editErr != nil {
		t.Fatal(editErr)
	}
	if count.Rotated != 0 || count.Skipped != 1 {
		t.Errorf("rotation rotated %d and skipped %d, want the purged credential skipped", count.Rotated, count.Skipped)
	}
	if _, held := client.Credential.GetX(ctx, cred.ID).Inputs["note"]; held {
		t.Error("rotation wrote back a value the purge deleted")
	}
	read, err := store.GetCredential(ctx, cred.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Inputs["note"] == noteSecret {
		t.Error("a value written as secret is shown as plain")
	}
}
