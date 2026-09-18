// Tests for the rotation the controller runs on ROTATE_ENCRYPTION_KEYS, through
// the crypto hooks main installs.
package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// rotationTestKeys are the old and new master keys the rotation tests move
// between.
var (
	rotationOldKey = []byte(strings.Repeat("o", 32))
	rotationNewKey = []byte(strings.Repeat("n", 32))
)

// openHooked opens dbPath through this binary's own installCryptoHooks, so
// the test composes exactly what main composes.
func openHooked(t *testing.T, dbPath string, svc *crypto.EnvelopeService) *ent.Client {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	installCryptoHooks(client, svc)
	return client
}

// TestRunKeyRotation_RotatesCredentialsAndSaysTheOldKeyCanGo is the
// regression test for the defect that made the production guide's rotation
// procedure destroy data: the controller rotated devices alone, so a
// credential stayed on the old key and was lost the moment an operator
// removed MASTER_ENCRYPTION_KEY_PREVIOUS as the guide told them to.
//
// It runs the function main runs, through the hooks main installs, then
// removes the previous key and reads the credential back.
func TestRunKeyRotation_RotatesCredentialsAndSaysTheOldKeyCanGo(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rotation.db")

	svcOld, err := crypto.NewEnvelopeService(rotationOldKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(old) error = %v", err)
	}
	seed := openHooked(t, dbPath, svcOld)
	org := seed.Organization.Create().SetName("acme").SaveX(ctx)
	ct := seed.CredentialType.Create().
		SetName("Custom API").SetKind("cloud").SetNamespace("custom_api").
		SetOrganization(org).SaveX(ctx)
	credID := seed.Credential.Create().
		SetName("api").SetOrganization(org).SetCredentialType(ct).
		SetInputs(map[string]string{"api_token": "rotate-me"}).
		SaveX(ctx).ID

	svcRotating, err := crypto.NewEnvelopeService(rotationNewKey, "v2", rotationOldKey, "v1")
	if err != nil {
		t.Fatalf("NewEnvelopeService(rotating) error = %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	runKeyRotation(ctx, openHooked(t, dbPath, svcRotating), svcRotating, logger)

	out := logs.String()
	for _, table := range []string{"credentials", "devices", `"saved launch configurations"`, `"mesh signing keys"`} {
		if !strings.Contains(out, "table="+table) {
			t.Errorf("the rotation log has no line for %s:\n%s", table, out)
		}
	}
	if !strings.Contains(out, "key rotation complete: no row needs MASTER_ENCRYPTION_KEY_PREVIOUS any more") {
		t.Fatalf("the rotation did not report itself complete:\n%s", out)
	}

	svcNewOnly, err := crypto.NewEnvelopeService(rotationNewKey, "v2", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(new only) error = %v", err)
	}
	cred, err := openHooked(t, dbPath, svcNewOnly).Credential.Get(ctx, credID)
	if err != nil {
		t.Fatalf("Credential.Get() error = %v", err)
	}
	if cred.Inputs["api_token"] != "rotate-me" {
		t.Fatalf("after removing the previous key the credential reads %v: rotation left it on the old key", cred.Inputs)
	}
}

// TestRunKeyRotation_SaysKeepThePreviousKeyWhenATableCannotBeRead proves the
// log never tells an operator the old key can go when a table was not read.
func TestRunKeyRotation_SaysKeepThePreviousKeyWhenATableCannotBeRead(t *testing.T) {
	svc, err := crypto.NewEnvelopeService(rotationNewKey, "v2", rotationOldKey, "v1")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client := openHooked(t, filepath.Join(t.TempDir(), "closed.db"), svc)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var logs bytes.Buffer
	runKeyRotation(context.Background(), client, svc, slog.New(slog.NewTextHandler(&logs, nil)))

	out := logs.String()
	if strings.Contains(out, "key rotation complete") {
		t.Fatalf("the rotation reported itself complete without reading any table:\n%s", out)
	}
	if !strings.Contains(out, "keep MASTER_ENCRYPTION_KEY_PREVIOUS set") {
		t.Fatalf("the rotation did not tell the operator to keep the previous key:\n%s", out)
	}
}
