package crypto_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers envelope encryption of Credential.inputs.
//
// Like launch_hook_test.go, the assertions read the raw bytes back through
// a second, plain database/sql handle rather than through ent, because
// ent's own read path is the thing that decrypts. Asserting through it
// would prove only that a round trip round-trips, which is true of no
// encryption at all.
//
// Unlike that file, this one also has a binding to prove. The bound
// envelope's whole purpose is that a ciphertext cannot be moved between
// rows, and the test that matters most here does the moving with raw SQL,
// the way a database-level attacker would.

// testAPIToken is the credential this file follows through storage.
const testAPIToken = "sk-live-not-a-real-token-9f8e7d6c5b4a"

// credentialFixture opens a real on-disk database with the hook and
// interceptor registered, and seeds the type and organization a credential
// hangs off.
func credentialFixture(t *testing.T) (*ent.Client, string, int, int) {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "credential_hook_test.db")
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().
		SetName("Custom API").
		SetKind(string(credtype.KindCloud)).
		SetNamespace("custom_api").
		SetInputs(credtype.InputSchema{Fields: []credtype.InputField{
			{ID: "api_token", Label: "Token", Secret: true},
		}}).
		SaveX(ctx)

	return client, dbPath, org.ID, ct.ID
}

// rawInputs reads a credential's inputs column as stored, bypassing ent.
func rawInputs(t *testing.T, dbPath string, id int) string {
	t.Helper()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var stored string
	if err := db.QueryRow("SELECT inputs FROM credentials WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatalf("reading raw inputs: %v", err)
	}
	return stored
}

// TestCredentialInputsAreEncryptedAtRest is the baseline claim.
func TestCredentialInputsAreEncryptedAtRest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, dbPath, orgID, typeID := credentialFixture(t)

	cred := client.Credential.Create().
		SetName("prod api").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": testAPIToken}).
		SaveX(ctx)

	stored := rawInputs(t, dbPath, cred.ID)
	if strings.Contains(stored, testAPIToken) {
		t.Fatalf("the token is in the database in plaintext: %s", stored)
	}
	if !strings.Contains(stored, crypto.EncryptedKeyMarker) {
		t.Errorf("the stored value does not look encrypted: %s", stored)
	}

	// And it reads back through ent.
	got := client.Credential.GetX(ctx, cred.ID)
	if got.Inputs["api_token"] != testAPIToken {
		t.Errorf("Inputs[api_token] = %q, want the original token", got.Inputs["api_token"])
	}
}

// TestRelocatingStoredCiphertextBetweenCredentialsFails is the test this
// whole binding exists for, done at the layer the attack happens at.
//
// The attacker here has write access to the database and no ability to
// decrypt anything. They copy the victim credential's opaque inputs column
// onto a credential they control, then have the platform read it. Without
// the binding that works, and the platform then injects the victim's token
// into the attacker's job.
func TestRelocatingStoredCiphertextBetweenCredentialsFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, dbPath, orgID, typeID := credentialFixture(t)

	victim := client.Credential.Create().
		SetName("victim").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": testAPIToken}).
		SaveX(ctx)

	attacker := client.Credential.Create().
		SetName("attacker").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": "the-attackers-own-token"}).
		SaveX(ctx)

	// The relocation, as one UPDATE against an opaque column.
	stolen := rawInputs(t, dbPath, victim.ID)
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.Exec("UPDATE credentials SET inputs = ? WHERE id = ?", stolen, attacker.ID); err != nil {
		db.Close()
		t.Fatalf("relocating the ciphertext: %v", err)
	}
	db.Close()

	// Reading the attacker's credential must not yield the victim's token.
	got := client.Credential.GetX(ctx, attacker.ID)
	if got.Inputs["api_token"] == testAPIToken {
		t.Fatal("a relocated ciphertext decrypted into another credential, so the binding is not doing anything")
	}
	// The interceptor leaves an unreadable row encrypted rather than
	// failing the whole query, so what comes back is the raw marker.
	if _, stillSealed := got.Inputs[crypto.EncryptedKeyMarker]; !stillSealed {
		t.Errorf("expected the relocated row to read back still sealed, got %v", got.Inputs)
	}
}

// TestCredentialInputsSurviveAnUpdate covers the second binding path, where
// the UUID comes from the existing row rather than from the mutation.
func TestCredentialInputsSurviveAnUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, dbPath, orgID, typeID := credentialFixture(t)

	cred := client.Credential.Create().
		SetName("rotating").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": "the-old-token-value"}).
		SaveX(ctx)

	const rotated = "sk-live-the-rotated-token-value"
	client.Credential.UpdateOneID(cred.ID).
		SetInputs(map[string]string{"api_token": rotated}).
		ExecX(ctx)

	stored := rawInputs(t, dbPath, cred.ID)
	if strings.Contains(stored, rotated) {
		t.Fatalf("the rotated token is in the database in plaintext: %s", stored)
	}

	got := client.Credential.GetX(ctx, cred.ID)
	if got.Inputs["api_token"] != rotated {
		t.Errorf("Inputs[api_token] = %q, want the rotated token", got.Inputs["api_token"])
	}
}

// TestBulkUpdatingCredentialInputsIsRefused pins the deliberate refusal.
//
// Every row has its own binding, so one encrypted value written across many
// rows could be correct for at most one of them. Refusing is the honest
// outcome; silently writing something that decrypts nowhere would be worse,
// and writing plaintext would be worst.
func TestBulkUpdatingCredentialInputsIsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _, orgID, typeID := credentialFixture(t)

	client.Credential.Create().
		SetName("one").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": "a-token-value"}).
		SaveX(ctx)

	_, err := client.Credential.Update().
		SetInputs(map[string]string{"api_token": "bulk-written-value"}).
		Save(ctx)
	if err == nil {
		t.Fatal("a bulk update of credential inputs succeeded, which cannot bind each row correctly")
	}
	if !errors.Is(err, crypto.ErrBulkCredentialInputs) {
		t.Errorf("error = %v, want one matching ErrBulkCredentialInputs", err)
	}
}

// TestCredentialWithNoInputsIsUntouched covers the empty case, which a
// credential resolved entirely from an external secret manager has.
func TestCredentialWithNoInputsIsUntouched(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _, orgID, typeID := credentialFixture(t)

	cred := client.Credential.Create().
		SetName("external only").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetExternal(map[string]string{"api_token": "vault:secret/data/api#token"}).
		SaveX(ctx)

	got := client.Credential.GetX(ctx, cred.ID)
	if len(got.Inputs) != 0 {
		t.Errorf("Inputs = %v, want empty", got.Inputs)
	}
	// external is deliberately NOT encrypted: a Vault path is a pointer to
	// a secret, not a secret, and encrypting it would make "which
	// credentials point at this mount" unanswerable during a migration.
	if got.External["api_token"] != "vault:secret/data/api#token" {
		t.Errorf("External[api_token] = %q, want the reference stored as written", got.External["api_token"])
	}
}

// TestExternalReferencesAreReadableInTheDatabase states the deliberate
// asymmetry as a test rather than only as a comment, so that somebody
// later "fixing" it by encrypting external has to delete an assertion that
// says why it is that way.
func TestExternalReferencesAreReadableInTheDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, dbPath, orgID, typeID := credentialFixture(t)

	const ref = "vault:secret/data/prod/api#token"
	cred := client.Credential.Create().
		SetName("external").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetExternal(map[string]string{"api_token": ref}).
		SaveX(ctx)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var stored string
	if err := db.QueryRow("SELECT external FROM credentials WHERE id = ?", cred.ID).Scan(&stored); err != nil {
		t.Fatalf("reading raw external: %v", err)
	}
	if !strings.Contains(stored, ref) {
		t.Errorf("the external reference is not queryable in the database: %s", stored)
	}
}

// TestCredentialSecretBindingIsGeneratedAndStable covers the column the
// whole binding rests on: it must exist without the caller setting it, and
// it must not change.
func TestCredentialSecretBindingIsGeneratedAndStable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _, orgID, typeID := credentialFixture(t)

	first := client.Credential.Create().
		SetName("one").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": "a-token-value"}).
		SaveX(ctx)
	second := client.Credential.Create().
		SetName("two").
		SetOrganizationID(orgID).
		SetCredentialTypeID(typeID).
		SetInputs(map[string]string{"api_token": "a-token-value"}).
		SaveX(ctx)

	if first.SecretBinding == "" {
		t.Fatal("secret_binding was not generated")
	}
	if first.SecretBinding == second.SecretBinding {
		t.Error("two credentials share a secret binding, so their ciphertext is interchangeable")
	}

	// An update must not change it, or every existing ciphertext for that
	// row becomes unopenable.
	client.Credential.UpdateOneID(first.ID).SetName("renamed").ExecX(ctx)
	after := client.Credential.GetX(ctx, first.ID)
	if after.SecretBinding != first.SecretBinding {
		t.Error("secret_binding changed on update, which would orphan the row's own ciphertext")
	}
	if after.Inputs["api_token"] != "a-token-value" {
		t.Errorf("inputs became unreadable after an unrelated update: %v", after.Inputs)
	}
}
