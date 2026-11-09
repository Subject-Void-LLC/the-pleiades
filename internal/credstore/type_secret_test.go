// This file holds the store to the rule that a value written as secret is
// never shown as plain, against a real database with the encryption hooks
// registered the way the composition root registers them.
package credstore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// Readers redact from the type's current schema, so the guarantee lives in
// the one method every type edit reaches, UpdateType, and in the managed
// reconcile beside it.

// noteSecret is the value these tests store under the note input.
const noteSecret = "sk-live-NOTE-CANARY-5d4c3b2a1f0e"

// noteSchema is a type's input schema with the note input as given, or
// without it when note is nil.
func noteSchema(note *credtype.InputField) credtype.InputSchema {
	fields := []credtype.InputField{{ID: "api_token", Label: "Token", Secret: true}}
	if note != nil {
		fields = append(fields, *note)
	}
	fields = append(fields, credtype.InputField{ID: "api_url", Label: "URL"})
	return credtype.InputSchema{Fields: fields, Required: []string{"api_token"}}
}

// noteType is a custom type whose note input is as given.
func noteType(note *credtype.InputField) credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Noted API",
		Kind:      credtype.KindCloud,
		Namespace: "noted_api",
		Inputs:    noteSchema(note),
		Injectors: credtype.Injectors{Env: map[string]string{"API_TOKEN": "{{ api_token }}"}},
	}
}

// The note input as each test writes it.
var (
	secretNote = &credtype.InputField{ID: "note", Label: "Note", Secret: true}
	plainNote  = &credtype.InputField{ID: "note", Label: "Note"}
)

// notedFixture is the shared fixture plus a type with a secret note input
// and one credential storing a value for it, with the note also read
// through a source binding and api_url read from an external reference.
func notedFixture(t *testing.T) (credstore.Store, *ent.Client, int, int) {
	t.Helper()
	ctx := context.Background()
	store, client, orgID, _, sourceID := sourceFixture(t)

	ct, err := store.CreateType(ctx, orgID, noteType(secretNote))
	if err != nil {
		t.Fatalf("CreateType() error = %v", err)
	}
	cred, err := store.CreateCredential(ctx, orgID, ct.ID, "noted", "",
		map[string]string{"api_token": theSecret, "note": noteSecret},
		map[string]string{"api_url": "file:noted_url"},
		credstore.WithInputSources(bind("note", sourceID)))
	if err != nil {
		t.Fatalf("CreateCredential() error = %v", err)
	}
	return store, client, ct.ID, cred.ID
}

// TestUpdateTypeRefusesMakingASecretInputPlain is the one-step exposure,
// refused at the store so every caller of UpdateType meets it.
func TestUpdateTypeRefusesMakingASecretInputPlain(t *testing.T) {
	ctx := context.Background()
	store, client, typeID, credID := notedFixture(t)

	_, err := store.UpdateType(ctx, typeID, noteType(plainNote))
	if !errors.Is(err, credtype.ErrInvalidType) {
		t.Fatalf("UpdateType() clearing secret = %v, want ErrInvalidType", err)
	}
	for _, want := range []string{`"note"`, "remove it and add it again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	if got := client.Credential.GetX(ctx, credID).Inputs["note"]; got != noteSecret {
		t.Error("a refused flip changed the stored note")
	}
	if ct, _ := store.GetType(ctx, typeID); !ct.Inputs.Fields[1].Secret {
		t.Error("a refused flip changed the type")
	}
}

// TestUpdateTypeRemovingAnInputDeletesWhatCredentialsHoldForIt is the
// two-step exposure: removal deletes the stored value, the external
// reference and the source binding, so adding the input back non-secret
// finds nothing, and the credential can be edited in between.
func TestUpdateTypeRemovingAnInputDeletesWhatCredentialsHoldForIt(t *testing.T) {
	ctx := context.Background()
	store, client, typeID, credID := notedFixture(t)

	withoutNote := noteType(nil)
	withoutNote.Inputs.Fields = withoutNote.Inputs.Fields[:1] // api_url goes too
	if _, err := store.UpdateType(ctx, typeID, withoutNote); err != nil {
		t.Fatalf("UpdateType() removing note and api_url = %v", err)
	}
	row := client.Credential.GetX(ctx, credID)
	if _, held := row.Inputs["note"]; held || row.Inputs["api_token"] != theSecret {
		t.Errorf("after the removal the row holds %d inputs, want api_token kept and note gone", len(row.Inputs))
	}
	if len(row.External) != 0 {
		t.Errorf("after removing api_url the row keeps external %v", row.External)
	}
	if sources, err := store.ListCredentialInputSources(ctx, credID); err != nil || len(sources) != 0 {
		t.Errorf("after removing note its source binding remains: %v, %v", sources, err)
	}
	if _, err := store.UpdateCredential(ctx, credID, "noted renamed", "", nil, nil); err != nil {
		t.Errorf("UpdateCredential() after the removal = %v, want the credential editable", err)
	}

	if _, err := store.UpdateType(ctx, typeID, noteType(plainNote)); err != nil {
		t.Fatalf("UpdateType() adding note back = %v", err)
	}
	if _, held := client.Credential.GetX(ctx, credID).Inputs["note"]; held {
		t.Error("adding note back found a value stored for it")
	}
}

// TestUpdateTypeDeletesValuesAnEarlierRemovalLeftBehind covers a value left
// by a removal made before this rule, seeded by writing the type row through
// the ent client as the old store did. Any later write to the type that does
// not add the input back as secret deletes it. Adding it back as secret keeps
// it, and that is safe: it reads back only as the marker, and the credential
// can be edited again, since its key is declared once more.
func TestUpdateTypeDeletesValuesAnEarlierRemovalLeftBehind(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		edit credtype.CredentialType
		kept bool
	}{
		"a description edit":    {func() credtype.CredentialType { ct := noteType(nil); ct.Description = "cleaned"; return ct }(), false},
		"adding it back plain":  {noteType(plainNote), false},
		"adding it back secret": {noteType(secretNote), true},
	} {
		t.Run(name, func(t *testing.T) {
			store, client, typeID, credID := notedFixture(t)
			client.CredentialType.UpdateOneID(typeID).SetInputs(noteSchema(nil)).ExecX(ctx)
			if _, err := store.UpdateCredential(ctx, credID, "renamed", "", nil, nil); !errors.Is(err, credtype.ErrInvalidCredential) {
				t.Fatalf("UpdateCredential() with a stale value = %v, want the old refusal", err)
			}

			if _, err := store.UpdateType(ctx, typeID, tc.edit); err != nil {
				t.Fatalf("UpdateType() = %v", err)
			}
			held, kept := client.Credential.GetX(ctx, credID).Inputs["note"]
			if kept != tc.kept || (kept && held != noteSecret) {
				t.Errorf("after the type write the value is kept %v, want %v", kept, tc.kept)
			}
			read, err := store.GetCredential(ctx, credID)
			if err != nil {
				t.Fatal(err)
			}
			if shown := read.Inputs["note"]; shown == noteSecret || (tc.kept && shown != redact.Marker) {
				t.Errorf("the read shows note as %q, want the marker or nothing", shown)
			}
			if _, err := store.UpdateCredential(ctx, credID, "renamed", "", nil, nil); err != nil {
				t.Errorf("UpdateCredential() after the cleanup = %v, want the credential editable", err)
			}
		})
	}
}

// TestUpdateTypeReAddingARemovedInputFindsNothing confirms the secret add
// rule changes nothing after a removal made under this rule: the removal
// deleted the values, so adding the input back, secret or not, finds none.
func TestUpdateTypeReAddingARemovedInputFindsNothing(t *testing.T) {
	ctx := context.Background()
	for name, note := range map[string]*credtype.InputField{"as secret": secretNote, "as plain": plainNote} {
		t.Run(name, func(t *testing.T) {
			store, client, typeID, credID := notedFixture(t)
			for _, ct := range []credtype.CredentialType{noteType(nil), noteType(note)} {
				if _, err := store.UpdateType(ctx, typeID, ct); err != nil {
					t.Fatalf("UpdateType() = %v", err)
				}
			}
			if _, held := client.Credential.GetX(ctx, credID).Inputs["note"]; held {
				t.Error("adding a removed input back found a value stored for it")
			}
		})
	}
}

// TestUpdateTypeRefusesWhileACredentialCannotBeRead fails closed: a
// credential of the type whose inputs this Controller cannot decrypt might
// hold a value for the input being removed, so the edit is refused and the
// ciphertext is left exactly as it was rather than overwritten.
func TestUpdateTypeRefusesWhileACredentialCannotBeRead(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.db")
	client, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
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
	cred, err := store.CreateCredential(ctx, org.ID, ct.ID, "noted", "", map[string]string{"api_token": theSecret, "note": noteSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A second client with no hooks writes a sealed value this key cannot
	// open, as a corrupted row or one sealed under another key would be.
	raw, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	unreadable := map[string]string{crypto.EncryptedKeyMarker: "v1:not-a-real-envelope"}
	raw.Credential.UpdateOneID(cred.ID).SetInputs(unreadable).ExecX(ctx)

	if _, err := store.UpdateType(ctx, ct.ID, noteType(nil)); !errors.Is(err, credstore.ErrInUse) {
		t.Fatalf("UpdateType() with an unreadable credential = %v, want ErrInUse", err)
	}
	if got := raw.Credential.GetX(ctx, cred.ID).Inputs; len(got) != 1 || got[crypto.EncryptedKeyMarker] != unreadable[crypto.EncryptedKeyMarker] {
		t.Errorf("the refused edit rewrote the unreadable ciphertext: %v", got)
	}
	if got, _ := store.GetType(ctx, ct.ID); len(got.Inputs.Fields) != 3 {
		t.Error("the refused edit changed the type")
	}
}

// TestEnsureManagedTypeHoldsTheSameRule holds the platform's own reconcile
// to the rule: a release cannot make a shipped secret input plain, and a
// value a removal leaves behind is deleted by the release that adds the
// input back, so it is never shown under an input that is not secret.
func TestEnsureManagedTypeHoldsTheSameRule(t *testing.T) {
	ctx := context.Background()
	store, client, orgID, _ := fixture(t)
	shipped := noteType(secretNote)
	shipped.Namespace = "shipped_noted"
	managed, err := store.EnsureManagedType(ctx, shipped)
	if err != nil {
		t.Fatalf("EnsureManagedType() = %v", err)
	}
	cred, err := store.CreateCredential(ctx, orgID, managed.ID, "noted", "", map[string]string{"api_token": theSecret, "note": noteSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}

	plain := noteType(plainNote)
	plain.Namespace = shipped.Namespace
	if _, err := store.EnsureManagedType(ctx, plain); !errors.Is(err, credtype.ErrInvalidType) {
		t.Fatalf("EnsureManagedType() clearing secret = %v, want ErrInvalidType", err)
	}
	if client.Credential.GetX(ctx, cred.ID).Inputs["note"] != noteSecret {
		t.Error("a refused reconcile changed the stored note")
	}

	removed := noteType(nil)
	removed.Namespace = shipped.Namespace
	if _, err := store.EnsureManagedType(ctx, removed); err != nil {
		t.Fatalf("EnsureManagedType() removing note = %v", err)
	}
	if client.Credential.GetX(ctx, cred.ID).Inputs["note"] != noteSecret {
		t.Error("a reconcile removing note deleted its stored value, which a newer build may own")
	}
	if _, err := store.EnsureManagedType(ctx, plain); err != nil {
		t.Fatalf("EnsureManagedType() adding note back plain = %v", err)
	}
	if _, held := client.Credential.GetX(ctx, cred.ID).Inputs["note"]; held {
		t.Error("a reconcile adding note back plain kept the value stored while it was secret")
	}
}

// TestEnsureManagedTypeNeverDeletesValuesOfAnInputItDoesNotShip is the
// mixed-version window of LESSONS 415 and 422: the stored managed type was
// written by a newer build and declares an input this build does not ship,
// and this build's reconcile runs. The value a credential holds for that
// input, and its reference, belong to the newer build's release data, so
// the older reconcile leaves them alone.
func TestEnsureManagedTypeNeverDeletesValuesOfAnInputItDoesNotShip(t *testing.T) {
	ctx := context.Background()
	store, client, orgID, _ := fixture(t)
	newer := noteType(secretNote)
	newer.Namespace = "shipped_newer"
	stored, err := store.EnsureManagedType(ctx, newer)
	if err != nil {
		t.Fatalf("EnsureManagedType(newer) = %v", err)
	}
	cred, err := store.CreateCredential(ctx, orgID, stored.ID, "noted", "",
		map[string]string{"api_token": theSecret, "note": noteSecret}, map[string]string{"note": "file:newer_note"})
	if err != nil {
		t.Fatal(err)
	}

	older := noteType(nil)
	older.Namespace = newer.Namespace
	if _, err := store.EnsureManagedType(ctx, older); err != nil {
		t.Fatalf("EnsureManagedType(older) = %v", err)
	}
	row := client.Credential.GetX(ctx, cred.ID)
	if row.Inputs["note"] != noteSecret || row.External["note"] != "file:newer_note" {
		t.Errorf("an older reconcile deleted a newer build's input: inputs %d keys, external %v", len(row.Inputs), row.External)
	}
}

// TestReconcileStopsAtAnUnreadableCredentialAndSaysSo pins what an operator
// sees when one credential of a managed type cannot be decrypted and a
// release adds a non-secret input to that type, the one reconcile that
// purges: the type is left as it was, for every organization, the reconcile
// reports it, and the Controller logs one ERROR line naming the type and the
// credential.
func TestReconcileStopsAtAnUnreadableCredentialAndSaysSo(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reconcile.db")
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

	shipped := noteType(nil)
	shipped.Namespace = "shipped_unreadable"
	managed, err := store.EnsureManagedType(ctx, shipped)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.CreateCredential(ctx, org.ID, managed.ID, "unreadable", "", map[string]string{"api_token": theSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.Credential.UpdateOneID(cred.ID).SetInputs(map[string]string{crypto.EncryptedKeyMarker: "v1:not-a-real-envelope"}).ExecX(ctx)

	var logged bytes.Buffer
	release := noteType(plainNote)
	release.Namespace = shipped.Namespace
	err = credstore.ReconcileManaged(ctx, store, []credtype.CredentialType{release}, slog.New(slog.NewTextHandler(&logged, nil)))
	if err == nil {
		t.Fatal("ReconcileManaged() = nil, want the unreadable credential reported")
	}
	for _, want := range []string{"level=ERROR", "failed to reconcile a managed credential type", "namespace=shipped_unreadable",
		fmt.Sprintf("credential %d's stored values cannot be read with this Controller's key", cred.ID)} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log does not say %q:\n%s", want, logged.String())
		}
	}
	if got, _ := store.GetType(ctx, managed.ID); len(got.Inputs.Fields) != 2 {
		t.Error("the refused reconcile changed the type")
	}
}

// TestEnsureManagedTypeRoundTripKeepsASecretInputsValues is the rollback
// window both ways: a newer build ships note, an older build's reconcile
// leaves it out, and the newer build's next reconcile adds it back. A secret
// note keeps its values, shown only as the marker, since nothing can show
// them as plain; a non-secret note is purged, the cost the release-ordinal
// fix (LESSONS 422) would remove.
func TestEnsureManagedTypeRoundTripKeepsASecretInputsValues(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		note *credtype.InputField
		kept bool
	}{
		"a secret input":     {secretNote, true},
		"a non-secret input": {plainNote, false},
	} {
		t.Run(name, func(t *testing.T) {
			store, client, orgID, _ := fixture(t)
			newer := noteType(tc.note)
			newer.Namespace = "round_trip"
			stored, err := store.EnsureManagedType(ctx, newer)
			if err != nil {
				t.Fatal(err)
			}
			cred, err := store.CreateCredential(ctx, orgID, stored.ID, "noted", "",
				map[string]string{"api_token": theSecret, "note": noteSecret}, nil)
			if err != nil {
				t.Fatal(err)
			}
			older := noteType(nil)
			older.Namespace = newer.Namespace
			for _, release := range []credtype.CredentialType{older, newer} {
				if _, err := store.EnsureManagedType(ctx, release); err != nil {
					t.Fatalf("EnsureManagedType() = %v", err)
				}
			}

			held, kept := client.Credential.GetX(ctx, cred.ID).Inputs["note"]
			if kept != tc.kept || (kept && held != noteSecret) {
				t.Fatalf("after the round trip note is kept %v, want %v", kept, tc.kept)
			}
			read, err := store.GetCredential(ctx, cred.ID)
			if err != nil {
				t.Fatal(err)
			}
			if shown := read.Inputs["note"]; shown == noteSecret || (tc.kept && shown != redact.Marker) {
				t.Errorf("the read shows note as %q, want the marker or nothing", shown)
			}
		})
	}
}

// TestUpdateTypeWithHoldsAFoldThatEditsInPlace hands UpdateTypeWith a fold
// that clears the secret flag on the very field it was given rather than on
// a copy. The store compares the new schema with the stored one after the
// fold runs, so the stored one must not share the fold's slices, or the
// flip would compare with itself and pass.
func TestUpdateTypeWithHoldsAFoldThatEditsInPlace(t *testing.T) {
	ctx := context.Background()
	store, client, typeID, credID := notedFixture(t)

	_, err := store.UpdateTypeWith(ctx, typeID, func(stored credstore.CredentialType) (credtype.CredentialType, error) {
		for i := range stored.Inputs.Fields {
			stored.Inputs.Fields[i].Secret = false
		}
		return stored.CredentialType, nil
	})
	if !errors.Is(err, credtype.ErrInvalidType) {
		t.Fatalf("UpdateTypeWith() clearing secret in place = %v, want ErrInvalidType", err)
	}
	if read, _ := store.GetCredential(ctx, credID); read.Inputs["note"] == noteSecret {
		t.Error("a fold editing in place made a stored secret readable")
	}
	if client.Credential.GetX(ctx, credID).Inputs["note"] != noteSecret {
		t.Error("the refused edit changed the stored note")
	}
}
