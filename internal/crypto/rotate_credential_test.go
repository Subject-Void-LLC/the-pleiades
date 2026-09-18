package crypto_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// Credential key rotation, proven the way TestRotateDeviceProperties proves
// the Device pass: a real on-disk SQLite file, seeded through one client
// under an old key, rotated through a second client whose previous slot
// holds that old key, and then checked at BOTH layers. The storage layer
// must show the new version tag, and the domain layer must still decrypt.
//
// One assertion here has no counterpart in the Device test, and it is the
// one that matters most: rotation must not weaken the binding. A credential
// ciphertext is sealed against that row's own secret_binding, and re-sealing
// it under a new key is exactly the moment that property could be lost by
// accident. The relocation attack is therefore attempted AFTER the rotation
// rather than before it.

const (
	credOldKey = "cccccccccccccccccccccccccccccccc"
	credNewKey = "dddddddddddddddddddddddddddddddd"
)

// credentialFixture seeds two credentials under an old key and returns the
// path of the database holding them.
func credentialRotationFixture(t *testing.T) (string, []int) {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rotate_credential_test.db")

	svcOld, err := crypto.NewEnvelopeService([]byte(credOldKey), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(old) error = %v", err)
	}
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	client.Credential.Use(crypto.CredentialInputsHook(svcOld))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svcOld))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().
		SetName("Custom API").SetKind("cloud").SetNamespace("custom_api").
		SetOrganization(org).SaveX(ctx)

	ids := make([]int, 0, 2)
	for _, name := range []string{"first", "second"} {
		row := client.Credential.Create().
			SetName(name).
			SetOrganization(org).
			SetCredentialType(ct).
			SetInputs(map[string]string{"api_token": "secret-for-" + name}).
			SaveX(ctx)
		ids = append(ids, row.ID)
	}
	return dbPath, ids
}

// openRotated returns a client whose current key is the new one and whose
// previous slot holds the old one, which is what a real operator has after
// moving MASTER_ENCRYPTION_KEY into MASTER_ENCRYPTION_KEY_PREVIOUS.
func openRotated(t *testing.T, dbPath string) (*ent.Client, *crypto.EnvelopeService) {
	t.Helper()

	svc, err := crypto.NewEnvelopeService([]byte(credNewKey), "v2", []byte(credOldKey), "v1")
	if err != nil {
		t.Fatalf("NewEnvelopeService(new) error = %v", err)
	}
	client, err := ent.OpenEmbedded(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))
	return client, svc
}

// storedInputs reads a credential's raw stored inputs, bypassing every hook
// and interceptor, which is the only way to see what is actually on disk.
func storedInputs(t *testing.T, dbPath string, id int) string {
	t.Helper()

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	var raw string
	if err := rawDB.QueryRowContext(context.Background(),
		"SELECT inputs FROM credentials WHERE id = ?", id).Scan(&raw); err != nil {
		t.Fatalf("reading stored inputs for credential %d: %v", id, err)
	}
	return raw
}

// TestRotateCredentialInputs is the pass's real caller and proof.
func TestRotateCredentialInputs(t *testing.T) {
	ctx := context.Background()
	dbPath, ids := credentialRotationFixture(t)

	// Before: every row carries the old version tag.
	for _, id := range ids {
		if got := storedInputs(t, dbPath, id); !strings.Contains(got, "v1$") {
			t.Fatalf("credential %d is not stored under v1 before rotation: %s", id, got)
		}
	}

	client, _ := openRotated(t, dbPath)

	rotated, err := crypto.RotateCredentialInputs(ctx, client, nil)
	if err != nil {
		t.Fatalf("RotateCredentialInputs() error = %v", err)
	}
	if rotated.Rotated != len(ids) {
		t.Fatalf("RotateCredentialInputs() rotated %d rows, want %d", rotated.Rotated, len(ids))
	}

	// Storage layer: every row now carries the new version tag, and still
	// the BOUND algorithm tag. Losing the second would be rotation quietly
	// downgrading every credential to the relocatable form.
	for _, id := range ids {
		raw := storedInputs(t, dbPath, id)
		if !strings.Contains(raw, "v2$") {
			t.Errorf("credential %d is not stored under v2 after rotation: %s", id, raw)
		}
		if !strings.Contains(raw, "AES256GCM-AAD") {
			t.Errorf("credential %d lost its bound envelope during rotation: %s", id, raw)
		}
	}

	// Domain layer: the values are unchanged.
	for i, id := range ids {
		row, err := client.Credential.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%d) error = %v", id, err)
		}
		want := "secret-for-" + []string{"first", "second"}[i]
		if row.Inputs["api_token"] != want {
			t.Errorf("credential %d = %q after rotation, want %q", id, row.Inputs["api_token"], want)
		}
	}
}

// TestRotationDoesNotWeakenTheBinding is the negative control, and it is
// this file's reason for existing.
//
// A credential's ciphertext is sealed against that row's own
// secret_binding, so relocating one row's inputs onto another must not
// decrypt. Re-sealing under a new key is exactly where that could be lost,
// because the obvious implementation of a rotation (decrypt, encrypt,
// write) has to be handed the binding again and would silently produce an
// unbound ciphertext if it were not.
func TestRotationDoesNotWeakenTheBinding(t *testing.T) {
	ctx := context.Background()
	dbPath, ids := credentialRotationFixture(t)

	client, _ := openRotated(t, dbPath)
	if _, err := crypto.RotateCredentialInputs(ctx, client, nil); err != nil {
		t.Fatalf("RotateCredentialInputs() error = %v", err)
	}

	// Relocate the first credential's freshly rotated ciphertext onto the
	// second row, at the storage layer, which is exactly the privilege the
	// bound envelope exists to defeat: a database writer who cannot read
	// anything.
	stolen := storedInputs(t, dbPath, ids[0])

	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()
	if _, err := rawDB.ExecContext(ctx, "UPDATE credentials SET inputs = ? WHERE id = ?", stolen, ids[1]); err != nil {
		t.Fatalf("relocating the ciphertext: %v", err)
	}

	// The interceptor leaves an unopenable row sealed rather than failing
	// the query, so the relocated row reads back still encrypted: the
	// marker is present and the first credential's value is not.
	row, err := client.Credential.Get(ctx, ids[1])
	if err != nil {
		t.Fatalf("Get() after relocation error = %v", err)
	}
	if row.Inputs["api_token"] == "secret-for-first" {
		t.Fatal("a relocated ciphertext decrypted after rotation: the binding was lost")
	}
	if _, sealed := row.Inputs[crypto.EncryptedKeyMarker]; !sealed {
		t.Errorf("the relocated row = %v, want it left sealed rather than opened", row.Inputs)
	}
}

// TestRotationSkipsAnUnreadableRowRatherThanAborting covers the pass's own
// tolerance. One unreadable credential must not stop an operator rotating
// every other one, which during an incident is the difference between a
// rotation that completes and one that cannot be started.
func TestRotationSkipsAnUnreadableRowRatherThanAborting(t *testing.T) {
	ctx := context.Background()
	dbPath, ids := credentialRotationFixture(t)

	// Corrupt the first row's ciphertext at the storage layer. It stays
	// well formed enough to be a string in the marker's place, which is
	// what an undecryptable row actually looks like.
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() { _ = rawDB.Close() }()
	corrupt, err := json.Marshal(map[string]string{
		crypto.EncryptedKeyMarker: "v1$AES256GCM-AAD$bm90$cmVhbA==",
	})
	if err != nil {
		t.Fatalf("building the corrupt row: %v", err)
	}
	// Built from the marker constant rather than written out, so a rename
	// of it cannot leave this test silently corrupting nothing. The first
	// draft did hardcode it, used redact's marker by mistake, and the row
	// read back as ordinary plaintext.
	if _, err := rawDB.ExecContext(ctx,
		"UPDATE credentials SET inputs = ? WHERE id = ?", string(corrupt), ids[0]); err != nil {
		t.Fatalf("corrupting the row: %v", err)
	}

	client, _ := openRotated(t, dbPath)

	rotated, err := crypto.RotateCredentialInputs(ctx, client, nil)
	if err != nil {
		t.Fatalf("RotateCredentialInputs() error = %v, want the pass to complete", err)
	}
	if rotated.Rotated != 1 {
		t.Fatalf("RotateCredentialInputs() rotated %d rows, want the one readable row", rotated.Rotated)
	}
	if rotated.Unreadable != 1 || rotated.Skipped != 0 {
		t.Fatalf("RotateCredentialInputs() = %+v, want the undecryptable row counted as unreadable and nothing skipped", rotated)
	}

	// The healthy row moved to the new key.
	if raw := storedInputs(t, dbPath, ids[1]); !strings.Contains(raw, "v2$") {
		t.Errorf("the readable credential was not rotated: %s", raw)
	}
	// The unreadable one was left exactly as it was, not overwritten with
	// its own sealed shape re-encrypted as though it were plaintext.
	if raw := storedInputs(t, dbPath, ids[0]); !strings.Contains(raw, "bm90") {
		t.Errorf("the unreadable credential was modified: %s", raw)
	}
}

// TestRotationLeavesACredentialWithNoInputsAlone covers the ordinary row
// that stores nothing, which every deployment binding only external sources
// now has after Phase 78a.
func TestRotationLeavesACredentialWithNoInputsAlone(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "empty.db")

	svc, err := crypto.NewEnvelopeService([]byte(credOldKey), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client, err := ent.OpenEmbedded(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().
		SetName("t").SetKind("cloud").SetNamespace("t").SetOrganization(org).SaveX(ctx)
	client.Credential.Create().
		SetName("external only").SetOrganization(org).SetCredentialType(ct).
		SetExternal(map[string]string{"api_token": "file:token"}).
		SaveX(ctx)

	rotated, err := crypto.RotateCredentialInputs(ctx, client, nil)
	if err != nil {
		t.Fatalf("RotateCredentialInputs() error = %v", err)
	}
	if rotated.Rotated != 0 {
		t.Errorf("RotateCredentialInputs() rotated %d rows, want none for a credential storing nothing", rotated.Rotated)
	}
}
